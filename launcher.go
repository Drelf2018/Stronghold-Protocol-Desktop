//go:build windows

package main

// The game behind the window: where its source comes from, how it is prepared, and the server
// process this program owns.
//
// dsh's desktop app could lean on npm for the whole of it - npx fetched the package and ran it.
// This game is not published (package.json says private, and there is no bin to run), so the
// source comes from GitHub instead: the archive endpoint serves a tarball, which needs neither
// git nor npm to fetch. Everything after that is the game's own preparation script,
// tools/setup.mjs - the same one scripts/start-windows.bat runs before it starts the server.

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// Where the game's server listens and what the window opens. Fixed, because the game's
	// multiplayer is one backend everybody points at: two players are two connections to the *same*
	// port, not two servers. 0.0.0.0 for the bind address is the game's own default and the one that
	// lets the other 1-3 players reach it - 建房 hands out a 4-character key to share.
	gamePort    = "3000"
	gameAddress = "http://127.0.0.1:3000/"
	gameHealth  = "http://127.0.0.1:3000/healthz"
	gameDial    = "127.0.0.1:3000"
	gameBind    = "0.0.0.0"

	// roomCodeLen is how long the game's room keys are (shared/constants.js). A key typed on its own
	// is turned into an invite link instead of being taken for a host name; the game's own lobby does
	// the same with what is pasted into it (public/js/screens/lobby.js).
	roomCodeLen = 4

	// sourceURL is the archive of the repository's default branch. It is the same file GitHub's
	// "Download ZIP" gives, and it is fetched directly rather than through git: a launcher that
	// needs git installed is a launcher that fails on a machine that only has Node.js.
	sourceURL = "https://codeload.github.com/sganggs/Stronghold-Protocol/tar.gz/refs/heads/master"

	// minNodeMajor is what the game itself requires (package.json: engines.node >= 22).
	minNodeMajor = 22

	// How long the server is given to answer /healthz once it has been started, and how often it
	// is asked. A cold start reads the game data, which takes a moment.
	gameWait = 60 * time.Second
	gamePoll = 500 * time.Millisecond

	// probeWait is how long /healthz is given to answer when the program asks whether a game is already
	// up, before deciding to start one of its own.
	probeWait = 2 * time.Second

	// setupWait caps the preparation step. The first run downloads about 250 MB of art, so this
	// is a ceiling for a stuck download, not a budget anybody expects to spend.
	setupWait = 30 * time.Minute

	// creationNoWindow is CreateProcess's CREATE_NO_WINDOW. This program has no console of its
	// own, so a console program started without this flag is given a brand new console window -
	// the black box that flashes past. Every child process goes through hiddenCommand.
	creationNoWindow = 0x08000000
)

// noGame is set by -no-game or --no-game: nothing is started, and the window is opened against
// whatever is already listening on the game's port.
var noGame bool

// hiddenCommand is a child process that is never given a console window of its own.
func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: creationNoWindow}
	return cmd
}

// gameCommand runs something inside the game directory.
//
// NO_COLOR: setup.mjs paints its output green and yellow, and the paint would land in the log as
// escape sequences. The variable is the switch that script reads.
func gameCommand(name string, args ...string) *exec.Cmd {
	cmd := hiddenCommand(name, args...)
	cmd.Dir = gameDir()
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	return cmd
}

// gameWriter passes a child's output to the log. The log is the only place it can go: this
// program has no console to write it to.
type gameWriter struct{ what string }

func (w gameWriter) Write(p []byte) (int, error) {
	if text := strings.TrimRight(string(p), "\r\n"); text != "" {
		slog.Info("output", "what", w.what, "line", text)
	}
	return len(p), nil
}

