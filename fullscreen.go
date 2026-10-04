//go:build windows

package main

// 从页面进入全屏。
//
// 游戏有自己的全屏按钮（public/js/ui/device.js），走的是标准 Fullscreen API——在 WebView2 里
// 它只让页面铺满那个 *控件*。窗口自己的边框和尺寸都不变，于是按钮看起来毫无作用。要变的是
// 窗口，而只有这一侧能变它：js/fullscreen.js 通过 _fullscreen 绑定上报页面做了什么，这边做
// Win32 那部分。
//
// 要还原的是三样东西，所以三样都留着：窗口样式、placement（它知道怎么把一个最大化的窗口
// 还原回最大化）以及窗口先前是不是置顶。样式为 0 就表示「现在不是全屏」。

import (
	"log/slog"
	"unsafe"
)

var (
	procMonitorFromWindow  = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW    = user32.NewProc("GetMonitorInfoW")
	procSetWindowPlacement = user32.NewProc("SetWindowPlacement")
)

const (
	// GWL_STYLE，以及拿来换掉 WS_OVERLAPPEDWINDOW 的那个样式。
	gwlStyle = ^uintptr(15)
	wsPopup  = 0x80000000

	// SWP_FRAMECHANGED 让 Windows 在样式改动后重算边框——没有它，无边框窗口会一直画着旧边框，
	// 直到别的什么东西来挪动它。
	swpFrameChanged = 0x0020
	swpShowWindow   = 0x0040

	// MONITOR_DEFAULTTONEAREST：窗口所在的那台显示器，或者最近的那台。
	monitorDefaultToNearest = 2
)

// monitorInfo 对应 MONITORINFO。rcMonitor 是整块屏幕；rcWork 是任务栏没盖住的那部分。
// 全屏要的是整块屏幕。
type monitorInfo struct {
	size    uint32
	monitor rect
	work    rect
	flags   uint32
}

var (
	fullScreenStyle uintptr
	fullScreenPlace windowPlacement
	fullScreenTop   bool
)

// fullScreen 让窗口铺满自己那台显示器，或者把它恢复成原来的样子。
//
// 它必须跑在窗口的线程上，本程序里每一个 Win32 调用都是如此。调用它的那个绑定就做到了：
// web 消息到达的，正是拥有 WebView2 的那个线程。
func (win *webviewWindow) fullScreen(on bool) {
	if on == (fullScreenStyle != 0) {
		return
	}
	if !on {
		procSetWindowLongPtrW.Call(win.hwnd, gwlStyle, fullScreenStyle)
		procSetWindowPlacement.Call(win.hwnd, uintptr(unsafe.Pointer(&fullScreenPlace)))
		// 置顶是用户自己选的，全屏时只是被借去用一下，要还回原样，而不是留下全屏顺手加上的那个。
		win.setTopMost(fullScreenTop)
		fullScreenStyle = 0
		slog.Info("fullscreen: off")
		return
	}

	style, _, _ := procGetWindowLongPtrW.Call(win.hwnd, gwlStyle)
	place, ok := windowPlacementOf(win.hwnd)
	if !ok {
		slog.Warn("fullscreen: cannot read the window's placement; leaving the window alone")
		return
	}
	area := monitorInfo{size: uint32(unsafe.Sizeof(monitorInfo{}))}
	monitor, _, _ := procMonitorFromWindow.Call(win.hwnd, monitorDefaultToNearest)
	if r, _, _ := procGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&area))); r == 0 {
		// 问不到显示器就退到主屏整块：总比什么都不做更像个全屏。
		cx, _, _ := procGetSystemMetrics.Call(smCXScreen)
		cy, _, _ := procGetSystemMetrics.Call(smCYScreen)
		area.monitor = rect{0, 0, int32(cx), int32(cy)}
		slog.Warn("fullscreen: cannot read the monitor's rectangle; using the whole screen")
	}

	fullScreenStyle, fullScreenPlace, fullScreenTop = style, place, win.TopMost()
	procSetWindowLongPtrW.Call(win.hwnd, gwlStyle, style&^uintptr(wsOverlappedWindow)|uintptr(wsPopup))
	procSetWindowPos.Call(win.hwnd, hwndTopmost,
		uintptr(area.monitor.left), uintptr(area.monitor.top),
		uintptr(area.monitor.width()), uintptr(area.monitor.height()),
		swpFrameChanged|swpShowWindow)
	slog.Info("fullscreen: on",
		"width", area.monitor.width(), "height", area.monitor.height(), "topMostBefore", fullScreenTop)
}

// fullScreenWindowRect 是窗口若不是全屏时**会**落在的那个矩形。窗口不在全屏时它返回 false，
// 那是平常情况——调用方接着去问 Windows。
//
// 没有它，在全屏状态下退出就会把显示器的尺寸记成窗口的尺寸。
func fullScreenWindowRect() (windowState, bool) {
	if fullScreenStyle == 0 {
		return windowState{}, false
	}
	return windowState{
		Width:     int(fullScreenPlace.rcNormalPosition.width()),
		Height:    int(fullScreenPlace.rcNormalPosition.height()),
		Maximized: fullScreenPlace.showCmd == swShowMaximized,
	}, true
}
