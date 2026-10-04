//go:build windows

package main

// WebView2 窗口，以及它身下的 Win32：窗口怎么建起来、DWM 在它周围画的边框、
// 那圈边框让两个矩形容易被混淆，还有那个把点 X 变成隐藏的消息过程。
//
// 本程序自己的尺寸菜单要的一切，落到这里都是可见框；Win32 要的一切都是 window rect。
// frameEdges 就是两者之差，是在这个窗口上量出来的、不是猜的；moveVisible 是唯一应用
// 它的地方。

import (
	"fmt"
	"log/slog"
	"strconv"
	"unsafe"

	"github.com/Drelf2018/Stronghold-Protocol-Launcher/internal/artwork"
	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("User32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	kernel32 = windows.NewLazySystemDLL("Kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procInsertMenuW              = user32.NewProc("InsertMenuW")
	procCallWindowProcW          = user32.NewProc("CallWindowProcW")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	procCheckMenuItem            = user32.NewProc("CheckMenuItem")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procSendMessageW             = user32.NewProc("SendMessageW")
	procDwmGetWindowAttribute    = dwmapi.NewProc("DwmGetWindowAttribute")
	procGetClientRect            = user32.NewProc("GetClientRect")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procGetMenuItemCount         = user32.NewProc("GetMenuItemCount")
	procGetMenuItemID            = user32.NewProc("GetMenuItemID")
	procGetMenuStringW           = user32.NewProc("GetMenuStringW")
	procGetSystemMenu            = user32.NewProc("GetSystemMenu")
	procGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	procGetWindowPlacement       = user32.NewProc("GetWindowPlacement")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procSetClassLongPtrW         = user32.NewProc("SetClassLongPtrW")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procSetWindowLongPtrW        = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procShellExecuteW            = shell32.NewProc("ShellExecuteW")
	procShowWindow               = user32.NewProc("ShowWindow")
	procSystemParametersInfoW    = user32.NewProc("SystemParametersInfoW")
)

const (
	swHide    = 0
	swRestore = 9

	// SW_SHOWMAXIMIZED：用来重新打开用户离开时处于最大化状态的窗口。
	swShowMaximized = 3

	// SW_SHOWNORMAL：让 shell 打开某个东西时向它要的显示方式。
	swShownormal = 1

	wmClose = 0x0010

	// GWLP_WNDPROC，作为 LONG_PTR 是 -4。
	gwlpWndProc = ^uintptr(3)

	// GWL_EXSTYLE，作为 LONG_PTR 是 -20，WS_EX_TOPMOST 就在这里面。
	gwlExStyle  = ^uintptr(19)
	wsExTopmost = 0x0008

	// WM_SETICON，以及窗口存图标用的两个槽位：ICON_SMALL 给标题栏，
	// ICON_BIG 给任务栏和 Alt+Tab。
	wmSetIcon     = 0x0080
	iconSlotSmall = 0
	iconSlotBig   = 1

	// GCLP_HICON 与 GCLP_HICONSM：同样两张图挂在窗口类上，给那种向类而不是向窗口
	// 要图标的 shell 用。
	gclpHIcon      = ^uintptr(13)
	gclpHIconSmall = ^uintptr(33)

	// WM_SYSCOMMAND：系统菜单回送的消息。选中我们追加进去的菜单项，也走这里。
	wmSysCommand = 0x0112

	// AppendMenuW 与 CheckMenuItem 用的标志位。
	mfString     = 0x0000
	mfByPosition = 0x0400
	mfSeparator  = 0x0800
	mfPopup      = 0x0010
	mfChecked    = 0x0008
	mfUnchecked  = 0x0000

	// CreateIconFromResourceEx 读取的图标资源格式的版本。
	iconResourceVersion = 0x00030000

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	// HWND_TOPMOST 与 HWND_NOTOPMOST：SetWindowPos 能把窗口放到的两个位置。
	hwndTopmost    = ^uintptr(0)
	hwndNotTopmost = ^uintptr(1)

	dwmwaExtendedFrameBounds = 9
	spiGetWorkArea           = 0x0030

	// SM_CXSCREEN、SM_CYSCREEN：主显示器的尺寸。
	smCXScreen = 0
	smCYScreen = 1

	// SM_CXICON 与 SM_CXSMICON：向窗口索取的图标宽度，按显示器缩放。100% 时是 32 和 16，
	// 150% 时是 48 和 24。
	smCXIcon      = 11
	smCXSmallIcon = 49

	// WS_OVERLAPPEDWINDOW，go-webview2 给真窗口的那个样式：DWM 画的边框取决于它，
	// 所以 measureFrameEdges 里那个用完就扔的窗口也戴着它。
	wsOverlappedWindow = 0x00CF0000

	// shadowClassName 是那个用完就扔的窗口的类名。没有别的东西用它，所以不会和
	// go-webview2 为真窗口注册的那个类撞上。
	shadowClassName = "systrayExampleShadow"
)

type rect struct{ left, top, right, bottom int32 }

func (r rect) width() int32  { return r.right - r.left }
func (r rect) height() int32 { return r.bottom - r.top }

type point struct{ x, y int32 }

// windowPlacement 就是 WINDOWPLACEMENT，它的 rcNormalPosition 是窗口被还原时回到的
// 那个矩形——要记住的正是它，哪怕窗口正处在最大化或最小化状态。
type windowPlacement struct {
	length           uint32
	flags            uint32
	showCmd          uint32
	ptMinPosition    point
	ptMaxPosition    point
	rcNormalPosition rect
}

// wndClassEx 就是 WNDCLASSEXW，给 measureFrameEdges 里那个用完就扔的窗口用。
type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

// frameEdges 是 DWM 画在可见框外面的边框，按边记：window rect 就是可见框往外长这么多。
// 厚度随主题和 Windows 版本变化，所以是量出来的而不是猜的，而且每一边都不一样。
type frameEdges struct{ left, top, right, bottom int32 }

// webviewWindow 是本程序的主窗口，由 WebView2 承载。
//
// 一个窗口有三个矩形在描述它，把它们搞混就是「我设了 1000，量出来却是 984」的
// 来源：
//
//	window rect   CreateWindowExW / SetWindowPos   标题栏、边框，以及 DWM 在它周围
//	                                              画的那圈看不见的缩放边框
//	visible frame DwmGetWindowAttribute           眼睛和截图得到的
//	client area   GetClientRect                   页面渲染进去的
//
// 窗口按 window rect 建起来、也按 window rect 记住，所以它打开时的尺寸就是上次离开时的
// 尺寸，分毫不差。菜单是例外：它们是按人的方式描述屏幕占比的，所以 SetVisibleSize 要
// 过一遍边框——用的是创建时在这个窗口上量到的边框，从不猜。
type webviewWindow struct {
	w        webview2.WebView
	hwnd     uintptr
	previous uintptr // 被这一个替换掉的窗口过程
	edges    frameEdges
}

// newWindow 按 state 描述的尺寸创建 WebView2 窗口，居中放在屏幕上，并让它可见：它就是界面，
// 关掉之后「显示窗口」能把它叫回来。WebView2 运行时缺失或窗口建不起来时返回 nil，
// main 把这当作致命错误。
//
// 之后没有任何东西再动它——建出来就是它要一直保持的尺寸，所以不存在第一帧位置或尺寸不对的
// 情况。
func newWindow(state windowState) *webviewWindow {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath: appDataDir(),
		// Debug 就是页面的两个开关：库（webview.go:118/123）把它直接交给
		// AreDefaultContextMenusEnabled 与 AreDevToolsEnabled。写成 true，页面的右键菜单与 F12
		// 的开发者工具就是开的——这是个游戏，那两样对报障的人有用；留成零值则两样都没有。
		Debug: true,
		WindowOptions: webview2.WindowOptions{
			Title:  appName,
			Width:  uint(state.Width),
			Height: uint(state.Height),
			// 一律居中：位置不跨运行记忆，而库只会把窗口居中，没法告诉它一个角坐标
			Center: true,
		},
	})
	if w == nil {
		// WebView2 运行时不可用，或者窗口没建起来
		return nil
	}

	win := &webviewWindow{w: w, hwnd: uintptr(w.Window())}

	// 必须趁窗口「已创建且已显示」时量：还没交给 DWM DwmGetWindowAttribute 会失败
	// 失败就退化成不修正，绝不猜一个厚度补上去
	if edges, ok := frameEdgesOf(win.hwnd); ok {
		win.edges = edges
	}

	// WM_CLOSE 换成隐藏：那个窗口一销毁就会 PostQuitMessage，会把托盘一起带走
	// 窗口不在这里收起来——主界面启动就该看得见，要再叫出来有「显示窗口」和左键
	win.previous, _, _ = procSetWindowLongPtrW.Call(
		win.hwnd, gwlpWndProc, windows.NewCallback(win.wndProc),
	)

	// 拖拽的下限：菜单算出来的尺寸由 size.go 夹住，鼠标拖出来的尺寸由这里夹住，两
	// 下限是同一个数。库把它记下来，WM_GETMINMAXINFO 时回给 Windows——ptMinTrackSize
	// window rect，所以下限和别处一样要加上边框
	if width, height := minVisibleSize(); win.w != nil {
		win.w.SetSize(
			width+int(win.edges.left+win.edges.right),
			height+int(win.edges.top+win.edges.bottom),
			webview2.HintMin,
		)
	}

	// 尺寸不再动：交给库的就是 window rect，和要记住的那个是同一个数
	win.logGeometry("startup")
	if state.Maximized {
		procShowWindow.Call(win.hwnd, swShowMaximized)
	}

	return win
}

