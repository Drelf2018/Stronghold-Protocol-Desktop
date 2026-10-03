//go:build windows

package main

// Going fullscreen from the page.
//
// The game has its own fullscreen button (public/js/ui/device.js), and it goes through the standard
// Fullscreen API - which inside WebView2 only makes the page fill the *control*. The window keeps its
// frame and its size, so the button looks like it does nothing. The window is the thing that has to
// change, and only this side can change it: js/fullscreen.js reports what the page did through the
// _fullscreen binding, and this does the Win32 part.
//
// What has to be put back is three things, so all three are kept: the window style, the placement
// (which knows how to return a maximised window to maximised) and whether the window was
// always-on-top before. A zero style means "not fullscreen now".

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
	// GWL_STYLE, and the style this swaps WS_OVERLAPPEDWINDOW for.
	gwlStyle = ^uintptr(15)
	wsPopup  = 0x80000000

	// SWP_FRAMECHANGED makes Windows recompute the frame after a style change - without it the
	// borderless window keeps drawing its old border until something else moves it.
	swpFrameChanged = 0x0020
	swpShowWindow   = 0x0040

	// MONITOR_DEFAULTTONEAREST: the monitor the window is on, or the nearest one.
	monitorDefaultToNearest = 2
)

// monitorInfo is MONITORINFO. rcMonitor is the whole screen; rcWork is the part of it the taskbar
// does not cover. Fullscreen wants the whole screen.
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

// fullScreen makes the window cover its monitor, or puts it back the way it was.
//
// It must run on the window's thread, like every other Win32 call in this program. The binding that
// calls it does: web messages arrive on the thread that owns the WebView2.
func (win *webviewWindow) fullScreen(on bool) {
	if on == (fullScreenStyle != 0) {
		return
	}
	if !on {
		procSetWindowLongPtrW.Call(win.hwnd, gwlStyle, fullScreenStyle)
		procSetWindowPlacement.Call(win.hwnd, uintptr(unsafe.Pointer(&fullScreenPlace)))
		// 置顶是用户自己选的，全屏时被借用了一下，要还回原样，而不是留下全屏顺手加上的那个。
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
		// 问不到显示器就退到主屏整块：总比什么都不做更像全屏。
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

// fullScreenWindowRect is the rectangle the window would be at if it were not fullscreen. It answers
// false when the window is not fullscreen, which is the ordinary case - the caller then asks Windows.
//
// Without this, quitting while fullscreen would remember the monitor's size as the window's size.
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
