package main

import "testing"

// The size floor is "how big the page wants to be", not a proportion of the screen. These
// settings are the narrowest the menus can be combined into: on 2560x1368, 屏幕高度 + 1/3 +
// 9:16 comes to 256 pixels wide, narrower than the page can use.
func TestSizeFloor(t *testing.T) {
	const screenW, screenH = 2560, 1368
	minW, minH := minVisibleSize()
	// 测试进程没有声明 DPI 感知（那是 main 的事），所以这里读到的缩放是 1、下限就是
	// 480x360；程序自己跑起来时同一个度量会给 24，下限跟着变成 720x540。下面的断言
	// 只比较关系，因此两种情况下都成立。
	t.Logf("这个进程读到的下限：%dx%d 设备像素（缩放 x%d）", minW, minH, displayScale())

	narrow := sizeState{anchor: screenHeight, share: share{1, 3}, ratio: ratio{9, 16}}
	if raw, _ := narrow.shapedSize(screenW, screenH); raw >= minW {
		t.Fatalf("这组参数本应算出比下限更窄的 %d，用例已经失效", raw)
	}
	if w, h := narrow.windowSize(screenW, screenH); w < minW || h < minH {
		t.Fatalf("夹取之后是 %dx%d，仍低于下限 %dx%d", w, h, minW, minH)
	}
}

// The other way round: ordinary sizes must not be touched by the floor. This is the set a first run
// opens with - 屏幕宽度的 3/4, 16:9, which on 2560x1368 comes to 1920x1080 - and it is the one that has
// to survive the floor, because a first run has nothing else to fall back on.
func TestSizeFloorLeavesOrdinarySizesAlone(t *testing.T) {
	const screenW, screenH = 2560, 1368
	ordinary := sizeState{anchor: screenWidth, share: share{3, 4}, ratio: ratio{16, 9}}
	wantW, wantH := ordinary.shapedSize(screenW, screenH)
	if w, h := ordinary.windowSize(screenW, screenH); w != wantW || h != wantH {
		t.Fatalf("默认尺寸被下限改动了：%dx%d，应当是 %dx%d", w, h, wantW, wantH)
	}
}

// The three settings now travel through window-state.json, so every value a menu offers has
// to come back unchanged - otherwise the next run opens with the wrong entry ticked.
func TestSizeStateSurvivesTheFile(t *testing.T) {
	for _, a := range anchors {
		for _, sh := range shares {
			for _, r := range ratios {
				want := sizeState{anchor: a, share: sh, ratio: r}
				got, ok := want.onDisk().state()
				if !ok || got != want {
					t.Fatalf("%+v came back as %+v (ok=%v)", want, got, ok)
				}
			}
		}
	}
}

// A value the menus do not offer - from a later version, or a hand-edited file - has to be
// refused, because a setting no entry matches would leave all three of them unticked.
func TestSizeStateRejectsUnknownValues(t *testing.T) {
	for _, d := range []sizeOnDisk{
		{Anchor: -1, Share: [2]int{9, 16}, Ratio: [2]int{4, 3}},
		{Anchor: 9, Share: [2]int{9, 16}, Ratio: [2]int{4, 3}},
		{Anchor: 0, Share: [2]int{9, 17}, Ratio: [2]int{4, 3}},
		{Anchor: 0, Share: [2]int{9, 16}, Ratio: [2]int{4, 5}},
	} {
		if _, ok := d.state(); ok {
			t.Errorf("%+v should have been refused", d)
		}
	}
}
