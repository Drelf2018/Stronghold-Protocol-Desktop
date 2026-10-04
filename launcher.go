//go:build windows

package main

// 窗口背后的游戏：它的源码从哪里来，怎么准备，以及本程序所占有的那个服务器进程。
//
// dsh 的桌面应用整件事都能靠 npm——npx 取包并运行它。这个游戏没有发布
// （package.json 写着 private，也没有可执行的 bin），所以源码改从 GitHub 取：
// 归档端点提供 tarball，取它既不需要 git 也不需要 npm。之后的一切都是游戏自己的
// 准备脚本 tools/setup.mjs——scripts/start-windows.bat 启动服务器前跑的就是它。

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
	// 游戏的服务器在哪里监听，窗口打开哪个地址。写死是因为游戏的联机只有一个
	// 所有人都指向的后端：两名玩家是对**同一个**端口的两条连接，而不是两个服务器。
	// 绑定地址用 0.0.0.0 是游戏自己的默认值，也是让另外 1-3 名玩家能连上它的
	// 那个值——建房 会发一个 4 字符的密钥用来分享。
	gamePort    = "3000"
	gameAddress = "http://127.0.0.1:3000/"
	gameHealth  = "http://127.0.0.1:3000/healthz"
	gameDial    = "127.0.0.1:3000"
	gameBind    = "0.0.0.0"

	// roomCodeLen 是游戏房间密钥的长度（shared/constants.js）。单独输入一个密钥时，
	// 它会被转成邀请链接，而不是被当成主机名；游戏自己的大厅对粘贴进去的内容
	// 也是这么处理的（public/js/screens/lobby.js）。
	roomCodeLen = 4

	// sourceURL 是仓库默认分支的归档。它就是 GitHub「Download ZIP」给的那个文件，
	// 而且是直接取下来的，不走 git：一个要求装好 git 的启动器，是一台只有 Node.js 的
	// 机器上会失败的启动器。
	sourceURL = "https://codeload.github.com/sganggs/Stronghold-Protocol/tar.gz/refs/heads/master"

	// minNodeMajor 是游戏自己要求的版本（package.json：engines.node >= 22）。
	minNodeMajor = 22

	// 服务器启动之后，给它多久来回应 /healthz，以及多久问它一次。冷启动要读取游戏数据，
	// 需要一点时间。
	gameWait = 60 * time.Second
	gamePoll = 500 * time.Millisecond

	// probeWait 是本程序在决定自己起一个之前，问「是不是已经有游戏在跑」时，给 /healthz
	// 多久来回应。
	probeWait = 2 * time.Second

	// setupWait 给准备这一步设上限。首次运行要下载约 250 MB 美术素材，所以这是给一次
	// 卡住的下载设的天花板，不是谁打算花掉的预算。
	setupWait = 30 * time.Minute

	// localWait 给可选的「从本机已安装的明日方舟客户端提取」设上限。在装了它的机器上
	// 要花 1-5 分钟，它装的 Python 依赖约 40 MB——所以这又是一个天花板，不是预算。
	localWait = 15 * time.Minute

	// creationNoWindow 是 CreateProcess 的 CREATE_NO_WINDOW。本程序自己没有控制台，
	// 所以不加这个标志启动的控制台程序会被分到一个全新的控制台窗口——一闪而过的黑框。
	// 每个子进程都走 hiddenCommand。
	creationNoWindow = 0x08000000
)

// noGame 由 -no-game 或 --no-game 设置：什么都不启动，窗口直接对着游戏端口上
// 已经在监听的那个开。
var noGame bool

// hiddenCommand 是一个永远不会被分到自己的控制台窗口的子进程。
func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: creationNoWindow}
	return cmd
}

// gameCommand 在游戏目录里运行某个东西。
//
// NO_COLOR：setup.mjs 会把它的输出涂成绿色和黄色，而这些颜色落进日志里就成了转义序列。
// 这个变量就是那个脚本读取的开关。
func gameCommand(name string, args ...string) *exec.Cmd {
	cmd := hiddenCommand(name, args...)
	cmd.Dir = gameDir()
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	return cmd
}

// gameWriter 把子进程的输出送进日志。日志是它唯一能去的地方：本程序没有控制台
// 可以写。
type gameWriter struct{ what string }

func (w gameWriter) Write(p []byte) (int, error) {
	if text := strings.TrimRight(string(p), "\r\n"); text != "" {
		slog.Info("output", "what", w.what, "line", text)
	}
	return len(p), nil
}

// probeGame 问的是关于这个游戏唯一值得问的问题：它的服务器在回应吗。/healthz 是
// 项目自己的 Docker healthcheck 用的那个端点。
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