// probeGame asks the one question worth asking about the game: is its server answering. /healthz is
// the endpoint the project's own Docker healthcheck uses.
func probeGame(timeout time.Duration) bool {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(gameHealth)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// busy reports whether anything answers on a TCP address.
//
// It connects rather than binds. A listen of our own - even for a moment, even on loopback - is a
// new program asking Windows' firewall for an entry, and this program already has one for the
// server it starts.
//
// It is also how the port is checked after the server has failed to come up: something else holding
// 3000 is a clearer answer than "the game did not start".
func busy(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// findNode locates the Node.js the game runs on, and refuses one that is too old.
//
// The absolute path is kept and used for every child rather than the bare name: this program is
// started by Explorer, and the PATH it sees is the one that was there at logon.
func findNode() (string, error) {
	exe, err := exec.LookPath("node")
	if err != nil {
		return "", err
	}
	// hiddenCommand, not exec.Command: node is a console program and this program has no console
	// of its own, so a plain exec.Command here makes Windows allocate a brand new console window
	// for it - one black window flashing past on every start, for a version string. Every child
	// this program starts goes through hiddenCommand for the same reason.
	out, err := hiddenCommand(exe, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", exe, err)
	}
	version := strings.TrimSpace(string(out))
	major, err := parseNodeMajor(version)
	if err != nil {
		return "", err
	}
	if major < minNodeMajor {
		return "", fmt.Errorf("node %s 太旧，需要 %d 或更高", version, minNodeMajor)
	}
	slog.Info("node", "path", exe, "version", version)
	return exe, nil
}

// parseNodeMajor reads the major version out of what "node --version" prints ("v24.16.0").
func parseNodeMajor(version string) (int, error) {
	fields := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".", 2)
	major, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, fmt.Errorf("看不懂 node 的版本号 %q", version)
	}
	return major, nil
}

// sourceReady reports whether the game's source is unpacked and looks whole. The lock file is
// part of the check because tools/setup.mjs runs npm ci, which needs it.
func sourceReady() bool {
	for _, name := range []string{
		"package.json",
		"package-lock.json",
		filepath.Join("server", "index.js"),
		filepath.Join("tools", "setup.mjs"),
	} {
		if _, err := os.Stat(filepath.Join(gameDir(), name)); err != nil {
			return false
		}
	}
	return true
}

// setupNeeded is the cheap version of what tools/setup.mjs --check decides: are the things a run
// needs already there?
//
// It exists so that an ordinary start does not pay for a Node.js process, a dependency check and
// a walk over 250 MB of art on every launch. It is deliberately generous - one missing piece and
// the preparation step runs, which re-checks all of it properly and repairs what it finds. The
// one thing it cannot see is a single missing art file, which is why the menu has
// 重新准备游戏文件.
func setupNeeded() bool {
	for _, name := range []string{
		filepath.Join("node_modules", "ws", "package.json"),
		filepath.Join("public", "vendor", "pixi.min.js"),
		filepath.Join("data", "stages.json"),
		"public/assets",
	} {
		if _, err := os.Stat(filepath.Join(gameDir(), name)); err != nil {
			return true
		}
	}
	return false
}

// fetchSource downloads the repository's archive and unpacks it into the game directory.
//
// It is an overlay: files that are there are replaced, files that are not are added. That is what
// makes an update cheap - node_modules and the downloaded art stay where they are - and it is
// also why the game directory is never wiped.
func fetchSource() error {
	// 先问清这次取的是哪一版，再下归档。顺序是有意的：等归档下完再问，拿到的可能是另一个 commit
	// 的 hash——master 在这几分钟里动了的话，记下来的就对不上刚解包的那份代码了。
	//
	// 问不到也不拦：源码该取还得取，只是这一份的 hash 记不下来（标题栏于是不写 hash）。
	revision := ""
	if hash, err := upstreamRevision(); err != nil {
		slog.Warn("game source revision: cannot ask which commit this is", "error", err)
	} else {
		revision = hash
	}

	slog.Info("fetching the game source", "url", sourceURL, "revision", shortHash(revision))
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Get(sourceURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	files, err := unpackArchive(resp.Body, gameDir())
	if err != nil {
		return err
	}
	// 归档已经解开了，才把 hash 记下来：记的是"磁盘上这一份代码来自哪个 commit"。
	if revision != "" {
		if err := saveRevision(revision); err != nil {
			slog.Warn("game source revision: cannot record it", "error", err)
		}
	}
	slog.Info("game source unpacked", "dir", gameDir(), "files", files, "revision", shortHash(revision))
	return nil
}

// unpackArchive unpacks a .tar.gz into root, overwriting what is there and creating what is not,
// and answers how many files it wrote.
func unpackArchive(r io.Reader, root string) (int, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	files := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return files, err
		}
		rel, ok := archivePath(hdr.Name)
		if !ok {
			continue
		}
		target := filepath.Join(root, rel)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return files, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return files, err
			}
			if err := writeFile(target, tr); err != nil {
				return files, err
			}
			files++
		}
	}
	return files, nil
}

// archivePath turns the name of one tar entry into a path under the game directory.
//
// false means "skip": the archive's single top-level folder itself, and anything that would land
// outside the game directory. A tar carries names as written, so an archive is hostile until every
// name has been through this - including the one this program fetches.
func archivePath(name string) (string, bool) {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == "." || clean == ".." || path.IsAbs(clean) || strings.HasPrefix(clean, "../") {
		return "", false
	}
	// 去掉归档最外层那个目录：GitHub 打出来的包总是整个套在 <仓库>-<分支>/ 下面。
	parts := strings.Split(clean, "/")
	if len(parts) < 2 {
		return "", false
	}
	rel := path.Join(parts[1:]...)
	if rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return filepath.FromSlash(rel), true
}

