//go:build windows

package main

// 窗口自己的线程：接收它消息的那个过程，以及从那些不能亲自碰 WebView2
// 的 goroutine 交给它的工作队列。

import (
	_ "embed"
	"log/slog"
	"sync"
	"time"
)

// wmRunOnWindowThread 是 Dispatch 投递给窗口的消息。它就是 WM_APP，
// go-webview2 用来唤醒自己派发队列的那个值：携带它的窗口消息干两件事——
// 运行本应用排入的工作，并让库把通过它排队的东西冲刷掉。
const wmRunOnWindowThread = 0x8000

// wndProc 把窗口藏起来而不是关掉它，这样点 X 之后托盘还在；它还运行
// Dispatch 排入的工作——这个过程就是窗口的线程，由当时正在泵消息的那个
// 循环抵达。其余消息都交给 go-webview2 安装的过程——包括
// WM_GETMINMAXINFO，下面那个最小尺寸正是靠它生效。
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

// Reload 请页面在它此刻所在的地址上重新加载自己。
//
// 那个地址是页面自己的，不是应用会导航过去的那个。游戏把全部状态都留在
// 页面里，所以刷新的意思是「再取一次这个页面」，不是「从应用记住的
// 什么东西重建地址」。
//
//go:embed js/reload.js
var reloadJS string

func (win *webviewWindow) Reload() {
	win.Dispatch(win.w.Eval, reloadJS)
}

// askForRoomJS 是加入菜单项弹出的提示；运行它的处理器在 main.go 里。
// 页面是这个程序唯一能提问的地方：没有控制台，而消息框没法把一行文字收回来。
//
//go:embed js/askForRoom.js
var askForRoomJS string

// fullscreenJS 把页面的全屏状态报告给 Go。游戏有自己的全屏按钮，而那个按钮
// 在 WebView2 里的行为本身还不够——见 fullscreen.go。
//
//go:embed js/fullscreen.js
var fullscreenJS string

// Bind 把一个 Go 函数以 window.<name> 的形式发布给页面。它必须在 Run 之前
// 注册：它是要为每个即将创建的文档执行的脚本，而已经打开的那个文档不算
// 其中之一。这个程序唯一的绑定，就是页面把加入提示里输入的内容交回来
// （见 askForRoom）。
func (win *webviewWindow) Bind(name string, fn any) error { return win.w.Bind(name, fn) }

// mutePageOn 与 mutePageOff 是窗口消失和回来时 Go 对页面说的话。
// 它们特意写在定义 _mutePage 的脚本旁边：只改了一边而没改另一边，
// 在任何地方都不会报错，只是声音会一直响下去——或者再也回不来。
const (
	mutePageOn  = "window._mutePage && window._mutePage(true)"
	mutePageOff = "window._mutePage && window._mutePage(false)"
)

//go:embed js/mute.js
var muteJS string

// setMuted 让页面的声音停下，或者让它回来。
//
// 关闭按钮把窗口藏起来，而藏起来的窗口仍然是一个可见的**页面**：只有当窗口
// 最小化时 WebView2 才会告诉页面并非如此，这就是为什么最小化游戏会安静下来，
// 而关闭它不会。页面对此有自己的处理（public/js/audio.js 会在
// visibilitychange 时挂起它的 AudioContext）；它从我们这里永远得不到的，
// 就是说明这件事的那个事件。这是通往同一处的另一条路：注入的脚本持有页面
// 创建的每一个 AudioContext，并挂起或恢复它们。
//
// 用 Eval 而不是绑定：不会有什么回来，页面也无从置喙。两个调用方——
// 窗口过程和 show——已经在窗口的线程上，而 WebView2 调用正该在那里。
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

// 等待窗口线程的工作，以及守护它的锁。
var (
	uiMu    sync.Mutex
	uiQueue []uiWork
)

// uiWork 是一次排队的调用以及它入队的时刻，好让跑晚了的调用能说出
// 自己晚了多久。
type uiWork struct {
	at time.Time
	f  func()
}

// Dispatch 在拥有窗口的那个线程上运行 f：消息循环和 WebView2 控制器
// 都住在那个线程上。菜单的 goroutine 不是那个线程，从其中任一
// goroutine 发出的控制器调用什么也做不了。
//
// 它把消息投递给*窗口*，而不是投递给线程（库自己的 Dispatch 是后者）。
// 线程消息不属于任何窗口，所以模态循环——菜单、消息框——会取走它，
// 而 DispatchMessageW 没有可投递的对象；其后排队的所有东西都要等下一件
// 事情投递消息才会动。规则就是这么一行。
//
// 调用和它的参数分开传递，而不是包进闭包，这样将要运行的调用写在调用点，
// 而不是藏在闭包体里——也让参数能经受编译器针对它所属函数的类型检查。
// 不是单个调用的函数体，就作为一个接收未用参数的闭包传入，window_test.go
// 就是这么做的。
func (win *webviewWindow) Dispatch[T any](f func(T), t T) {
	uiMu.Lock()
	uiQueue = append(uiQueue, uiWork{at: time.Now(), f: func() { f(t) }})
	uiMu.Unlock()
	procPostMessageW.Call(win.hwnd, wmRunOnWindowThread, 0, 0)
}

// runOnWindowThread 运行 Dispatch 排入的工作。它从窗口过程被调用，而窗口
// 过程按定义就是窗口的线程——库的循环或菜单的模态循环，当时哪个在泵
// 消息就是哪个。
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
