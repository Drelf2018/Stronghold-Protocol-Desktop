//go:build windows

package main

// The window's own thread: the procedure that receives its messages, and the queue of work
// handed to it from the goroutines that must not touch WebView2 themselves.

import (
	_ "embed"
	"log/slog"
	"sync"
	"time"
)

// wmRunOnWindowThread is the message Dispatch posts to the window. It is WM_APP, the value
// go-webview2 wakes its own dispatch queue with: a window message carrying it does both jobs -
// it runs what this app queued, and it lets the library flush what was queued through it.
const wmRunOnWindowThread = 0x8000

// wndProc hides the window instead of closing it, so the tray survives a click on
// the X, and it runs what Dispatch queued - this procedure is the window's thread, and it is
// reached by whichever loop is pumping. Every other message goes to the procedure go-webview2
// installed - including WM_GETMINMAXINFO, which is how the minimum size below takes effect.
func (win *webviewWindow) wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	if msg == wmRunOnWindowThread {
		runOnWindowThread()
		return 0
	}
	if msg == wmClose {
		// 关掉窗口就是藏起来，而藏起来并不会让页面变成 hidden——声音得自己收（见 setMuted）。
		win.setMuted(true)
		procShowWindow.Call(hwnd, swHide)
		return 0
	}
	if msg == wmSysCommand {
		// 低四位由系统占用，比对自己那几个 id 之前先抹掉。
		if handleSystemCommand(wparam &^ 0xF) {
			return 0
		}
	}
	res, _, _ := procCallWindowProcW.Call(win.previous, hwnd, msg, wparam, lparam)
	return res
}

func (win *webviewWindow) Navigate(url string) { win.w.Navigate(url) }

// Reload asks the page to load itself again, at the address it is on right now.
//
// That address is the page's own, not the one the app would navigate to. The game keeps all of its
// state inside the page, so 刷新 means "fetch this same page again", not "rebuild the address from
// something the app remembered".
//
//go:embed js/reload.js
var reloadJS string

func (win *webviewWindow) Reload() {
	win.Dispatch(win.w.Eval, reloadJS)
}

// askForRoomJS is the prompt the join menu item puts up; the handler that runs it is in main.go. The
// page is the only place this program can ask a question: there is no console, and a message box
// cannot take a line of text back.
//
//go:embed js/askForRoom.js
var askForRoomJS string

// fullscreenJS reports the page's fullscreen state to Go. The game has its own fullscreen button, and
// what that button does inside WebView2 is not enough on its own - see fullscreen.go.
//
//go:embed js/fullscreen.js
var fullscreenJS string

// Bind publishes a Go function to the page as window.<name>. It has to be registered before Run: it
// is a script for every document about to be created, and the document already open is not one of
// them. The one binding this program has is the page handing back what was typed into the join
// prompt (see askForRoom).
func (win *webviewWindow) Bind(name string, fn any) error { return win.w.Bind(name, fn) }

// mutePageOn and mutePageOff are what Go says to the page when the window goes away and comes back.
// They are written beside the script that defines _mutePage on purpose: one side renamed without
// the other is not an error anywhere, it is simply sound that keeps playing - or never comes back.
const (
	mutePageOn  = "window._mutePage && window._mutePage(true)"
	mutePageOff = "window._mutePage && window._mutePage(false)"
)

//go:embed js/mute.js
var muteJS string

// setMuted stops the page's sound, or lets it come back.
//
// The close button hides the window, and a hidden window is still a visible *page*: WebView2 only
// tells the page otherwise when the window is minimised, which is why minimising the game went
// quiet and closing it did not. The page has its own handling for that (public/js/audio.js
// suspends its AudioContext on visibilitychange); what it never gets from us is the event that
// says so. This is the other way to the same place: the injected script holds every AudioContext
// the page made and suspends or resumes them.
//
// Eval rather than a binding: nothing comes back, and the page has no say in it. Both callers -
// the window procedure and show - are already on the window's thread, which is where WebView2
// calls belong.
func (win *webviewWindow) setMuted(on bool) {
	if win == nil || win.w == nil {
		return
	}
	script := mutePageOn
	if !on {
		script = mutePageOff
	}
	win.w.Eval(script)
}

// The work waiting for the window's thread, and the lock that guards it.
var (
	uiMu    sync.Mutex
	uiQueue []uiWork
)

// uiWork is one queued call and the moment it was queued, so that a call that ran late can say
// how late it was.
type uiWork struct {
	at time.Time
	f  func()
}

// Dispatch runs f on the thread that owns the window: the one the message loop and the
// WebView2 controller both live on. The menu's goroutines are not that thread, and a
// controller call made from one of them does nothing at all.
//
// It posts a message to the *window*, not to the thread (which is what the library's
// Dispatch does). A thread message belongs to no window, so a modal loop - a menu, a message
// box - retrieves it and DispatchMessageW has nothing to deliver it to; everything queued
// behind it then waits for the next thing to post one. The rule is this one line.
//
// The call and its argument are passed apart rather than closed over, so that the call which
// will run is written at the call site instead of living inside a closure body - and so that
// the argument goes through the compiler's type check against the function it is for. A body
// that is not one call goes in as a closure taking an unused argument, as window_test.go does.
func (win *webviewWindow) Dispatch[T any](f func(T), t T) {
	uiMu.Lock()
	uiQueue = append(uiQueue, uiWork{at: time.Now(), f: func() { f(t) }})
	uiMu.Unlock()
	procPostMessageW.Call(win.hwnd, wmRunOnWindowThread, 0, 0)
}

// runOnWindowThread runs what Dispatch queued. It is called from the window procedure, which is
// the window's thread by definition - the library's loop or a menu's modal loop, whichever is
// pumping at that moment.
func runOnWindowThread() {
	uiMu.Lock()
	queued := uiQueue
	uiQueue = nil
	uiMu.Unlock()
	for _, work := range queued {
		if late := time.Since(work.at); late > time.Second {
			// 排进来和跑起来隔了这么久：有人把唤醒消息吃掉了。这一条是留给下一次的线索。
			slog.Warn("queued work ran late", "late", late.Round(time.Millisecond))
		}
		work.f()
	}
}
