//go:build windows

package main

// The WebView2 window and the Win32 it sits on: how it is built, the frame DWM draws
// around it, the two rectangles that frame makes it easy to confuse, and the message
// procedure that turns a click on the X into a hide.
//
// Everything the app's own size menus ask for lands here as a visible frame, and
// everything Win32 asks for is a window rect. frameEdges is the difference, measured on
// this window rather than assumed, and moveVisible is the single place it is applied.

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

	// SW_SHOWMAXIMIZED, for reopening a window the user left maximised.
	swShowMaximized = 3

	// SW_SHOWNORMAL, what the shell is asked for when it opens something.
	swShownormal = 1

	wmClose = 0x0010

	// GWLP_WNDPROC, which is -4 as a LONG_PTR.
	gwlpWndProc = ^uintptr(3)

	// GWL_EXSTYLE, which is -20 as a LONG_PTR, and WS_EX_TOPMOST inside it.
	gwlExStyle  = ^uintptr(19)
	wsExTopmost = 0x0008

	// WM_SETICON, and the two slots a window keeps an icon in: ICON_SMALL for the title
	// bar, ICON_BIG for the taskbar and Alt+Tab.
	wmSetIcon     = 0x0080
	iconSlotSmall = 0
	iconSlotBig   = 1

	// GCLP_HICON and GCLP_HICONSM: the same two pictures on the window class, where a
	// shell that asks the class rather than the window finds them.
	gclpHIcon      = ^uintptr(13)
	gclpHIconSmall = ^uintptr(33)

	// WM_SYSCOMMAND: the message the system menu sends back. Picking the items we appended to
	// it comes through here too.
	wmSysCommand = 0x0112

	// The flags AppendMenuW and CheckMenuItem use.
	mfString     = 0x0000
	mfByPosition = 0x0400
	mfSeparator  = 0x0800
	mfPopup      = 0x0010
	mfChecked    = 0x0008
	mfUnchecked  = 0x0000

	// The version of the icon resource format CreateIconFromResourceEx reads.
	iconResourceVersion = 0x00030000

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	// HWND_TOPMOST and HWND_NOTOPMOST: the two places SetWindowPos can put a window.
	hwndTopmost    = ^uintptr(0)
	hwndNotTopmost = ^uintptr(1)

	dwmwaExtendedFrameBounds = 9
	spiGetWorkArea           = 0x0030

	// SM_CXSCREEN, SM_CYSCREEN: the primary display.
	smCXScreen = 0
	smCYScreen = 1

	// SM_CXICON and SM_CXSMICON: the width of the icons a window is asked for, scaled to
	// the display. They are 32 and 16 at 100%, 48 and 24 at 150%.
	smCXIcon      = 11
	smCXSmallIcon = 49

	// WS_OVERLAPPEDWINDOW, the style go-webview2 gives the real window: the frame DWM
	// draws depends on it, so the throwaway window in measureFrameEdges wears it too.
	wsOverlappedWindow = 0x00CF0000

	// shadowClassName names that throwaway window's class. Nothing else uses it, so it
	// cannot collide with the class go-webview2 registers for the real one.
	shadowClassName = "systrayExampleShadow"
)

type rect struct{ left, top, right, bottom int32 }

func (r rect) width() int32  { return r.right - r.left }
func (r rect) height() int32 { return r.bottom - r.top }

type point struct{ x, y int32 }

// windowPlacement is WINDOWPLACEMENT, whose rcNormalPosition is the rectangle the
// window returns to when it is restored the right rectangle to remember, even
// while the window is maximised or minimised.
type windowPlacement struct {
	length           uint32
	flags            uint32
	showCmd          uint32
	ptMinPosition    point
	ptMaxPosition    point
	rcNormalPosition rect
}

// wndClassEx is WNDCLASSEXW, for the throwaway window in measureFrameEdges.
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

// frameEdges is the border DWM draws outside the visible frame, per side: the
// window rect is the visible frame grown by these. The thickness follows the theme
// and the Windows version, so it is measured rather than assumed, and it is not the
// same on every side.
type frameEdges struct{ left, top, right, bottom int32 }

// webviewWindow is the app's main window, hosted by WebView2.
//
// Three rectangles describe one window, and mixing them is where "I set 1000 and
// measured 984" comes from:
//
//	window rect   CreateWindowExW / SetWindowPos   title bar, borders, and the invisible
//	                                              resize border DWM draws around it
//	visible frame DwmGetWindowAttribute           what the eye and a screenshot get
//	client area   GetClientRect                   what the page renders into
//
// The window is built and remembered in window rects, so the size it opens with is
// the size it was left at, exactly. The menus are the exception: they describe a share
// of the screen the way a person would, so SetVisibleSize converts through edges -
// the border measured on this window at creation, never assumed.
type webviewWindow struct {
	w        webview2.WebView
	hwnd     uintptr
	previous uintptr // the window procedure this one replaced
	edges    frameEdges
}

