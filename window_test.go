package main

// 窗口线程那个队列：排进去的活儿一定会被执行。这是「点了 更新游戏 / 重新准备游戏文件 之后，窗口停在
// 『正在准备』那一页不动」那个毛病的一半——另一半是「用窗口消息唤醒，而不是线程消息」，那一条测不了：
// 它要有一个真的消息循环，而模态循环吃掉线程消息这件事只有真菜单才复现得出来。

import (
	"testing"
	"time"
)

func TestWindowThreadQueueRunsEverything(t *testing.T) {
	uiMu.Lock()
	uiQueue = nil
	uiMu.Unlock()
	t.Cleanup(func() {
		uiMu.Lock()
		uiQueue = nil
		uiMu.Unlock()
	})

	// hwnd 是 0，投递那一步必然失败——正合此意：这里测的是队列本身，投递要有真窗口才看得见。
	win := &webviewWindow{}
	ran := 0
	win.Dispatch(func(any) { ran++ }, nil)
	win.Dispatch(func(any) { ran++ }, nil)

	uiMu.Lock()
	queued := len(uiQueue)
	uiMu.Unlock()
	if queued != 2 {
		t.Fatalf("Dispatch 之后队列里有 %d 项，应当是 2 项", queued)
	}

	runOnWindowThread()
	if ran != 2 {
		t.Errorf("跑了 %d 项，应当是 2 项", ran)
	}
	uiMu.Lock()
	left := len(uiQueue)
	uiMu.Unlock()
	if left != 0 {
		t.Errorf("跑完之后队列里还剩 %d 项", left)
	}

	// 排了很久的那一项也要跑：迟到只留一条日志线索，不许被丢掉。
	uiMu.Lock()
	uiQueue = append(uiQueue, uiWork{at: time.Now().Add(-time.Minute), f: func() { ran++ }})
	uiMu.Unlock()
	runOnWindowThread()
	if ran != 3 {
		t.Errorf("迟到的那一项没有跑：一共跑了 %d 项，应当是 3 项", ran)
	}
}