// measureFrameEdges 用一个建完就扔的窗口，量出 DWM 在像本程序这样的窗口周围画的边框：
// 一个刚创建、还没上屏的窗口报出的边框和上屏的窗口一样（150% 下 WS_OVERLAPPEDWINDOW 是
// 9/0/9/9），而文档里那些度量不行——SM_CXSIZEFRAME 加 SM_CXPADDEDBORDER 给出 11，
// DWM 却只画 9。
//
// 这是给第一次运行用的：之后几次运行在状态文件里已有窗口矩形，只有第一次手里只有菜单，
// 而菜单说的是可见框那套话。返回 false 表示调用方按老样子打开窗口，而不是猜一个厚度。
func measureFrameEdges() (frameEdges, bool) {
	instance, _, _ := procGetModuleHandleW.Call(0)
	name, err := windows.UTF16PtrFromString(shadowClassName)
	if err != nil {
		return frameEdges{}, false
	}
	class := wndClassEx{
		wndProc:   procDefWindowProcW.Addr(),
		instance:  instance,
		className: name,
	}
	class.size = uint32(unsafe.Sizeof(class))
	// 同一个名字注册第二次会失败，这没关系：算数的是第一次那个
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class)))

	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(name)),
		0,
		wsOverlappedWindow,
		0, 0, 200, 200,
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return frameEdges{}, false
	}
	defer procDestroyWindow.Call(hwnd)
	return frameEdgesOf(hwnd)
}

