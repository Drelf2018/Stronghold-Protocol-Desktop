//go:build windows

package main

// Where this app's own files are: the data folder under %LOCALAPPDATA%, the log inside it, the
// window state beside it, and the folder the running .exe sits in.

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
)

// windowState is what the app remembers between runs: the size the window was left at, whether it
// was maximised, whether it was left on top, and which of the three size settings produced it. It
// is window-state.json.
//
// The size is a *window rectangle*, borders included, because that is what CreateWindowExW is given
// when the window is built. There is no position here on purpose: within a run Windows keeps a
// hidden window's rectangle, and across runs the window opens centred.
//
// There is no address here either: a launcher for one game has one address, and it is a constant.
type windowState struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized"`

	// URL is the address the window was left on: the game's own, or a room joined by link. Empty
	// means nothing was chosen, and the run opens this machine's own game.
	URL string `json:"url"`

	// Size is what the three menus were set to. The window rectangle above is the size the
	// window was actually left at - which a drag can move away from what the menus describe -
	// so this is kept apart from it: the rectangle says how big to open, this says which
	// entry to tick when the menus are built.
	Size sizeOnDisk `json:"size"`

	// OnTop is whether the window was left above the others. It is put back on the next run,
	// and the tick beside 置顶 comes from the window itself, so this is read from there on the
	// way out rather than kept in a variable of its own.
	OnTop bool `json:"onTop"`
}

// LogValue renders the state as a group, so a "state" field in the log keeps the field names
// the old %+v showed, and stays readable to anything that reads the log back.
func (s windowState) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("width", s.Width),
		slog.Int("height", s.Height),
		slog.Bool("maximized", s.Maximized),
		slog.String("url", s.URL),
		slog.Bool("onTop", s.OnTop),
	)
}

// dataDirOverride is what -data-dir put here: where this app keeps its own files instead of the
// folder under %LOCALAPPDATA%. It is for the machine whose profile cannot be written to - the one
// case where the default folder is not merely inconvenient but unusable - and for running a
// second, self-contained copy beside the first.
var dataDirOverride string

// appDataDir is the folder this app owns: everything it writes belongs under it (the WebView2
// profile in EBWebView inside it is the one thing it does not own). Left to themselves
// go-webview2 would name a folder after the .exe and WebView2 would put its profile beside it, so
// the app names its own.
//
// WebView2 is handed this directory too, and it is not tolerant of one it cannot create its
// EBWebView in: it fails, and the library panics rather than returning. That is what ensureDataDir
// is for.
func appDataDir() string {
	if dataDirOverride != "" {
		return dataDirOverride
	}
	return filepath.Join(os.Getenv("LOCALAPPDATA"), appID)
}

// ensureDataDir creates the data directory, and is asked *before* the window is built: a data
// directory that cannot be created is a window that cannot be created, and saying so beats
// discovering it in a panic.
func ensureDataDir() error {
	return os.MkdirAll(appDataDir(), 0o755)
}

// gameDir is where the game itself lives: its source, its node_modules, and the art and audio it
// downloads. Under the same folder as everything else this app owns, so that one folder is the
// whole installation - and "打开游戏目录" in the tray has something to point at.
func gameDir() string {
	return filepath.Join(appDataDir(), "game")
}

// logFile is the file setupLogging actually opened. It is not always under appDataDir: a machine
// whose profile cannot be written gets the log in the temp folder instead, so that a run that goes
// wrong still leaves something to read.
var logFile string

// logPath is the file the app writes its own diagnostics to.
func logPath() string {
	if logFile != "" {
		return logFile
	}
	return filepath.Join(appDataDir(), "app.log")
}

// programDir is the folder the running .exe sits in, which the menu opens as 程序目录. It
// belongs to whoever put the folder there, unlike appDataDir - the only place this program
// writes.
func programDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// logLimit is where a log is rotated aside. A long-lived installation writes for months, and
// nothing inside a run rotates it, so the check happens once, on the way in.
const logLimit = 1 << 20

