package main

import "testing"

// 尺寸下限是「页面自己想多大」，不是屏幕的一个比例。下面这组参数是菜单能组合出来的最窄一种：
// 在 2560x1368 上，屏幕高度 + 1/3 + 9:16 算出来只有 256 设备像素宽，比页面能用得上的还窄。
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

// 反过来那一半：普通尺寸不该被下限碰到。这是第一次运行打开时用的那一组——屏幕宽度的 3/4、
// 16:9，在 2560x1368 上是 1920x1080——而它必须能活着穿过下限，因为第一次运行没有别的东西可退。
func TestSizeFloorLeavesOrdinarySizesAlone(t *testing.T) {
	const screenW, screenH = 2560, 1368
	ordinary := sizeState{anchor: screenWidth, share: share{3, 4}, ratio: ratio{16, 9}}
	wantW, wantH := ordinary.shapedSize(screenW, screenH)
	if w, h := ordinary.windowSize(screenW, screenH); w != wantW || h != wantH {
		t.Fatalf("默认尺寸被下限改动了：%dx%d，应当是 %dx%d", w, h, wantW, wantH)
	}
}

// 三组设置现在要穿过 window-state.json 走一遭，所以菜单提供的每一个取值都必须原样回来——
// 少一个，下一次运行就会在错的那一项上打勾。
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

// 菜单不提供的取值——来自更新的版本，或者来自一个被人手改过的文件——必须被拒掉：一个哪一项都
// 对不上的设置，会让那三项一个勾都没有。
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