// frameEdgesOf 量出 DWM 画在窗口可见框外面的边框，每一边都量。两个查询只要有一个失败就
// 返回 false，调用方随后按没有边框来干活，而不是从一次没成的查询里编一个边框出来。
func frameEdgesOf(hwnd uintptr) (frameEdges, bool) {
	var vis, wr rect
	if r, _, _ := procDwmGetWindowAttribute.Call(
		hwnd, dwmwaExtendedFrameBounds,
		uintptr(unsafe.Pointer(&vis)), unsafe.Sizeof(vis),
	); r != 0 {
		return frameEdges{}, false
	}
	if r, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&wr))); r == 0 {
		return frameEdges{}, false
	}
	edges := frameEdges{
		left:   vis.left - wr.left,
		top:    vis.top - wr.top,
		right:  wr.right - vis.right,
		bottom: wr.bottom - vis.bottom,
	}
	// 边框画在 window rect 外面，所以每一边都是非负数。出现别的值，说明这次查询是在窗口还没
	// 上边框的时候答的，那跟没有回答一样不值钱
	if edges.left < 0 || edges.top < 0 || edges.right < 0 || edges.bottom < 0 {
		return frameEdges{}, false
	}
	return edges, true
}

// shellOpen 把一个地址、或者任何别的文件交给 shell，由它按在资源管理器里双击那样打开——
// 所以用的是用户自己选的浏览器、平时用的配置文件。ShellExecuteW 成功时返回大于 32 的
// 值，失败时返回 shell 的错误码（低于 32），所以失败按其编号报出来。
func shellOpen(target string) {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		slog.Error("shell open: cannot build the verb", "error", err)
		return
	}
	page, err := windows.UTF16PtrFromString(target)
	if err != nil {
		slog.Error("shell open: cannot build the target", "error", err)
		return
	}
	ret, _, _ := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(page)), 0, 0, swShownormal)
	if ret <= 32 {
		slog.Error("shell open failed", "target", target, "code", ret)
	}
}

