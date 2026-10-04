//go:build windows

package main

// 本程序自己的文件都在哪：%LOCALAPPDATA% 下的数据目录、里面的日志、旁边的窗口状态，以及这个
// 正在跑的 .exe 所在的那个目录。

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
)

// windowState 是本程序跨运行记住的东西：窗口离开时的尺寸、是不是最大化的、是不是留在了最上层，
// 以及它是三组尺寸设置里的哪一组产生的。它就是 window-state.json。
//
// 这里的尺寸是**整扇窗的矩形**，边框算在内——因为窗口建起来时交给 CreateWindowExW 的就是它。
// 位置不在这里是刻意的：一次运行之内，Windows 会替一个隐藏的窗口留着它的矩形；而跨运行时，
// 窗口总是居中打开的。
//
// 游戏自己的地址不在这里：一个只伺候一款游戏的启动器只有一个地址，而它是个常量。这里放的是
// 窗口**离开时停在**的那个地址——在有人打开朋友发来的链接之前它就是这个常量，之后就是那个
// 房间（见下面的 URL）。
type windowState struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized"`

	// URL 是窗口离开时停在的那个地址：游戏自己的，或者由链接加入的某个房间。为空表示没有选过，
	// 这一次运行就打开本机自己的游戏。
	URL string `json:"url"`

	// Size 是三组菜单被设成了哪一组。上面那个窗口矩形是窗口**实际**离开时的尺寸——拖一下就能
	// 离开菜单所描述的那个值——所以两者分开存：矩形说的是「开多大」，这个说的是「菜单建起来时
	// 该在哪一项上打勾」。
	Size sizeOnDisk `json:"size"`

	// OnTop 是窗口离开时是不是压在别人上面。下一次运行会把它装回去，而「置顶」旁边那个勾是从
	// 窗口本身读的，所以这个值是出去的时候从那里读的，而不是另外养一个变量。
	OnTop bool `json:"onTop"`
}

// LogValue 把状态画成一个组，这样日志里那个 state 字段保留着旧时 %+v 显示的那几个字段名，
// 对任何回头读日志的东西也仍旧可读。
func (s windowState) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("width", s.Width),
		slog.Int("height", s.Height),
		slog.Bool("maximized", s.Maximized),
		slog.String("url", s.URL),
		slog.Bool("onTop", s.OnTop),
	)
}

// dataDirOverride 是 -data-dir 放在这里的：本程序把自己的文件放在哪，而不是 %LOCALAPPDATA% 下
// 那个目录。它是给写不进自己配置目录的机器用的——那是「默认目录不只是不方便、而是根本用不了」
// 的唯一一种情况——也是给「在第一份旁边再跑一份自成一体的的副本」用的。
var dataDirOverride string

// appDataDir 是本程序自己的那个目录：它写下的每一样东西都该在它下面（里面那个 EBWebView 里的
// WebView2 配置是唯一一样不属于它的）。要是放着不管，go-webview2 会按 .exe 的名字起一个目录，
// WebView2 会把它的配置放在旁边——所以本程序自己起名。
//
// 这个目录也交给 WebView2，而它对一个建不出 EBWebView 的目录毫不宽容：它会失败，而库不会返回
// 错误，是直接 panic。ensureDataDir 就是为这件事存在的。
func appDataDir() string {
	if dataDirOverride != "" {
		return dataDirOverride
	}
	return filepath.Join(os.Getenv("LOCALAPPDATA"), appID)
}

// ensureDataDir 建出数据目录，而且要在窗口建起来**之前**问一次：建不出来的数据目录就是建不出来
// 的窗口，而明说一句总比在 panic 里发现它好。
func ensureDataDir() error {
	return os.MkdirAll(appDataDir(), 0o755)
}

// gameDir 是游戏本身住的地方：它的源码、它的 node_modules，以及它下载的美术与音频。放在本程序
// 自己的东西同一个目录下面，这样这一个目录就是整份安装——托盘里的「打开游戏目录」也才有东西可指。
func gameDir() string {
	return filepath.Join(appDataDir(), "game")
}

// logFile 是 setupLogging 实际打开的那个文件。它不一定在 appDataDir 下：写不进自己配置目录的
// 机器会把日志放在临时目录里，这样一次出了问题的运行至少还留下点能读的东西。
var logFile string

// logPath 是本程序写自己那些诊断信息的文件。
func logPath() string {
	if logFile != "" {
		return logFile
	}
	return filepath.Join(appDataDir(), "app.log")
}

// programDir 是正在跑的这个 .exe 所在的目录，菜单里作为「程序目录」打开。它属于把它放在那里的
// 人，和 appDataDir 不一样——后者是本程序唯一写东西的地方。
func programDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// logLimit 是日志轮转的界限。一份长期在用的安装会写上几个月，而一次运行之内没有任何东西去轮转
// 它，所以这个检查只在进来的那一次做。
const logLimit = 1 << 20

// openAt 打开一个日志文件；上一份长得太大了，就另起一份新的。
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

// openLog 打开数据目录里的日志，先把目录建出来。
func openLog() (*os.File, error) {
	if err := ensureDataDir(); err != nil {
		return nil, err
	}
	return openAt(filepath.Join(appDataDir(), "app.log"))
}

// setupLogging 把本程序的日志记录送去一个文件。
//
// 没有控制台（-H=windowsgui），所以少了这一步，每一行都会去一个没人看得见的标准错误输出。
// 一次 slog.SetDefault 就罩住了标准库、以及每一个通过它写日志的包。
//
// 一个写不进去的数据目录，不能把日志一起带走。完全没有日志时，一次失败的运行什么都不会留下——
// 没有窗口、没有文件、没有线索——这个后备就是为那种失败存在的。临时目录是第二个选择，而连那里
// 也去不了时，它会写进本程序摆在屏幕上的那条消息里。
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
	fileLog := slog.New(slog.NewTextHandler(file, nil)).With("pid", os.Getpid())
	// 外面再套一层：提示页上那块输出区域从同一个出口分走一份（见 logview.go）。落盘的内容不变。
	stopLog = installLogSink(fileLog.Handler())
}

// stopLog 拆掉输出区域那一路。由 main 在消息循环结束之后调用：窗口已经没了，日志却还会再写几行。
var stopLog = func() {}

// windowStatePath 是记住窗口尺寸与最大化状态的那个文件。
func windowStatePath() string {
	return filepath.Join(appDataDir(), "window-state.json")
}

// loadWindowState 是上一次运行记下的状态。
//
// 文件不在、读不出来、以及里面的尺寸根本描述不了一扇窗——这三件事对调用方是同一个意思：没有
// 东西可恢复，于是窗口按菜单设置要求的那个样子打开。
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

// saveWindowState 记下窗口离开时的尺寸，留给下一次运行。
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