// openAt opens a log file, starting a fresh one once the last has grown large.
//
// 多开之后这一步会失败，而且失败是正常的：Windows 下 Go 打开文件只给 FILE_SHARE_READ 与
// FILE_SHARE_WRITE，没有 FILE_SHARE_DELETE，所以只要还有另一份实例开着这个文件，改名就会被拒。
// 那就接着写同一个文件——两份实例的内容不会互相写坏（每次写入都是一次原子的追加），而且等它们
// 都退出之后，下一次启动自然会把该轮转的轮转掉。这里曾经把改名失败当成错误返回，于是那一份实例
// 会悄悄改用 %TEMP% 里的日志：这才是多开真正会出的问题。
func openAt(path string) (*os.File, error) {
	if info, err := os.Stat(path); err == nil && info.Size() > logLimit {
		old := path + ".old"
		// 文件还在的时候，Windows 不允许改名覆盖它。
		_ = os.Remove(old)
		if err := os.Rename(path, old); err != nil {
			slog.Warn("log: another copy has it open, so it is not rotated", "path", path, "error", err)
		}
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}

// openLog opens the log inside the data directory, creating the directory first.
func openLog() (*os.File, error) {
	if err := ensureDataDir(); err != nil {
		return nil, err
	}
	return openAt(filepath.Join(appDataDir(), "app.log"))
}

// setupLogging sends the app's log records to a file.
//
// There is no console (-H=windowsgui), so without this every line would go to a stderr nobody can
// see. One slog.SetDefault covers the standard library and every package that logs through it.
//
// A data directory that cannot be written must not take the log down with it. With no log at all a
// failed run leaves nothing whatsoever behind - no window, no file, no clue - which is the failure
// this fallback exists for. The temp folder is the second choice, and it is named in the message
// the program puts on screen when it cannot get that far either.
func setupLogging() {
	file, err := openLog()
	if err != nil {
		slog.Warn("cannot write the log in the data directory", "path", filepath.Join(appDataDir(), "app.log"), "error", err)
		fallback := filepath.Join(os.TempDir(), appID+".log")
		file, err = openAt(fallback)
		if err != nil {
			slog.Warn("cannot write the log in the temp directory either", "path", fallback, "error", err)
			return
		}
	}
	logFile = file.Name()
	// pid 带上：多开时几份实例写进同一个 app.log（见 openAt），日志里得看得出哪一行是哪一份。
	slog.SetDefault(slog.New(slog.NewTextHandler(file, nil)).With("pid", os.Getpid()))
}

// windowStatePath is the file the window's size and maximised state are remembered in.
func windowStatePath() string {
	return filepath.Join(appDataDir(), "window-state.json")
}

// loadWindowState is the state the last run recorded.
//
// A missing file, an unreadable one, and one whose size cannot describe a window all
// mean the same thing to the caller: there is nothing to restore, so the window opens
// the way the menu settings ask for.
func loadWindowState() (windowState, bool) {
	data, err := os.ReadFile(windowStatePath())
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("window state: cannot read it", "path", windowStatePath(), "error", err)
		}
		return windowState{}, false
	}
	var s windowState
	if err := json.Unmarshal(data, &s); err != nil {
		slog.Warn("window state: cannot parse it", "path", windowStatePath(), "error", err)
		return windowState{}, false
	}
	if s.Width <= 0 || s.Height <= 0 {
		slog.Warn("window state: not a window size, ignoring it", "path", windowStatePath(), "state", s)
		return windowState{}, false
	}
	return s, true
}

// saveWindowState records the size the window was left at, for the next run.
func saveWindowState(s windowState) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		slog.Error("window state: cannot encode it", "error", err)
		return
	}
	if err := os.MkdirAll(appDataDir(), 0o755); err != nil {
		slog.Error("window state: cannot create the data directory", "path", appDataDir(), "error", err)
		return
	}
	if err := os.WriteFile(windowStatePath(), append(data, '\n'), 0o644); err != nil {
		slog.Error("window state: cannot write it", "path", windowStatePath(), "error", err)
		return
	}
	slog.Info("window state saved", "path", windowStatePath(), "state", s)
}
