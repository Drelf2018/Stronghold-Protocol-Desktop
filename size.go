//go:build windows

package main

// 参考尺寸 / 相对占比 / 宽高比 这三个菜单所描述的尺寸，以及把这三者
// 换算成给定屏幕上的窗口大小的算术。
//
// 它与驱动它的菜单（main.go）和它为其定尺寸的窗口（window.go）分开放置，
// 因为它只是纯粹的算术：不涉及 Win32、不涉及 WebView2，也不需要有什么
// 正在运行才能保证它正确。

import (
	"fmt"
	"slices"
	"sync"
)

// titled 是一种能用来填充菜单的值：它知道自己被画成什么标签。
// comparable 让它所属的那一组能找出被选中的条目。
type titled interface {
	comparable
	title() string
}

// anchor 是窗口所跟随的屏幕边：相对占比作用在这条边上，
// 宽高比则从它推出另一条边。
type anchor int

const (
	screenWidth anchor = iota
	screenHeight
)

func (a anchor) title() string {
	switch a {
	case screenWidth:
		return "屏幕宽度"
	case screenHeight:
		return "屏幕高度"
	default:
		return "未知参考"
	}
}

// share 是窗口占参考边的比例，以精确分数 p/q 保存：窗口大小由这两个数
// 算出，绝不来自标签。标签只补上 p/q 四舍五入得到的整数百分比。
type share struct {
	numerator   int
	denominator int
}

// title 是菜单绘制时用的标签。分数与百分比之间的制表符不是装饰：
// Windows 会把制表符后面的内容交给菜单的快捷键列并右对齐，这是在比例字体里
// 把它们对齐的唯一办法。用空格补不出来——一个数字宽 7px，一个空格 4px——
// 也没有哪个填充字符能在各种字体和字号下都恰好占一个数字宽。
func (s share) title() string {
	return fmt.Sprintf("%s\t(%d%%)", s.fraction(), s.percent())
}

// fraction 是写出来的精确比例，「5/8」，整条边则只是「1」。
func (s share) fraction() string {
	if s.denominator == 1 {
		return fmt.Sprintf("%d", s.numerator)
	}
	return fmt.Sprintf("%d/%d", s.numerator, s.denominator)
}

// percent 是 p/q 四舍五入到最接近的整数百分比。
func (s share) percent() int {
	return (s.numerator*100 + s.denominator/2) / s.denominator
}

// ratio 是窗口的宽与高之比，以精确的数对保存，宽高比正是由它算出的。
type ratio struct {
	width  int
	height int
}

func (r ratio) title() string {
	return fmt.Sprintf("%d: %d\t(%d%%)", r.width, r.height, r.percent())
}

// percent 是 p/q 四舍五入到最接近的整数百分比。
func (r ratio) percent() int {
	return (r.width*100 + r.height/2) / r.height
}

// 三个菜单的条目，自上而下。相对占比从最小的开始，到 1 结束；
// 每个分数都已经约分。
var (
	anchors = []anchor{screenWidth, screenHeight}
	shares  = []share{
		{1, 3},
		{7, 16},
		{1, 2},
		{9, 16},
		{5, 8},
		{2, 3},
		{3, 4},
		{5, 6},
		{7, 8},
		{1, 1},
	}
	ratios = []ratio{
		{9, 16},
		{2, 3},
		{3, 4},
		{1, 1},
		{4, 3},
		{3, 2},
		{16, 9},
	}
)

// sizeState 是这三个菜单合起来的意思：相对占比作用在哪条屏幕边、
// 窗口占这条边的多少，以及另一条边的形状。
type sizeState struct {
	anchor anchor
	share  share
	ratio  ratio
}

// sizeOnDisk 是 sizeState 在 window-state.json 里的形态：字段导出，
// 好让编码器看得见，分数则是成对的数字。sizeState 本身全部未导出，
// 原样是写不出去的。
type sizeOnDisk struct {
	Anchor int    `json:"anchor"` // 0 是屏幕宽度，1 是屏幕高度
	Share  [2]int `json:"share"`  // 分子、分母
	Ratio  [2]int `json:"ratio"`  // 宽、高
}

// onDisk 是写下去时用的形式。
func (s sizeState) onDisk() sizeOnDisk {
	index := 0
	for i, a := range anchors {
		if a == s.anchor {
			index = i
		}
	}
	return sizeOnDisk{
		Anchor: index,
		Share:  [2]int{s.share.numerator, s.share.denominator},
		Ratio:  [2]int{s.ratio.width, s.ratio.height},
	}
}

