//go:build windows

// The launcher puts a tray icon and a WebView2 window together around one game: 卫戍协议：盟// (Stronghold Protocol), a fan-made 1-4 player co-op browser game whose server is a Node.js
// process. The window is the game - it opens the server's own page, on this machine.
//
// The menu is split by what it acts on: the window carries its own (置顶, 居中, 刷新, 恢复参数
// 尺寸 and the three size settings) in the system menu on the title bar, the tray carries the
// program's own (显示窗口, 开机自启动, and the 打开 and 更多 submenus, 退.
//
// The window is up as soon as it is created, and closing it hides it rather than quitting: the
// tray stays, and 显示窗口 brings it back, until 退出 is picked. Its size is remembered between
// runs - the position is not, since a run starts centred.
//
// Built with -ldflags=-H=windowsgui: the console subsystem would put a console window beside it,
// whose taskbar button wears the console icon. README.md has the two build steps.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/Drelf2018/Stronghold-Protocol-Desktop/internal/artwork"
	"github.com/Drelf2018/systray"
	"golang.org/x/sys/windows"
)

// The app names itself twice, and the two are not interchangeable.
//
// appName is what a person reads: the window title, the tray tooltip, the caption of an error box.
//
// appID is what a machine reads, and what the app is filed under: the folder under %LOCALAPPDATA%
// (which also holds the game's own source), and the name of the startup entry. It is ASCII,
// lowercase, and must never change: the day it does, every user's saved window size, browser
// profile and downloaded game are left behind under the old name. Do not
// derive it from appName.
const (
	appName = "卫戍协议：盟约 启动器"
	appID   = "stronghold-protocol-launcher"
)

// Version is the build's version, and the one thing in this program the linker rewrites: the
// release workflow builds with
//
//	-ldflags "-H=windowsgui -X main.Version=${{ github.ref_name }}"
//
// so a release carries the tag it was published from, and a local build stays at "dev".
//
// It has to stay a plain string variable: -X only reaches one whose initializer is a constant
// expression, and does nothing at all - silently - to a variable computed at run time.
//
// Nothing in the menu shows it, but a local build does get one extra submenu, so the value earns
// its place twice. It goes to the log at startup, which is where the question it answers ("which
// build is this") is asked anyway.
var Version = "dev"

// win is the main window, set once in main and never nil after that: newWindow
// returns nil when the WebView2 runtime is missing or the window could not be
// created, and main stops there rather than running a tray with nothing behind it.
var win *webviewWindow

// report puts a message in front of the person. Before there is a window this is the only way the
// program can say anything at all: with -H=windowsgui there is no console to print to.
func report(cause string) {
	if err := showError(cause); err != nil {
		slog.Error("show", "error", err)
	}
}

// parseArgs reads the switches this program answers to.
//
// -data-dir is here, and read before anything else, because it decides where the first file this
// program writes goes. -no-game is described where it is used.
func parseArgs() {
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-no-game" || arg == "--no-game":
			noGame = true
		case arg == "-data-dir" || arg == "--data-dir":
			if i+1 < len(args) {
				i++
				dataDirOverride = args[i]
			}
		case strings.HasPrefix(arg, "-data-dir="):
			dataDirOverride = strings.TrimPrefix(arg, "-data-dir=")
		case strings.HasPrefix(arg, "--data-dir="):
			dataDirOverride = strings.TrimPrefix(arg, "--data-dir=")
		}
	}
	// 相对路径按启动时的工作目录算，算完就固定下来：WebView2 拿到的是这个字符串，而它不认
	// 「相对于谁」这种问题
	if dataDirOverride != "" {
		if abs, err := filepath.Abs(dataDirOverride); err == nil {
			dataDirOverride = abs
		}
	}
}

// createWindow builds the window, turning the one failure that is not an error into one.
//
// When WebView2 cannot create its environment - no runtime installed, or a user data folder it
// cannot make - its completion handler is called with a nil controller and go-webview2
// dereferences it. That panic happens on this goroutine on the way back through NewWithOptions, so
// it is caught here rather than reaching the runtime and taking the process with it: without this
// the program disappears without a word, and a word is all anyone has to go on.
func createWindow(state windowState) (w *webviewWindow) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("creating the window panicked", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			w = nil
		}
	}()
	return newWindow(state)
}