// SetTitle 写窗口标题。
//
// SetWindowTextW 本身是线程安全的——它只往窗口上写一段字，不碰 WebView2 的控制器——所以这里不像
// Eval 那样必须回到窗口线程：从哪个 goroutine 调都行。标题栏上那行版本号就是这么来的（见
// main.go 的 setTitle，它在后台问完上游之后补写标题）。
func (win *webviewWindow) SetTitle(title string) {
	if win == nil || win.w == nil {
		return
	}
	win.w.SetTitle(title)
}

// Show 把窗口叫出来，最小化时先还原。它是从菜单回调里调的，而菜单回调跑在自己的
// goroutine 上，所以要过一遍 Dispatch：WebView2 的控制器属于跑消息循环的那个线程。
func (win *webviewWindow) Show() {
	win.Dispatch((*webviewWindow).show, win)
}

func (win *webviewWindow) show() {
	// 先把声音放回来：藏起来时收掉的那一份（setMuted），这里不恢复就没别处会恢复了
	win.setMuted(false)
	procShowWindow.Call(win.hwnd, swRestore)
	procSetForegroundWindow.Call(win.hwnd)
}

// placeVisible 把窗口的可见框设成 width x height，左上角留在原处：菜单改尺寸并不是
// 要挪窗口。它直接跟 Win32 打交道，所以必须在窗口线程上跑。
func (win *webviewWindow) placeVisible(size [2]int) {
	width, height := size[0], size[1]
	vx, vy := win.visibleCorner(width, height)
	vx, vy = keepOnScreen(vx, vy, width, height)
	win.moveVisible(vx, vy, width, height, strconv.Itoa(width)+"x"+strconv.Itoa(height)+" visible")
}

// Center 把窗口放到屏幕正中，不动它的尺寸。它是窗口被拖得半个身子在屏幕外之后回来的路，
// 也是选完尺寸、故意没动窗口角之后回到正中的路。
//
// 用的是整块屏幕而不是工作区，好跟系统的做法对上：由 shell 摆放的窗口也会盖在任务栏上。
func (win *webviewWindow) Center() {
	win.Dispatch((*webviewWindow).center, win)
}

// center 干活的地方，在窗口线程上。
func (win *webviewWindow) center() {
	var wr rect
	if ret, _, _ := procGetWindowRect.Call(win.hwnd, uintptr(unsafe.Pointer(&wr))); ret == 0 {
		slog.Error("centre: GetWindowRect failed")
		return
	}
	width := int(wr.width() - win.edges.left - win.edges.right)
	height := int(wr.height() - win.edges.top - win.edges.bottom)
	if width <= 0 || height <= 0 {
		slog.Warn("centre: not a visible frame", "width", width, "height", height)
		return
	}
	// 两件事要一起对齐，少一件就会和启动时的位置差几个像素：
	//
	//  1. 居中的范围是**整块屏幕**，不是工作区——系统的居中就是这样（把窗口拖到任务栏上方，
	//     鼠标能越过的边界是屏幕）
	//  2. 居中用的*窗口矩形**，不是可见框——库创建窗口时就是这么摆的（Center: true），系统
	//     也一样。用可见框会差半个边框（这里上下 9、两侧 4 像素）
	//
	// 这两条都对齐之后，按下「居中」得到的就是启动那一刻的位置；缺一条，按下去就会看到窗
	// 往上或往下跳几个像素，同一件事有两个答案
	screen := screenArea()
	x := (screen.width() - wr.width()) / 2
	y := (screen.height() - wr.height()) / 2
	// 再换算成可见框的角：移动是按可见框说的，两个矩形差一个边框。夹取也用屏幕：比工作区
	// 高的窗口，把多出来的部分对半分会让标题栏跑到屏幕外
	vx := screen.left + x + win.edges.left
	vy := screen.top + y + win.edges.top
	vx, vy = clampTo(screen, vx, vy, width, height)
	win.moveVisible(vx, vy, width, height, "centred")
}