// writeFile writes one entry out. The file is truncated rather than removed and recreated, so an
// overlay update keeps the timestamps of everything the archive does not carry.
func writeFile(target string, src io.Reader) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// runSetup is the game's own preparation step: dependencies, the vendored browser libraries, the
// game data, and - unless they are already there - the art and audio. It is what
// scripts/start-windows.bat runs before the server, and it is safe to run again: every step
// checks first, and the asset download resumes where it stopped.
func runSetup(node string) error {
	script := filepath.Join(gameDir(), "tools", "setup.mjs")
	// --no-local: the optional extraction from an installed Arknights client asks a question, and
	// this program has no terminal to answer it on.
	cmd := gameCommand(node, script, "--quiet", "--no-local")
	cmd.Stdout, cmd.Stderr = gameWriter{"setup"}, gameWriter{"setup"}
	slog.Info("preparing the game", "script", script)
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			return err
		}
		slog.Info("the game is prepared")
		return nil
	case <-time.After(setupWait):
		// 一条卡住不动的下载不该把窗口永远留在「正在准备」那一页上。
		killTree(cmd.Process.Pid)
		return fmt.Errorf("准备步骤超过 %s 没有结束", setupWait)
	}
}

// The server this program started, if it started one. Nothing else may be killed, and a server
// the user already had running is not ours to stop.
var (
	gameCmd    *exec.Cmd
	gameIsOurs bool

	// gameDone is closed when that process ends, so that waiting for /healthz can give up as soon
	// as the server has died rather than sitting out the whole timeout.
	gameDone chan struct{}
)

// startGame starts the server. It returns as soon as the process is up; whether it stays up is
// what waitHealthy answers.
func startGame(node string) error {
	cmd := gameCommand(node, filepath.Join(gameDir(), "server", "index.js"))
	cmd.Env = append(cmd.Env, "PORT="+gamePort, "HOST="+gameBind)
	cmd.Stdout, cmd.Stderr = gameWriter{"game"}, gameWriter{"game"}
	if err := cmd.Start(); err != nil {
		return err
	}
	gameCmd, gameIsOurs = cmd, true
	gameDone = make(chan struct{})
	slog.Info("game server started", "pid", cmd.Process.Pid)
	containGame(cmd.Process.Pid)
	go func() {
		err := cmd.Wait()
		slog.Info("the game server ended", "pid", cmd.Process.Pid, "error", err)
		close(gameDone)
	}()
	return nil
}

// waitHealthy waits for /healthz to answer, and gives up early when the server has already
// exited - which is what taking 3000 does: the server prints EADDRINUSE and quits.
func waitHealthy(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if probeGame(probeWait) {
			return true
		}
		select {
		case <-gameDone:
			return false
		default:
		}
		time.Sleep(gamePoll)
	}
	return false
}