func showError(cause string) error {
	title, err := windows.UTF16PtrFromString(appName)
	if err != nil {
		return err
	}
	body, err := windows.UTF16PtrFromString(cause)
	if err != nil {
		return err
	}
	_, err = windows.MessageBox(0, body, title, windows.MB_OK|windows.MB_ICONERROR|windows.MB_TASKMODAL|windows.MB_SETFOREGROUND)
	return err
}

// The address the window is showing. The tray menu's goroutines read it, the page's binding writes
// it, and both end up on the thread that runs the message loop.
var (
	urlMu      sync.RWMutex
	currentURL = gameAddress
)

// url is the address the window is showing, or is about to.
func url() string {
	urlMu.RLock()
	defer urlMu.RUnlock()
	return currentURL
}

// address turns what was typed into an address the window can open.
//
// What gets typed is usually a host, maybe with a port, and nothing in front of it - and WebView2
// reads that as a search or a file path rather than as a site. Anything already carrying a scheme is
// left alone; everything else gets http:// in front.
func address(typed string) string {
	target := strings.TrimSpace(typed)
	if target == "" {
		return ""
	}
	if strings.Contains(target, "://") {
		return target
	}
	lower := strings.ToLower(target)
	for _, prefix := range []string{"about:", "data:"} {
		if strings.HasPrefix(lower, prefix) {
			return target
		}
	}
	return "http://" + target
}