// busy 报告某个 TCP 地址上有没有东西在应答。
//
// 它去连接，而不是去监听。自己监听一次——哪怕只是一瞬，哪怕只在环回上——都是一个
// 新程序在向 Windows 防火墙要一条放行规则，而本程序已经为它启动的服务器要过一条了。
//
// 服务器没能起来之后，端口也是这样检查的：3000 被别的东西占着，比「游戏没启动」
// 是更清楚的答案。
func busy(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// findNode 找到游戏运行的 Node.js，并拒绝太旧的版本。
//
// 保留绝对路径并把它用在每个子进程上，而不是用光秃秃的名字：本程序是被资源管理器
// 启动的，它看到的 PATH 是登录时的那一份。
func findNode() (string, error) {
	exe, err := exec.LookPath("node")
	if err != nil {
		return "", err
	}
	// 用 hiddenCommand，不是 exec.Command：node 是控制台程序，而本程序自己没有控制台，
	// 所以这里用裸的 exec.Command 会让 Windows 为它分一个全新的控制台窗口——每次启动
	// 都闪过一个黑窗，只为了一句版本号。本程序启动的每个子进程都出于同样的理由
	// 经过 hiddenCommand。
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

// parseNodeMajor 从「node --version」打印的东西里读出主版本号（「v24.16.0」）。
func parseNodeMajor(version string) (int, error) {
	fields := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".", 2)
	major, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, fmt.Errorf("看不懂 node 的版本号 %q", version)
	}
	return major, nil
}

// sourceReady 报告游戏的源码是否已解包、看起来是否完整。锁文件也在检查之列，因为
// tools/setup.mjs 会跑 npm ci，而它需要这个文件。
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

// setupNeeded 是 tools/setup.mjs --check 所判断之事的廉价版：一次运行需要的东西
// 都已经在了吗？
//
// 它存在的意义，是让普通启动不必每次都为一次 Node.js 进程、一遍依赖检查和一次
// 250 MB 美术素材的遍历付钱。它故意写得很宽松——缺一样东西，准备这一步就运行，
// 而它会重新把这一切好好检查一遍，修好发现的问题。它唯一看不见的，是美术素材
// 少了单独某一个文件，所以菜单里才有 重新准备游戏文件。
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

// fetchSource 下载仓库的归档，并把它解包进游戏目录。
//
// 这是一次覆盖：已有的文件被替换，没有的文件被加上。更新之所以便宜就是这个缘故——
// node_modules 和已下载的美术素材留在原地——这也正是游戏目录从不被清空的理由。
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

// unpackArchive 把一个 .tar.gz 解包进 root，覆盖已有的、创建没有的，并回答它写了
// 多少个文件。
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

// archivePath 把一个 tar 项的名字变成游戏目录下的路径。
//
// false 表示「跳过」：归档唯一那个顶层文件夹本身，以及任何会落到游戏目录之外的东西。
// tar 里的名字怎么写就怎么带，所以在每个名字都过这一道之前，归档都是敌意的——
// 包括本程序取回来的这一份。
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

// writeFile 写出一个项。文件是被截断，而不是删掉重建，所以覆盖式更新能保住归档
// 没有携带的一切东西的时间戳。
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

// runSetup 是游戏自己的准备步骤：依赖、随仓库带的浏览器库、游戏数据，以及——如果还
// 没有的话——美术素材和音频。scripts/start-windows.bat 在服务器之前跑的就是它，而且
// 再跑一次是安全的：每一步都先检查，素材下载也会从停下的地方继续。
func runSetup(node string) error {
	// --no-local：从已安装的明日方舟客户端做可选提取时会问一个问题，而本程序没有终端
	// 来回答它。提取本地客户端素材 那一项走的是同一条路，只是把这两个开关换掉
	// （见 runSetupStep）。
	if err := runSetupStep(node, "the game is prepared", setupWait, "--quiet", "--no-local"); err != nil {
		return err
	}
	return nil
}

// runSetupStep 带着给定的开关运行游戏自己的准备脚本，并把它的输出交给日志——准备页面
// 那个输出框读的就是日志。
//
// 它被两处用：准备游戏（--quiet --no-local），以及可选的本地客户端提取（--quiet --local）。
// 两处的差别只有开关与时限，所以执行、超时、杀进程树这几件事只有一份。
//
// wait 是"卡住不动"的上限，不是一个预算：它到了就杀整棵进程树，转成错误交回调用方。done 只是
// 日志里的那句话（"the game is prepared" / "本地客户端素材提取完成"）。
func runSetupStep(node, done string, wait time.Duration, flags ...string) error {
	script := filepath.Join(gameDir(), "tools", "setup.mjs")
	cmd := gameCommand(node, append([]string{script}, flags...)...)
	cmd.Stdout, cmd.Stderr = gameWriter{"setup"}, gameWriter{"setup"}
	slog.Info("running the game's setup script", "script", script, "flags", strings.Join(flags, " "))
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
		slog.Info(done)
		return nil
	case <-time.After(wait):
		// 一条卡住不动的下载不该把窗口永远留在那一页上。
		killTree(cmd.Process.Pid)
		return fmt.Errorf("这一步超过 %s 没有结束", wait)
	}
}