// TopMost 报告窗口是否在所有其他窗口之上。
func (win *webviewWindow) TopMost() bool {
	style, _, _ := procGetWindowLongPtrW.Call(win.hwnd, gwlExStyle)
	return style&wsExTopmost != 0
}

// ToggleTopMost 让窗口在「压在所有窗口之上」和普通层序之间来回翻，并报告翻完是哪种。
//
// 状态是在移动之前读的，不是在移动之后：移动要过 Dispatch，而它不等，所以这里唯一能给的
// 答案就是窗口即将变成的样子——也正是菜单勾选想要的那个。
func (win *webviewWindow) ToggleTopMost() bool {
	on := !win.TopMost()
	win.Dispatch(win.setTopMost, on)
	return on
}

// setTopMost 把窗口放到所有其他窗口之上，或者放回它们中间。它在窗口线程上跑。
func (win *webviewWindow) setTopMost(on bool) {
	after := hwndNotTopmost
	if on {
		after = hwndTopmost
	}
	if ret, _, err := procSetWindowPos.Call(
		win.hwnd, after, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoActivate,
	); ret == 0 {
		slog.Error("top most: SetWindowPos failed", "error", err)
		return
	}
	slog.Info("topmost changed", "topmost", on)
}

// moveVisible 把可见框的左上角放到 vx、vy，并把它设成 width x height。两个矩形必须在
// 这一个地方换算：菜单说的是人看得见的框，而 SetWindowPos 要的是 window rect，所以要把
// 创建时量到的边框加回去。反过来把 window rect 设成可见尺寸，会让框比要求的位置多出一个
// 边框宽。
//
// 最大化窗口不管你怎么说都保持它最大化时的矩形，所以先还原它：不这么做，从菜单选的尺寸或
// 位置要等用户手动还原窗口才看得见。
func (win *webviewWindow) moveVisible(vx, vy int32, width, height int, want string) {
	if wp, ok := windowPlacementOf(win.hwnd); ok && wp.showCmd == swShowMaximized {
		procShowWindow.Call(win.hwnd, swRestore)
	}

	// 进去的是可见边框的角，出来的是 window rect 的角：两者差着一个边框，而要保持的是用户
	// 看得见的那个
	cx := int32(width) + win.edges.left + win.edges.right
	cy := int32(height) + win.edges.top + win.edges.bottom
	x := vx - win.edges.left
	y := vy - win.edges.top
	if ret, _, err := procSetWindowPos.Call(
		win.hwnd, 0,
		uintptr(x), uintptr(y), uintptr(cx), uintptr(cy),
		swpNoZOrder|swpNoActivate,
	); ret == 0 {
		slog.Error("move window: SetWindowPos failed", "error", err)
		return
	}
	win.logGeometry(want)
}

// visibleCorner 是窗口可见框现在起于哪里；没有框可读时，就是工作区的正中。
func (win *webviewWindow) visibleCorner(width, height int) (int32, int32) {
	var vis rect
	if r, _, _ := procDwmGetWindowAttribute.Call(win.hwnd, dwmwaExtendedFrameBounds,
		uintptr(unsafe.Pointer(&vis)), unsafe.Sizeof(vis)); r == 0 {
		return vis.left, vis.top
	}
	wa := workArea()
	return wa.left + (wa.width()-int32(width))/2, wa.top + (wa.height()-int32(height))/2
}