// drop removes a file or a directory, and treats "it was not there" as success - which is what
// os.RemoveAll already does for a missing path. It exists so that the two deletions in updateGame
// read the same and report a real failure the same way.
func drop(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// prepareLockPath is the file two copies of this program take turns on while they write into the
// game directory.
func prepareLockPath() string { return filepath.Join(appDataDir(), "prepare.lock") }

// lockFile takes the exclusive byte-range lock on path, and answers a function that lets it go.
//
// A file lock rather than a named mutex or a semaphore. A Windows mutex belongs to the *thread* that
// took it, and Go moves goroutines between threads underneath - releasing it from another one fails
// with ERROR_NOT_OWNER. A file lock belongs to the *handle*, so one goroutine may take it and another
// may let it go; and the system drops it when the process ends, so a copy killed in the middle of a
// download leaves nothing stuck behind.
//
// Waiting is bounded by setupWait: the copy that waits here is waiting for a 250 MB download it
// would otherwise be doing itself, so the ceiling is the same one that download gets.
func lockFile(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(setupWait + time.Minute)
	waited := false
	for {
		err := windows.LockFileEx(windows.Handle(file.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			file.Close()
			return nil, err
		}
		if !waited {
			waited = true
			slog.Info("another copy is preparing the game; waiting for it", "path", path)
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, fmt.Errorf("等了 %s 也没等到准备锁", setupWait)
		}
		time.Sleep(time.Second)
	}
	return func() {
		// 和第 1 个字节那个范围同一个：解锁要的偏移量对得上，句柄一关锁也会没。
		_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
		file.Close()
	}, nil
}

// prepareFiles makes the game directory ready to run, and answers the page to show when it cannot.
//
// Everything in here writes into game\: the archive is unpacked over it, npm ci rewrites
// node_modules inside it, fetch-assets refills public/. Two copies doing that at the same time
// corrupt each other - npm ci is the obvious one, but the unpacking truncates and rewrites every
// file it carries as well - so this is the one part of a run that takes turns. Running two copies is
// still the point; they only queue here, and the one that waited usually finds the work already done.
func prepareFiles(node string, forceSetup bool) string {
	release, err := lockFile(prepareLockPath())
	if err != nil {
		slog.Error("preparation lock", "error", err)
		return setupFailedPage()
	}
	defer release()

	// 等到手是有意义的：等在门口的这段时间里，前一份可能正好把源码、依赖和素材都弄好了。所以
	// 每一项都要在拿到锁之后再判断一次，而不是进门之前。
	if !sourceReady() {
		slog.Info("the game source is not there yet")
		if err := fetchSource(); err != nil {
			slog.Error("fetching the game source", "error", err)
			return sourceFailedPage()
		}
	}
	if forceSetup || setupNeeded() {
		if err := runSetup(node); err != nil {
			slog.Error("preparing the game", "error", err)
			return setupFailedPage()
		}
	}
	// 源码已经在磁盘上、这一轮没有重新取的场合（绝大多数启动都是这样），也把"这是哪一版"记下来。
	//
	// 没有这一句，本机的 hash 就只可能来自"取源码"那一次：装过一次之后再也不重新取，而这个记录
	// 又是在那之后才加进来的安装，就会永远读不到本机 hash——标题栏上那一半永远是空的。这正是
	// 这一版第一次上线时的样子。
	recordRevision()
	return ""
}

// updateGame fetches the newest source over what is there, and clears what has to be rebuilt so
// that the preparation which follows really does follow.
//
// This is the one part of 更新游戏 the game's own scripts cannot do. tools/setup.mjs checks what is
// *derived* - node_modules, public/vendor, data, assets - and has no idea whether the code itself
// changed. Upstream's answer is a `git pull`, which fetchSource stands in for; there is no git here,
// and deliberately so, because a launcher that needs git is a launcher that fails on a machine that
// only has Node.js.
//
// Two things are removed, and each is a switch inside somebody else's script:
//
//   - node_modules. setup.mjs runs npm ci only when a package's directory is *missing*, and that
//     test cannot see a version change: after an update, three@0.186 would satisfy it while the new
//     code wants 0.190. Removing the tree is the only thing that makes it run.
//   - the two upstream index tables. assets/cache.mjs downloads them when they are missing and
//     otherwise reuses them for ever, so a stale audio_data.json means the new version's operators
//     never enter the asset manifest at all.
func updateGame() error {
	// 更新也写 game\，所以和准备步骤共用同一把锁。
	release, err := lockFile(prepareLockPath())
	if err != nil {
		return err
	}
	defer release()

	before := localRevision()
	if err := fetchSource(); err != nil {
		return err
	}
	if err := drop(filepath.Join(gameDir(), "node_modules")); err != nil {
		return err
	}
	for _, rel := range []string{
		filepath.Join(".cache", "gamedata", "excel", "audio_data.json"),
		filepath.Join(".cache", "ark-models", "models_data.json"),
	} {
		if err := drop(filepath.Join(gameDir(), rel)); err != nil {
			return err
		}
	}
	slog.Info("game source updated", "from", shortHash(before), "to", shortHash(localRevision()))
	return nil
}

// recordRevision writes down which commit the source on disk came from.
//
// 它答的是"这份代码是哪一版"，而本机没有任何 git 元数据可读（源码是 tar.gz 解包的），所以只能问
// 上游要当前那个 commit。它因此有一处诚实的局限，写在日志里而不写在标题栏上：源码如果能重新取，
// 记的是确切的；如果只是"原来就在磁盘上"，记的是**此刻上游的位置**——两者只有在磁盘那一份没有
// 落后于上游时才是同一个。差别写在日志里，由读日志的人判断。
//
// 取不到就当没这回事：少一行显示，不该让准备游戏失败。
func recordRevision() {
	if localRevision() != "" {
		return
	}
	hash, err := upstreamRevision()
	if err != nil {
		slog.Warn("game source revision: cannot tell which commit this is", "error", err)
		return
	}
	if err := saveRevision(hash); err != nil {
		slog.Warn("game source revision: cannot record it", "error", err)
		return
	}
	slog.Info("game source revision recorded", "revision", shortHash(hash), "gameVersion", gameVersionNote())
}

// gameVersionNote 只是留给日志的一句话：这一份源码自己报的版本号（game\package.json 里的
// version）。它不参与任何判断——判断用的是 commit。
func gameVersionNote() string {
	data, err := os.ReadFile(filepath.Join(gameDir(), "package.json"))
	if err != nil {
		return "?"
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil || pkg.Version == "" {
		return "?"
	}
	return pkg.Version
}

// prepareGame brings the game up, and reports what the window should say when it cannot. An empty
// answer means the server is answering and the window can be pointed at it.
//
// forceSetup runs the preparation step even when nothing looks missing. That is what the menu
// item 重新准备游戏文件 asks for - the asset download is the one thing setupNeeded cannot see the
// state of.
func prepareGame(forceSetup bool) string {
	// 端口上已经有游戏在回应就直接开它，不再起第二个——联机要的是同一个后端，多起一个反而是
	// 另一局。这也是双击第二次会看见同一局的原因。
	if probeGame(probeWait) {
		slog.Info("a game server is already answering; nothing to start")
		return ""
	}

	node, err := findNode()
	if err != nil {
		slog.Error("node", "error", err)
		return nodeMissingPage()
	}
	if page := prepareFiles(node, forceSetup); page != "" {
		return page
	}
	if err := startGame(node); err != nil {
		slog.Error("starting the game server", "error", err)
		return startFailedPage()
	}
	if !waitHealthy(gameWait) {
		slog.Error("the game server never answered", "waited", gameWait)
		// 服务没起来而端口上有东西：多半是别的程序占着 3000，那比「游戏自身起不来」更能说清楚。
		if busy(gameDial) {
			return portBusyPage()
		}
		return startFailedPage()
	}
	slog.Info("the game is ready", "address", gameAddress)
	return ""
}

// gameJob holds the server this run started. Its one rule is
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: when the last handle to the job closes - which happens when
// this process ends, by any means at all, a crash and a kill from the Task Manager included -
// Windows ends every process in it.
//
// This is what keeps a crash from leaving a server behind. An orphaned server keeps answering on
// the port, and the next run then finds it taken, starts nothing, and waits for something that
// will never happen.
var gameJob windows.Handle

// containGame puts a started server under that job. Failure is logged and survived: the graceful
// path still stops the server, and a process that is already in a job this process may not modify
// refuses the assignment.
func containGame(pid int) {
	if gameJob == 0 {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			slog.Error("job object: cannot create it", "error", err)
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			slog.Error("job object: cannot set its limits", "error", err)
			windows.CloseHandle(h)
			return
		}
		gameJob = h
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		slog.Error("job object: cannot open the process", "pid", pid, "error", err)
		return
	}
	defer windows.CloseHandle(p)
	if err := windows.AssignProcessToJobObject(gameJob, p); err != nil {
		slog.Error("job object: cannot assign the process", "pid", pid, "error", err)
		return
	}
	slog.Info("the server is in the job: it ends when this program does", "pid", pid)
}

// stopGame ends the server, but only if this program started it.
func stopGame() {
	if !gameIsOurs || gameCmd == nil || gameCmd.Process == nil {
		return
	}
	pid := gameCmd.Process.Pid
	gameCmd, gameIsOurs = nil, false

	// 关掉 job 本身就结束了整棵树，连 taskkill 够不着的进程也一并结束。Windows 从这一步起就在
	// 收它了，所以先给它一点时间自己走完——机器上那一次就是这样：服务已经结束，而下面那条
	// taskkill 还是补了一刀，拿回一个 128（找不到这个进程），在白跑一趟之余还记了一条警告。
	if gameJob != 0 {
		windows.CloseHandle(gameJob)
		gameJob = 0
	}
	if gameDone != nil {
		select {
		case <-gameDone:
			slog.Info("stopped the game server", "pid", pid)
			return
		case <-time.After(2 * time.Second):
		}
	}

	// 还没走：可能卡在一个不理会关门的子进程上，那就只好点名结束它。
	slog.Info("stopping the game server", "pid", pid)
	killTree(pid)
}

// killTree ends a process and everything it started.
func killTree(pid int) {
	done := hiddenCommand("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	done.Stdout, done.Stderr = io.Discard, io.Discard
	if err := done.Run(); err != nil {
		slog.Warn("taskkill", "pid", pid, "error", err)
	}
}