// newWindow creates the WebView2 window at the size state describes, centred on the screen, and
// leaves it visible: it is the interface, and 显示窗口 brings it back once it is closed. It
// returns nil when the WebView2 runtime is missing or the window cannot be created, which main
// treats as fatal.
//
// Nothing moves it afterwards - it is built at the size it will keep, so there is no first
// frame at the wrong place or size.
func newWindow(state windowState) *webviewWindow {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath: appDataDir(),
		// Debug 打开 WebView2 的开发者工具和页面自己的右键菜单：库就是把它交
		// AreDevToolsEnabled AreDefaultContextMenusEnabled 的。留成零值（原来的样子）时，
		// F12 什么都打不开，页面也没有自己的右键菜单
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

// measureFrameEdges learns the border DWM draws around a window like this app's, by making one
// and throwing it away: a window that has only been created reports the same frame as one on
// screen (9/0/9/9 for a WS_OVERLAPPEDWINDOW at 150%), while the documented metrics do not -
// SM_CXSIZEFRAME plus SM_CXPADDEDBORDER gives 11 where DWM draws 9.
//
// It is for the first run: later runs have the window rectangle in their state file, and the
// first has only the menus, which speak in visible terms. False means the caller opens the
// window as it did before rather than guessing a thickness.
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

// frameEdgesOf measures the border DWM draws outside the window's visible frame, on
// each side. It answers false when either query fails, and the caller then works with
// no border rather than inventing one from a query that did not work.
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

// shellOpen hands an address, or any other file, to the shell, which opens it the way a
// double click in Explorer would - so it is the browser the user chose, with the profile
// they normally use. ShellExecuteW answers with a value above 32 on success and one of the
// shell's error codes below it, so a failure is reported by number.
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

// SetTitle writes the window's title.
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

// Show brings the window up, restoring it if it was minimised. It is called from a
// menu callback, which runs on its own goroutine, so it goes through Dispatch:
// WebView2's controller belongs to the thread running the loop.
func (win *webviewWindow) Show() {
	win.Dispatch((*webviewWindow).show, win)
}

func (win *webviewWindow) show() {
	// 先把声音放回来：藏起来时收掉的那一份（setMuted），这里不恢复就没别处会恢复了
	win.setMuted(false)
	procShowWindow.Call(win.hwnd, swRestore)
	procSetForegroundWindow.Call(win.hwnd)
}

// placeVisible gives the window the visible frame width x height, keeping its top left
// corner where it is: a menu that changes the size is not a request to move the window.
// It talks to Win32 directly, so it must run on the window's thread.
func (win *webviewWindow) placeVisible(size [2]int) {
	width, height := size[0], size[1]
	vx, vy := win.visibleCorner(width, height)
	vx, vy = keepOnScreen(vx, vy, width, height)
	win.moveVisible(vx, vy, width, height, strconv.Itoa(width)+"x"+strconv.Itoa(height)+" visible")
}

// Center puts the window in the middle of the screen without changing its size. It is the way
// back from a window that has been dragged half off the screen, and the way back to the middle
// after a size was picked with the corner deliberately left alone.
//
// The screen rather than the work area, so that it agrees with what the system does: a window
// placed by the shell sits over the taskbar too.
func (win *webviewWindow) Center() {
	win.Dispatch((*webviewWindow).center, win)
}

// center does the work, on the window's thread.
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

// TopMost reports whether the window is above every other window.
func (win *webviewWindow) TopMost() bool {
	style, _, _ := procGetWindowLongPtrW.Call(win.hwnd, gwlExStyle)
	return style&wsExTopmost != 0
}

// ToggleTopMost flips the window between on top of everything and the ordinary order, and
// reports which it is now.
//
// The state is read before the move rather than after it: the move goes through Dispatch,
// which does not wait, so what the window is about to be is the only answer available here
// - and it is the one the menu's tick wants.
func (win *webviewWindow) ToggleTopMost() bool {
	on := !win.TopMost()
	win.Dispatch(win.setTopMost, on)
	return on
}

// setTopMost puts the window above every other window, or back among them. It runs on the
// window's thread.
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

// moveVisible puts the visible frame's top left corner at vx, vy and makes it width x
// height. It is the one place the two rectangles have to be converted: a menu speaks of the
// frame a person sees, and SetWindowPos takes a window rect, so the border measured at
// creation is added back. Setting the window rect to the visible size instead would put the
// frame a border's width past where it was asked for.
//
// A maximised window keeps its maximised rectangle whatever it is told, so it is restored
// first: without that, a size or a place picked from the menu would not be seen until the
// user restored the window by hand.
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

// visibleCorner is where the window's visible frame starts now, or the middle of the
// work area when there is no frame to read.
func (win *webviewWindow) visibleCorner(width, height int) (int32, int32) {
	var vis rect
	if r, _, _ := procDwmGetWindowAttribute.Call(win.hwnd, dwmwaExtendedFrameBounds,
		uintptr(unsafe.Pointer(&vis)), unsafe.Sizeof(vis)); r == 0 {
		return vis.left, vis.top
	}
	wa := workArea()
	return wa.left + (wa.width()-int32(width))/2, wa.top + (wa.height()-int32(height))/2
}

// keepOnScreen pulls a corner back inside the work area: a size picked from the menu should not
// grow the window over the taskbar.
func keepOnScreen(vx, vy int32, width, height int) (int32, int32) {
	return clampTo(workArea(), vx, vy, width, height)
}

// clampTo pulls a corner back inside area, whichever rectangle that is: the work area for a size
// picked from the menu, the whole screen for centring (see center). Without it the window would
// grow or move off the edge, taking the title bar with it, where it cannot be reached to drag
// back. The far edge is applied first, so a window larger than the area ends up against the top
// left rather than at coordinates that are neither inside nor on the edge.
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

// screenArea is the whole primary display, taskbar included. Centring uses it rather than the
// work area because that is what the system centres in; see center.
func screenArea() rect {
	width, height := displaySize()
	return rect{right: int32(width), bottom: int32(height)}
}

// logGeometry writes the window's three rectangles and its frame edges.
//
// It is the self check: window minus visible should be the frame edges, visible minus
// client should be the title bar and borders. want says what was being asked for, so
// one line is enough to tell where a window ended up and why. WebView2's controller
// follows WM_SIZE, so nothing is synced by hand here.
//
// The work area is in the line too: it is what 居中窗口 centres in and what keeps a size
// picked from the menu on screen, and having it next to the result is what makes "why is it
// not in the middle" answerable without another run.
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

// Destroy takes the window down for real, bypassing the WM_CLOSE handled above.
func (win *webviewWindow) Destroy() {
	procDestroyWindow.Call(win.hwnd)
}

// WindowRect reports the size to remember for the next run.
//
// The rectangle comes from GetWindowPlacement's rcNormalPosition, the one the window
// returns to when it is restored. That is the rectangle worth keeping even while the
// window is maximised or minimised - the state a user is most likely to quit from -
// and showCmd travels with it so that a maximised window reopens maximised.
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

// windowPlacementOf asks Windows for the window's restored rectangle and the state
// it is in now.
func windowPlacementOf(hwnd uintptr) (windowPlacement, bool) {
	var wp windowPlacement
	wp.length = uint32(unsafe.Sizeof(wp))
	if r, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp))); r == 0 {
		return windowPlacement{}, false
	}
	return wp, true
}