// keepOnScreen 把一个角拉回工作区里：从菜单选的尺寸不该让窗口长到任务栏上去。
func keepOnScreen(vx, vy int32, width, height int) (int32, int32) {
	return clampTo(workArea(), vx, vy, width, height)
}

// clampTo 把一个角拉回 area 里，不管那是哪个矩形：菜单选的尺寸用工作区，居中用整块屏幕
// （见 center）。没有它，窗口会长出或移出边缘，把标题栏也一起带走，到了那儿就够不着、拖不
// 回来。先夹远边，所以比 area 还大的窗口会贴住左上角，而不是停在一组既不在里面、也不在
// 边上的坐标上。
func clampTo(area rect, vx, vy int32, width, height int) (int32, int32) {
	if area.width() <= 0 {
		return vx, vy
	}
	if max := area.right - int32(width); vx > max {
		vx = max
	}
	if max := area.bottom - int32(height); vy > max {
		vy = max
	}
	if vx < area.left {
		vx = area.left
	}
	if vy < area.top {
		vy = area.top
	}
	return vx, vy
}

// screenArea 是整块主显示器，含任务栏。居中用它而不是工作区，因为系统的居中就是按它算的；
// 见 center。
func screenArea() rect {
	width, height := displaySize()
	return rect{right: int32(width), bottom: int32(height)}
}

// logGeometry 写下窗口的三个矩形和它的边框。
//
// 这是自检：window 减 visible 应该等于边框，visible 减 client 应该是标题栏和边框。want
// 说明当时在要什么，所以一行就够看出窗口落到了哪里、为什么。WebView2 的控制器会跟着
// WM_SIZE 走，所以这里没有什么是手工同步的。
//
// 工作区也在这一行里：居中窗口按它居中，菜单选的尺寸靠它留在屏幕上，把它放在结果旁边，
// 「为什么没在正中」才不用再跑一次就能答上来。
func (win *webviewWindow) logGeometry(want string) {
	var wr, cr, vis rect
	procGetWindowRect.Call(win.hwnd, uintptr(unsafe.Pointer(&wr)))
	procGetClientRect.Call(win.hwnd, uintptr(unsafe.Pointer(&cr)))
	visibleW, visibleH := "?", "?"
	if ret, _, _ := procDwmGetWindowAttribute.Call(win.hwnd, dwmwaExtendedFrameBounds,
		uintptr(unsafe.Pointer(&vis)), unsafe.Sizeof(vis)); ret == 0 {
		visibleW = strconv.Itoa(int(vis.width()))
		visibleH = strconv.Itoa(int(vis.height()))
	}
	wa := workArea()
	slog.Info("window geometry",
		"want", want,
		"windowX", wr.left, "windowY", wr.top, "windowWidth", wr.width(), "windowHeight", wr.height(),
		"visibleWidth", visibleW, "visibleHeight", visibleH,
		"clientWidth", cr.width(), "clientHeight", cr.height(),
		"edgeLeft", win.edges.left, "edgeTop", win.edges.top,
		"edgeRight", win.edges.right, "edgeBottom", win.edges.bottom,
		"workX", wa.left, "workY", wa.top, "workWidth", wa.width(), "workHeight", wa.height())
}

func (win *webviewWindow) Run() { win.w.Run() }

// Destroy 真正把窗口拆掉，绕过上面接管的 WM_CLOSE。
func (win *webviewWindow) Destroy() {
	procDestroyWindow.Call(win.hwnd)
}

// WindowRect 报告要留给下一次运行的尺寸。
//
// 这个矩形来自 GetWindowPlacement 的 rcNormalPosition，也就是窗口被还原时回到的那个。
// 哪怕窗口正处在最大化或最小化——用户最可能在这种状态下退出——也值得把它记下来；
// showCmd 跟着它一起走，这样最大化退出的窗口下次才会最大化打开。
func (win *webviewWindow) WindowRect() (windowState, bool) {
	// 全屏时窗口铺满整个显示器，那不是用户选的尺寸：要记的是进全屏之前那个矩形
	if s, ok := fullScreenWindowRect(); ok {
		return s, true
	}
	wp, ok := windowPlacementOf(win.hwnd)
	if !ok {
		return windowState{}, false
	}
	r := wp.rcNormalPosition
	return windowState{
		Width:     int(r.width()),
		Height:    int(r.height()),
		Maximized: wp.showCmd == swShowMaximized,
	}, true
}