// roomCode is the room key in what was typed, or "" when it is not one.
//
// Four letters or digits on their own are taken for a key rather than a host: "ABCD" is not a
// machine anybody has, and it is exactly what a friend reads out loud. The game's own lobby makes the
// same call about what gets pasted into it (public/js/screens/lobby.js).
func roomCode(typed string) string {
	key := strings.ToUpper(strings.TrimSpace(typed))
	if len(key) != roomCodeLen {
		return ""
	}
	for _, r := range key {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return key
}

// joinURL turns what was typed into an address the window can open. Three shapes, because all three
// are things people actually paste: a whole invite link - which may point at someone else's machine,
// so it is used as it stands - a bare host or host:port, and the four-character key on its own, which
// is completed against this machine's own game.
func joinURL(typed string) string {
	if key := roomCode(typed); key != "" {
		return gameAddress + "?room=" + key
	}
	return address(typed)
}

// showGame points the window at the game: the address this run is on, which is the saved one when
// there is one - a room joined by link, or a friend's server - and this machine's own otherwise.
func showGame() {
	if win == nil {
		return
	}
	slog.Info("opening the game", "url", url())
	win.Dispatch(win.Navigate, url())
}

// showNotice puts a page this app built into the window, and remembers nothing.
func showNotice(page string) {
	if win == nil || page == "" {
		return
	}
	slog.Info("showing a page of our own", "page", page[:min(len(page), 48)])
	win.Dispatch(win.Navigate, page)
}

// openStartup points the window at the address this run opens: the one the last run was left on
// (the game's own, a room joined by link, or a friend's server), or this machine's own game when
// nothing was saved.
func openStartup(saved string) {
	if target := address(saved); target != "" {
		urlMu.Lock()
		currentURL = target
		urlMu.Unlock()
	}
	slog.Info("opening the saved address", "url", url())
	win.Navigate(url())
}

// setURL points the window at a new address and remembers it. This is what the join prompt comes back
// through, so it arrives on the thread that runs the message loop - which is where Navigate belongs.
func setURL(raw string) {
	target := joinURL(raw)
	if target == "" || target == url() {
		return
	}
	urlMu.Lock()
	currentURL = target
	urlMu.Unlock()
	slog.Info("opening a new address", "url", target)
	win.Navigate(target)
	remember()
}

// reprepare takes the game down and brings it back up through the game's own preparation step.
//
// update is the whole difference between the two menu items: 更新游戏 fetches the newest source
// first (updateGame), 重新准备游戏文件 works with the tree that is there. Everything around the
// preparation is shared on purpose - stopping the server, the page that says what is going on,
// putting the game back or saying why not - because those must not drift apart between them.
//
// It runs on a goroutine of its own: the preparation lasts as long as a 250 MB download lasts, and
// the goroutine that ranges over a menu item's channel is not the one to sit through that.
func reprepare(update bool) {
	go func() {
		stopGame()
		showNotice(preparingPage())
		if update {
			if err := updateGame(); err != nil {
				slog.Error("updating the game", "error", err)
				showNotice(updateFailedPage())
				return
			}
		}
		page := prepareGame(true)
		if page != "" {
			showNotice(page)
		} else {
			showGame()
		}
	}()
}

// remember writes down the size the window is at. A window whose rectangle cannot be read leaves
// the file alone, rather than writing a size of zero over a good one.
func remember() {
	s := windowState{URL: url(), Size: currentSize().onDisk(), OnTop: win.TopMost()}
	rect, ok := win.WindowRect()
	if !ok {
		slog.Warn("window state not saved: no window rectangle to read")
		return
	}
	s.Width, s.Height, s.Maximized = rect.Width, rect.Height, rect.Maximized
	saveWindowState(s)
}

func main() {
	// 参数先看 -data-dir 决定下面这些写到哪儿，setupLogging 是第一个要写的
	parseArgs()

	// 放在最前面，这样下面的一切——包括库自己报的错——都会留下痕迹，之后有人能读到
	setupLogging()

	// 版本只进日志：它是「这一份是哪一版」唯一的答案，而日志正是排障时第一个被打开的地方
	slog.Info("starting", "version", Version, "data", appDataDir(), "log", logPath(), "integrity", processIntegrity())

	// WebView2 要在数据目录里放它的 EBWebView，而它建不出来的时候，go-webview2 会在回调里空指针
	// panic——一行日志都没有，程序已经没了。所以先自己试一次：这个错误有窗口可以说话
	if err := ensureDataDir(); err != nil {
		slog.Error("cannot create the data directory", "path", appDataDir(), "error", err)
		report("无法创建数据目录：\n" + appDataDir() + "\n\n" + err.Error() +
			integrityNote(processIntegrity()) +
			"\n\n换个位置再试：\nStronghold Protocol Launcher.exe -data-dir <目录>")
		os.Exit(3)
	}

	// 只在 Windows 上，而且必须在任何窗口出现之前：不调的话，缩放过的显示器报出来的图标
	// 度量还是 16 像素，系统只好把结果拉伸
	if err := systray.EnableDPIAwareness(); err != nil {
		slog.Warn("dpi awareness", "error", err)
	}

	// 托盘图标来自内置图形：凡是有可能被要到的尺寸都现画一张，打包成一个 .ico（internal/artwork）
	// 托盘只要一张，就是通知区域此刻要的那个尺寸——所以这里不必预置一整张尺寸表，125% 缩放的显示器也
	// 不会退而让系统去缩。需要那张表的是 exe 的资源图标，它只能构建时定
	icon, err := artwork.IconICO(smallIconSize())
	if err != nil {
		slog.Error("icon", "error", err)
		report("无法加载内置图标。")
		os.Exit(1)
	}

	// 默认尺寸：锚在屏幕宽度上，占它的 3/4，宽高比 16:9。菜单在启动时显示的就是这一套，
	// 所以先把它交给菜单——菜单才会勾在正确的位置上，之后点任何一项也才有数可算。窗口尺
	// 优先用记住的那个	//
	// 菜单算出来的*可见**尺寸，创建窗口要的是 window rect：有记录时不用换算（文件里存
	// 本来就是 window rect），第一次运行则先量一次边框再换算，这样第一帧就是菜单承诺的尺寸
	screenW, screenH := displaySize()
	preset := sizeState{anchor: screenWidth, share: share{3, 4}, ratio: ratio{16, 9}}

	// 文件只读一次：它同时带着窗口矩形和三组参数。参数在这里就要装回菜单，菜单构建时
	// 才能勾对位置
	saved, haveState := loadWindowState()
	if haveState {
		if s, ok := saved.Size.state(); ok {
			preset = s
		}
	}
	storeSize(preset)

	var state windowState
	if haveState {
		state = saved
	} else if edges, ok := measureFrameEdges(); ok {
		width, height := preset.windowSize(screenW, screenH)
		state.Width = width + int(edges.left+edges.right)
		state.Height = height + int(edges.top+edges.bottom)
		slog.Info("first run: frame measured",
			"edgeLeft", edges.left, "edgeTop", edges.top, "edgeRight", edges.right, "edgeBottom", edges.bottom,
			"windowWidth", state.Width, "windowHeight", state.Height,
			"visibleWidth", width, "visibleHeight", height)
	}

	win = createWindow(state)
	if win == nil {
		slog.Error("webview2: the runtime is missing, or the window could not be created")
		report("无法创建窗口。\n\n" +
			"多半是缺少 WebView2 运行时（Windows 11 自带，Windows 10 多数随 Edge 装过）；\n" +
			"也可能是数据目录不可写。" +
			integrityNote(processIntegrity()) +
			"\n\n详情见日志：\n" + logPath())
		os.Exit(2)
	}

	// 窗口自己的图标：任务栏和 Alt+Tab 画的是它，跟 exe 里那份资源是两回事
	if err := win.setWindowIcons(); err != nil {
		slog.Warn("window icons", "error", err)
	}

	// 窗口自己的四项（置顶/居中/刷新/恢复参数尺寸）接在系统菜单上：右键标题栏
	// Alt+空格，见 sysmenu.go	// 上次是不是置顶，装回去：先落到窗口上，再装菜单——菜单上那个勾是从窗口读的
	if state.OnTop {
		win.setTopMost(true)
	}
	installSystemMenu(win)

	// 页面里那套声音是页面自己建的：public/js/audio.js 拿着它的 AudioContext，Go 这边够不着
	// 所以往每个文档里装一段脚本，由它收放（见 setMuted）。必须赶在 Run 之前——Init 注册的是
	// "之后创建的每一个文
	// Init 只收一段脚本，两段拼起来：各自都是独立的作用域，拼在一起不会互相看见
	win.w.Init(muteJS + "\n" + fullscreenJS)

	// 页面把地址交回来靠这个绑定：Eval 不回传值，所以 askForRoom 的答案要从这里回来。绑定必须赶
	// Run 之前——它注册的是「之后创建的每一个文档」，已经打开的这个文档不算
	if err := win.Bind("_join", func(raw string) { setURL(raw) }); err != nil {
		slog.Warn("binding _join", "error", err)
	}
	// 页面里的全屏按钮走标准 Fullscreen API，而那只让页面填满控件：要让窗口自己变
	if err := win.Bind("_fullscreen", func(on bool) { win.fullScreen(on) }); err != nil {
		slog.Warn("binding _fullscreen", "error", err)
	}

	// 先认下上一次的地址：可能是朋友发来的加入链接，那就直接开到那一局；没有就用本机自己的
	openStartup(saved.URL)

	// 游戏：端口上已经有游戏在回应就直接开它，否则由本程序把它拉起来。准备工作——第一次要下载
	// 源码、装依赖、拉 250 MB 素材——全在窗口背后做，这期间窗口显示的是本程序自己的一页
	//
	// 联机的模型是「所有人连同一个后端」：同一个端口上的第二条连接才是第二个玩家，所以这里不另起
	// 一个服务端去避开占用——那是另一局，不是队友
	if noGame {
		// 什么都不启动：窗口对着上面那个地址，服务由别处提供
		showGame()
	} else {
		showNotice(preparingPage())
		go func() {
			if page := prepareGame(false); page != "" {
				showNotice(page)
			} else {
				showGame()
			}
		}()
	}

	onReady := func() {
		systray.SetIcon(icon)
		systray.SetTooltip(appName)
		systray.SetOnLeftClick(win.Show)
		addMenuItems()
	}

	// WebView2 自己跑消息循环，而托盘的隐藏窗口挂在同一个线程上，它的消息就
	// 那个循环分发出去 ——这正是 Register 存在的理由。用 Run 会和 WebView2 抢循环
	systray.Register(onReady, nil)
	win.Run()
	slog.Info("the message loop ended")

	// 退出前把窗口尺寸记下来。窗口最大化着退出也记得对：WindowRect 取的是「还原后的那个矩
	// 形」，并把最大化这件事一起记下
	remember()
	// 游戏服务跟着窗口一起走：把它留下，就等于让一个屏幕上已经没有前端对着的服务器继续跑。本
	// 程序启动之前就已经在跑的服务不是我们的，stopGame 不会碰它
	stopGame()
	// 下面不能再有任何东西绕过这里结束进程：本程序里每一个 os.Exit 都发生在 win 存在之前，也
	// 因为这样，Destroy 可以就是一个放在最后的普通调用，而不必 defer
	win.Destroy()
}

// addMenuItems builds the menu. systray appends in call order, so these calls run
// top to bottom exactly as the menu is drawn:
//
//	显示窗口
//	开机自启动
//	────────────────
//	打开 ▸
//	  打开日志文件
//	  打开游戏目录
//	  打开程序目录
//	更多 ▸
//	  更新游戏
//	  输入加入链接
//	  在浏览器中打开
//	  重新准备游戏文件
//	────────────────
//	测试 （本地构建才有）
//	退出
func addMenuItems() {
	mShow := systray.AddMenuItem("显示窗口", "显示主窗口")
	go func() {
		for range mShow.ClickedCh {
			win.Show()
		}
	}()

	// 开机自启动紧随显示窗口。它是这张菜单里唯一的状态（勾选框），摆在最上面，勾没勾一
	// 就看得见，不必为了确认这一点把菜单读到底	//
	// 勾的是注册表里的真实状态，不是记在内存里的意图：setAutostart 写完之后会再读一次，
	// 写不进去（策略、权限）时勾就上不去，用户一眼能看出来没生效
	mStartup := systray.AddMenuItemCheckbox("开机自启动", "登录时自动运行本程序", autostartEnabled())
	go func() {
		for range mStartup.ClickedCh {
			if setAutostart(!autostartEnabled()) {
				mStartup.Check()
			} else {
				mStartup.Uncheck()
			}
		}
	}()

	// 窗口那几项（置顶 / 居中 / 刷新 / 恢复参数尺寸）和三组尺寸参数搬到了窗口自己的
	// 菜单里（右键标题栏、Alt+空格，见 sysmenu.go）：它们调的就是这个窗口，跟托盘没关系
	// 窗口的事跟着窗口走，托盘只留这个程序自己的事
	systray.AddSeparator()

	mOpen := systray.AddMenuItem("打开", "打开文件或目录")

	// 这三个都是「打开…」，是同一件事，所以收进「打开」这一个子菜单里，不占托盘的一行
	mLog := mOpen.AddSubMenuItem("打开日志文件", "用默认程序打开运行日志")
	go func() {
		for range mLog.ClickedCh {
			shellOpen(logPath())
		}
	}()

	// 「打开游戏目录」开的是本程序自己那份（%LOCALAPPDATA% 下的 game\），不是谁另外 clone 的一份：
	// 源码、依赖、素材都由本程序管
	mFolder := mOpen.AddSubMenuItem("打开游戏目录", "打开游戏源码与素材所在的那个目录")
	go func() {
		for range mFolder.ClickedCh {
			shellOpen(gameDir())
		}
	}()

	// 程序目录和上面那些不是一回事：上面都在 %LOCALAPPDATA% 下，是这个程序管的地方；
	// 程序目录是这个 .exe 被解开放着的地方，属于把它放在那里的人（见 appdata.go）
	// 「程序」不是随手挑的词：原来叫「运行目录」，而「运行」在这个菜单里什么都不特指
	mProgramDir := mOpen.AddSubMenuItem("打开程序目录", "打开本程序 exe 所在的那个目录")
	go func() {
		for range mProgramDir.ClickedCh {
			dir, err := programDir()
			if err != nil {
				slog.Warn("program dir", "error", err)
				continue
			}
			shellOpen(dir)
		}
	}()

	// 「更多」装的是动这个游戏的三件事：取新版、重新准备、以及拿到浏览器里再开一份
	mMore := systray.AddMenuItem("更多", "更多操作")

	// 「更新」和「重新准备」共用一条路（reprepare），只差开头那一步：更新先把最新源码取回来
	// 并把必须重建的东西清掉，重新准备则直接用现有这棵树
	mUpdate := mMore.AddSubMenuItem("更新游戏", "从 GitHub 取回最新源码，重装依赖、补齐素材，然后重启游戏服务")
	go func() {
		for range mUpdate.ClickedCh {
			reprepare(true)
		}
	}()

	// 加入这一项回到网页原来那套「改地址」的做法：问题在页面里问（Go 这边没有控制台，消息框也
	// 不回一行字），答案_join 绑定回来（见 setURL js/askForRoom.js）。输入的可以是完整的加入
	// 链接、主机名，或者光秃秃的 4 位密钥——怎么补全由 joinURL 决定
	mJoin := mMore.AddSubMenuItem("输入加入链接", "粘贴朋友发来的加入链接，或直接输入 4 位同盟密钥")
	go func() {
		for range mJoin.ClickedCh {
			// 先把窗口叫出来，再把脚本交给窗口的线程：Eval 最终落在 WebView2 controller 的调用上，
			// 那不是菜单 goroutine 该碰的东西
			win.Show()
			win.Dispatch(win.w.Eval, askForRoomJS+"askForRoom("+fmt.Sprintf("%q", url())+");")
		}
	}()

	mBrowser := mMore.AddSubMenuItem("在浏览器中打开", "用系统默认浏览器打开游戏地址")
	go func() {
		for range mBrowser.ClickedCh {
			shellOpen(gameAddress)
		}
	}()

	// 「重新准备」是唯一能把中断的素材下载接下去的入口：那一半的状态只有游戏自己知道，
	// setupNeeded（launcher.go）看不见缺的是哪一个文件
	mPrepare := mMore.AddSubMenuItem("重新准备游戏文件", "重新安装依赖并下载美术素材，然后重启游戏服务")
	go func() {
		for range mPrepare.ClickedCh {
			reprepare(false)
		}
	}()

	systray.AddSeparator()

	// 本地构建里多一个「测试」子菜单：几张提示页要等真实的故障才看得到，这里能直接调出来看
	// 判断的是 Version —— 发布时的 -X main.Version=<tag> 一定把它填上，所以只有自己编的那一份还是 "dev"
	if Version == "dev" {
		mTest := systray.AddMenuItem("测试", "调出几张本地提示页")
		for _, c := range []struct {
			title   string
			tooltip string
			page    func() string
		}{
			{"正在准备", "首次启动、下载与安装时显示的那一张", preparingPage},
			{"需要 Node.js", "找不到合格的 node 时显示的那一张", nodeMissingPage},
			{"无法获取源码", "下载游戏源码失败时显示的那一张", sourceFailedPage},
			{"更新失败", "「更新游戏」没能完成时显示的那一张", updateFailedPage},
			{"准备失败", "安装依赖或下载素材出错时显示的那一张", setupFailedPage},
			{"启动失败", "服务始终没有回应时显示的那一张", startFailedPage},
			{"端口被占用", "3000 上已经有别的程序时显示的那一张", portBusyPage},
		} {
			item := mTest.AddSubMenuItem(c.title, c.tooltip)
			page := c.page
			go func() {
				for range item.ClickedCh {
					win.Show()
					showNotice(page())
				}
			}()
		}
	}

	mQuit := systray.AddMenuItem("退出", "退出程序")
	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()
}