// displaySize is the primary display's size in pixels.
//
// It follows the process's DPI awareness, so on a scaled display these are physical
// pixels rather than the logical ones a webview would call vw. Call it after
// systray.EnableDPIAwareness, and replace it with the actual monitor's size if the
// window is not going to open on the primary display.
func displaySize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(smCXScreen)
	h, _, _ := procGetSystemMetrics.Call(smCYScreen)
	return int(w), int(h)
}

// workArea is the desktop minus the taskbar. Centring on the full screen would put
// the bottom edge of the window under the taskbar.
func workArea() rect {
	var r rect
	procSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0)
	return r
}

// setWindowIcons gives the window the icons the taskbar, Alt+Tab and the title bar draw.
//
// This is not the icon inside the .exe - that one is a resource, written by internal/genicon. A
// window's icon is a runtime thing, and the class go-webview2 registers has none: without this,
// the taskbar, Alt+Tab and the title bar fall back to the default.
//
// Each one is drawn at the size Windows says it wants rather than at 16 and 32: a scaled
// display asks for larger icons (SM_CXSMICON is 24 at 150%, SM_CXICON 48) and does not
// scale up what it is handed, so an icon drawn at 16 and shown at 24 is a stretched one,
// which is the blur. Drawing the artwork again at the asked-for size costs almost nothing
// and comes out sharp.
//
// The HICONs are deliberately not destroyed afterwards: they belong to the window and have
// to outlive this call, and the end of the process is what releases them.
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