// windowPlacementOf 向 Windows 要窗口还原后的矩形，以及它当前所处的状态。
func windowPlacementOf(hwnd uintptr) (windowPlacement, bool) {
	var wp windowPlacement
	wp.length = uint32(unsafe.Sizeof(wp))
	if r, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp))); r == 0 {
		return windowPlacement{}, false
	}
	return wp, true
}

// displaySize 是主显示器的像素尺寸。
//
// 它跟随进程的 DPI 感知，所以在缩放过的显示器上，这些是物理像素，而不是 webview 会叫作
// vw 的逻辑像素。要在 systray.EnableDPIAwareness 之后调它；如果窗口不打算开在主显示器
// 上，就把它换成实际那块显示器的尺寸。
func displaySize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(smCXScreen)
	h, _, _ := procGetSystemMetrics.Call(smCYScreen)
	return int(w), int(h)
}

// workArea 是桌面减去任务栏。按整块屏幕居中会把窗口的下边缘塞到任务栏底下。
func workArea() rect {
	var r rect
	procSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0)
	return r
}

// setWindowIcons 给窗口装上任务栏、Alt+Tab 和标题栏画的那些图标。
//
// 这不是 .exe 里面的图标——那个是资源，由 internal/genicon 写进去。窗口的图标是运行时的
// 东西，而 go-webview2 注册的类一个都没有：没有这个函数，任务栏、Alt+Tab 和标题栏就退回
// 默认图标。
//
// 每个图标都按 Windows 说要的尺寸画，而不是一律画 16 和 32：缩放过的显示器要更大的图标
// （150% 时 SM_CXSMICON 是 24，SM_CXICON 是 48），而且不会把它拿到的东西放大，所以画 16、
// 显示 24 的图标是拉伸出来的，那就是模糊的来源。按要求的尺寸重画一遍美术资源几乎不花什么
// 代价，出来却是清晰的。
//
// 这些 HICON 之后是故意不销毁的：它们属于窗口，必须活过这次调用，由进程结束来释放。
func (win *webviewWindow) setWindowIcons() error {
	icons := make([]uintptr, 0, 2)
	sizes := make([]int, 0, 2)
	for _, want := range []struct {
		metric   uintptr
		fallback int
		slot     uintptr
		where    string
	}{
		{smCXSmallIcon, 16, iconSlotSmall, "title bar"},
		{smCXIcon, 32, iconSlotBig, "taskbar and Alt+Tab"},
	} {
		size := want.fallback
		if got, _, _ := procGetSystemMetrics.Call(want.metric); got > 0 {
			size = int(got)
		}
		sizes = append(sizes, size)

		blob, err := artwork.IconICO(size)
		if err != nil {
			return fmt.Errorf("%dpx icon for the %s: %w", size, want.where, err)
		}
		_, image, err := artwork.IconEntry(blob, size)
		if err != nil {
			return fmt.Errorf("%dpx icon for the %s: %w", size, want.where, err)
		}
		// fIcon 1：这是图标，不是光标
		hicon, _, err := procCreateIconFromResourceEx.Call(
			uintptr(unsafe.Pointer(&image[0])), uintptr(len(image)),
			1, iconResourceVersion, uintptr(size), uintptr(size), 0)
		if hicon == 0 {
			return fmt.Errorf("%dpx icon for the %s: %v", size, want.where, err)
		}
		icons = append(icons, hicon)
		procSendMessageW.Call(win.hwnd, wmSetIcon, want.slot, hicon)
	}

	// 类上也挂同样两张图，给那种去类里找图标的系统用
	// 用这个类的只有本程序的窗口，所以不影响别的东西
	procSetClassLongPtrW.Call(win.hwnd, gclpHIconSmall, icons[0])
	procSetClassLongPtrW.Call(win.hwnd, gclpHIcon, icons[1])

	slog.Info("window icons", "titleBar", sizes[0], "taskbar", sizes[1])
	return nil
}
