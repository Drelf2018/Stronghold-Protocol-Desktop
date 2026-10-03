//go:build windows

package main

// The size the 参考尺寸 / 相对占比 / 宽高比 menus describe, and the arithmetic that turns
// those three into a window size on a screen of a given size.
//
// It is kept apart from the menu that drives it (main.go) and the window it sizes
// (window.go) because it is plain arithmetic: no Win32, no WebView2, nothing that has to
// be running for it to be right.

import (
	"fmt"
	"slices"
	"sync"
)

// titled is a value a menu can be filled with: it knows the label it is drawn as.
// comparable is what lets a group find the entry that was picked.
type titled interface {
	comparable
	title() string
}

// anchor is the screen edge the window follows: 相对占比 applies to this edge, and
// 宽高比 derives the other one from it.
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

// share is the part of the reference edge the window takes, kept as the exact
// fraction p/q: the window size is computed from those two numbers, never from the
// label. The label only adds the whole percentage p/q rounds to.
type share struct {
	numerator   int
	denominator int
}

// title is the label as the menu draws it. The tab between the fraction and the
// percentage is not decoration: Windows hands whatever follows a tab to the menu's
// accelerator column and right-aligns it, which is the only way to line these up in
// a proportional font. Padding cannot do it - a digit is 7px wide and a space 4px -
// and no filler character stays exactly one digit wide across fonts and sizes.
func (s share) title() string {
	return fmt.Sprintf("%s\t(%d%%)", s.fraction(), s.percent())
}

// fraction is the exact proportion written out, "5/8", or just "1" for the whole
// edge.
func (s share) fraction() string {
	if s.denominator == 1 {
		return fmt.Sprintf("%d", s.numerator)
	}
	return fmt.Sprintf("%d/%d", s.numerator, s.denominator)
}

// percent is p/q rounded to the nearest whole percent.
func (s share) percent() int {
	return (s.numerator*100 + s.denominator/2) / s.denominator
}

// ratio is the window's width against its height, kept as the exact pair 宽高比 is
// computed from.
type ratio struct {
	width  int
	height int
}

func (r ratio) title() string {
	return fmt.Sprintf("%d: %d\t(%d%%)", r.width, r.height, r.percent())
}

// percent is p/q rounded to the nearest whole percent.
func (r ratio) percent() int {
	return (r.width*100 + r.height/2) / r.height
}

// The entries of the three menus, top to bottom. 相对占比 runs smallest first and
// ends at 1; every fraction is already reduced.
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

// sizeState is what the three menus add up to: which screen edge the share applies
// to, how much of that edge the window takes, and the shape of the other edge.
type sizeState struct {
	anchor anchor
	share  share
	ratio  ratio
}

// sizeOnDisk is sizeState in the shape window-state.json keeps: exported fields, so the
// encoder can see them, and the fractions as pairs of numbers. sizeState itself is all
// unexported and cannot be written out as it stands.
type sizeOnDisk struct {
	Anchor int    `json:"anchor"` // 0 is screen width, 1 is screen height
	Share  [2]int `json:"share"`  // numerator, denominator
	Ratio  [2]int `json:"ratio"`  // width, height
}

// onDisk is the form to write down.
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

// state is the form to read back, and it answers false for anything the menus do not offer.
//
// The check matters more than it looks: a value that is not in anchors, shares or ratios
// gives the menus an entry they cannot tick, so the window would open with none of the three
// settings shown as chosen. A file written by a later version, or edited by hand, falls back
// to the caller's defaults instead.
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

// The smallest visible size the menus can work out to, said in CSS pixels: this is the floor
// the page wants, not a proportion of the screen.
//
// Below it the chat interface starts scrolling sideways, and any shorter and not even the
// input box is visible; the menus themselves can be combined into smaller numbers - 屏幕高度
// + 1/3 + 9:16 leaves only 256x456 visible on 2560x1368, narrower than this. So the floor is
// clamped here rather than trusting the user not to pick that combination.
const (
	minVisibleWidth  = 480
	minVisibleHeight = 360
)

// smallIconSize is the edge the notification area draws at: SM_CXSMICON, which follows the
// display scaling (16 at 100%, 20 at 125%, 24 at 150%).
//
// Two things come out of it: the tray icon, and the window's own icons (title bar, taskbar,
// Alt+Tab). Both are drawn at the size they will be drawn, because Windows does not scale up what
// it is handed.
//
// It lives here rather than in internal/artwork because it is the one part of the drawing that asks
// Windows something, and that package is deliberately platform-independent - it has to be, because
// the release workflow runs its generator on Linux.
func smallIconSize() int {
	if got, _, _ := procGetSystemMetrics.Call(smCXSmallIcon); got > 0 {
		return int(got)
	}
	return 16
}

// displayScale is the display scaling factor, read from SM_CXSMICON: that metric is 16 pixels
// at 96 dpi and follows the scaling after that (24 at 150%). The icon code already uses the
// same metric.
func displayScale() int {
	if got, _, _ := procGetSystemMetrics.Call(smCXSmallIcon); got > 0 {
		return max(int(got)/16, 1)
	}
	return 1
}

// minVisibleSize is that floor converted into device pixels: the page lays out in CSS pixels,
// so the more scaling there is, the more device pixels the same content takes.
func minVisibleSize() (int, int) {
	scale := displayScale()
	return minVisibleWidth * scale, minVisibleHeight * scale
}

// windowSize is the visible frame these settings describe on a screen of the given
// size — the rectangle the user sees, not the client area and not the window rect.
// The reference edge gets the share of the screen; 宽高比 gives the other edge.
//
// Each edge is clamped to the floor on its own: the ratio is given up rather than a size the
// page cannot use being handed out.
func (s sizeState) windowSize(screenW, screenH int) (int, int) {
	width, height := s.shapedSize(screenW, screenH)
	minW, minH := minVisibleSize()
	return max(width, minW), max(height, minH)
}

// shapedSize is the size the three menus describe, before the floor is applied.
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

// currentSize is the settings the menus currently describe.
func currentSize() sizeState {
	sizeMu.Lock()
	defer sizeMu.Unlock()
	return size
}

// storeSize records settings without touching the window. Startup uses it to hand the menus
// the defaults they open with, so that the current entry is ticked and a later click has
// real numbers to work from; loadAndStoreSize is the one that also resizes.
func storeSize(s sizeState) {
	sizeMu.Lock()
	defer sizeMu.Unlock()
	size = s
}

// setSize stores new settings, then resizes the window to match.
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