// 本程序启动的那个服务器，如果它启动了一个的话。别的都不能被杀，而用户本来就在跑的
// 服务器也轮不到我们来停。
var (
	gameCmd    *exec.Cmd
	gameIsOurs bool

	// 那个进程结束时 gameDone 被关闭，好让等待 /healthz 能在服务器一死掉时就放弃，
	// 而不是把整个超时坐穿。
	gameDone chan struct{}
)

// startGame 启动服务器。进程一起来它就返回；它能不能稳住，是 waitHealthy 回答的事。
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

// waitHealthy 等待 /healthz 回应，并在服务器已经退出时提前放弃——3000 被占时
// 就是这样：服务器打印 EADDRINUSE 然后退出。
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

// drop 删除一个文件或目录，并把「它本来就不在」当成成功——os.RemoveAll 对不存在的
// 路径本来就如此。它存在的意义，是让 updateGame 里那两处删除读起来一样，报真失败
// 的方式也一样。
func drop(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// prepareLockPath 是本程序的两份在往游戏目录里写时轮流占据的那个文件。
func prepareLockPath() string { return filepath.Join(appDataDir(), "prepare.lock") }

// lockFile 在 path 上取独占的字节范围锁，并交回一个函数用来放开它。
//
// 用文件锁，而不是具名 mutex 或信号量。Windows 的 mutex 属于取它的那个**线程**，而
// Go 会在底下把 goroutine 在线程之间搬来搬去——从另一个线程释放它会失败，报
// ERROR_NOT_OWNER。文件锁属于**句柄**，所以一个 goroutine 可以取它，另一个可以放开
// 它；而且进程结束时系统会把它丢掉，所以一次下载中途被杀掉的副本不会留下任何卡住
// 的东西。
//
// 等待以 setupWait 为界：在这里等的副本，等的是它本来自己也要做的一次 250 MB 下载，
// 所以这个天花板和那次下载得到的是同一个。
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

// prepareFiles 把游戏目录弄成可以运行的样子，弄不成时回答该显示哪一页。
//
// 这里面的一切都往 game\ 里写：归档解包到它上面，npm ci 重写它里面的 node_modules，
// fetch-assets 补满 public/。两份同时这么做会互相弄坏——npm ci 是最明显的那个，
// 但解包同样会截断并重写它携带的每一个文件——所以这是整个运行里唯一要轮流的部分。
// 跑两份仍然是重点；它们只是在这里排队，而等过的那一份通常发现活儿已经干完了。
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

// updateGame 把最新的源码取下来盖过现有的，并清掉必须重建的东西，好让接下来的准备
// 步骤真的会跟着做。
//
// 这是 更新游戏 里游戏自己的脚本做不了的那一部分。tools/setup.mjs 检查的是**派生**
// 出来的东西——node_modules、public/vendor、data、assets——它并不知道代码本身有没有
// 变。上游的答案是 `git pull`，fetchSource 顶替的就是它；这里没有 git，而且是故意的，
// 因为一个要求装好 git 的启动器，是一台只有 Node.js 的机器上会失败的启动器。
//
// 有两样东西会被删掉，而每一样都是别人脚本里的一个开关：
//
//   - node_modules。setup.mjs 只在某个包的目录**缺失**时才跑 npm ci，而这个测试看不见
//     版本变化：更新之后，three@0.186 就能满足它，可新代码要的是 0.190。删掉整棵树
//     是唯一能让它跑起来的办法。
//   - 两张上游索引表。assets/cache.mjs 在它们缺失时下载，否则就永远沿用旧的，所以
//     一份过期的 audio_data.json 意味着新版本的干员根本进不了素材清单。
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

// recordRevision 记下磁盘上的源码来自哪个 commit。
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

// prepareGame 把游戏带起来，并在带不起来时报告窗口该说什么。空答案意味着服务器正在
// 应答，窗口可以指向它了。
//
// forceSetup 让准备步骤即使看起来什么都不缺也照跑。菜单项 重新准备游戏文件 要的就是
// 这个——素材下载是 setupNeeded 唯一看不透状态的东西。
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

// gameJob 装住本次运行启动的那个服务器。它唯一的一条规则是
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE：当指向该 job 的最后一个句柄关闭时——本进程以任何
// 方式结束时都会这样，崩溃和从任务管理器里结束也算——Windows 会结束它里面的每一个
// 进程。
//
// 这就是崩溃不会留下一个服务器在后面跑的原因。一个成了孤儿的服务器会继续在端口上
// 应答，下一次运行于是发现端口被占，什么都不启动，然后等一件永远不会发生的事。
var gameJob windows.Handle

// containGame 把一个已启动的服务器放进那个 job。失败会被记进日志然后放过去：优雅
// 路径仍然会停掉服务器，而一个已经在这个进程无权修改的 job 里的进程，会拒绝这次
// 指派。
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

// stopGame 结束服务器，但只有在本程序启动过它时才动手。
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

// killTree 结束一个进程以及它启动的一切。
func killTree(pid int) {
	done := hiddenCommand("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	done.Stdout, done.Stderr = io.Discard, io.Discard
	if err := done.Run(); err != nil {
		slog.Warn("taskkill", "pid", pid, "error", err)
	}
}