// state 是读回来时用的形式，凡是菜单不提供的值它都答 false。
//
// 这个检查比看上去更重要：不在 anchors、shares 或 ratios 里的值会让菜单
// 得到一个勾不上的条目，于是窗口打开时三项设置没有一项显示为已选中。
// 由更晚的版本写出、或被手工改过的文件，则退回调用方的默认值。
func (d sizeOnDisk) state() (sizeState, bool) {
	if d.Anchor < 0 || d.Anchor >= len(anchors) {
		return sizeState{}, false
	}
	s := sizeState{
		anchor: anchors[d.Anchor],
		share:  share{d.Share[0], d.Share[1]},
		ratio:  ratio{d.Ratio[0], d.Ratio[1]},
	}
	if !slices.Contains(shares, s.share) || !slices.Contains(ratios, s.ratio) {
		return sizeState{}, false
	}
	return s, true
}

// 这些菜单能算出的最小可见尺寸，以 CSS 像素表示：这是页面要求的下限，
// 不是屏幕的某个比例。
//
// 低于它，聊天界面就会横向滚动，再矮一点连输入框都看不见；菜单本身
// 可以组合出更小的数字——屏幕高度 + 1/3 + 9:16 在 2560x1368 上只留下
// 256x456 可见，比这个还窄。所以下限在这里夹住，而不是指望用户不去
// 选那种组合。
const (
	minVisibleWidth  = 480
	minVisibleHeight = 360
)

// smallIconSize 是通知区域绘制所用的边长：SM_CXSMICON，它跟随
// 显示缩放（100% 时 16，125% 时 20，150% 时 24）。
//
// 它有两处用途：托盘图标，以及窗口自己的图标（标题栏、任务栏、
// Alt+Tab）。两者都按最终绘制的尺寸画，因为 Windows 不会把交给它的东西放大。
//
// 它放在这里而不是 internal/artwork，因为它是绘制中唯一向 Windows
// 提问的部分，而那个包刻意保持平台无关——必须如此，因为发布流程
// 在 Linux 上运行它的生成器。
func smallIconSize() int {
	if got, _, _ := procGetSystemMetrics.Call(smCXSmallIcon); got > 0 {
		return int(got)
	}
	return 16
}

// displayScale 是显示缩放系数，从 SM_CXSMICON 读出：该指标在 96 dpi 时
// 是 16 像素，之后随缩放变化（150% 时 24）。图标代码用的已经是同一个指标。
func displayScale() int {
	if got, _, _ := procGetSystemMetrics.Call(smCXSmallIcon); got > 0 {
		return max(int(got)/16, 1)
	}
	return 1
}

// minVisibleSize 是把那个下限换算成设备像素：页面按 CSS 像素布局，
// 所以缩放越大，同样的内容占用的设备像素就越多。
func minVisibleSize() (int, int) {
	scale := displayScale()
	return minVisibleWidth * scale, minVisibleHeight * scale
}

// windowSize 是这些设置在给定大小的屏幕上描述的可见边框——用户看到的
// 那个矩形，不是客户区，也不是窗口矩形。参考边分得屏幕的相应比例；
// 宽高比给出另一条边。
//
// 每条边各自夹到下限：宁可放弃比例，也不交出一个页面用不了的尺寸。
func (s sizeState) windowSize(screenW, screenH int) (int, int) {
	width, height := s.shapedSize(screenW, screenH)
	minW, minH := minVisibleSize()
	return max(width, minW), max(height, minH)
}

// shapedSize 是三个菜单描述的尺寸，尚未应用下限。
func (s sizeState) shapedSize(screenW, screenH int) (int, int) {
	if s.anchor == screenHeight {
		height := screenH * s.share.numerator / s.share.denominator
		return height * s.ratio.width / s.ratio.height, height
	}
	width := screenW * s.share.numerator / s.share.denominator
	return width, width * s.ratio.height / s.ratio.width
}

var (
	sizeMu sync.Mutex
	size   sizeState
)

// currentSize 是菜单当前描述的设置。
func currentSize() sizeState {
	sizeMu.Lock()
	defer sizeMu.Unlock()
	return size
}

// storeSize 只记录设置，不碰窗口。启动时用它把菜单打开时的默认值交给菜单，
// 好让当前条目被勾上，之后点击时也有真实的数字可用；
// loadAndStoreSize 才是同时调整窗口大小的那个。
func storeSize(s sizeState) {
	sizeMu.Lock()
	defer sizeMu.Unlock()
	size = s
}

// setSize 保存新的设置，然后把窗口调整到匹配的大小。
func loadAndStoreSize(fn func(*sizeState)) {
	sizeMu.Lock()
	defer sizeMu.Unlock()
	if fn != nil {
		fn(&size)
	}
	width, height := size.windowSize(displaySize())
	win.Dispatch(win.placeVisible, [2]int{width, height})
}

func setAnchor(a anchor) { loadAndStoreSize(func(s *sizeState) { s.anchor = a }) }
func setShare(v share)   { loadAndStoreSize(func(s *sizeState) { s.share = v }) }
func setRatio(r ratio)   { loadAndStoreSize(func(s *sizeState) { s.ratio = r }) }
