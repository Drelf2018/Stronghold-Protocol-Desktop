//go:build windows

package main

// The window's own menu: the system menu that comes up on a right click on the title bar (or
// Alt+Space), with this program's own items attached at the very top.
//
// Why here: whoever right clicks the title bar wants to adjust **this window**, whoever
// right clicks the tray wants to work **this program**. The window's size, position and
// topmost state travel with the window; the log, autostart and the address stay in the tray.
//
// The system menu rather than a hand-drawn TrackPopupMenu: the look, the shortcuts, the
// moment and the place it pops up are all the system's.

import (
	"log/slog"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The command ids, and neither rule may be broken:
//
//  1. **The low four bits must be 0.** WM_SYSCOMMAND uses them as an internal marker and
//     clears them before comparing, so 0xF101 becomes 0xF100 and two items collide.
//  2. **0xF000-0xF180 must be avoided.** Those are the system's own SC_* command numbers:
//     0xF100 is SC_KEYMENU, 0xF110 is SC_ARRANGE, 0xF120 is SC_RESTORE. Landing on
//     SC_RESTORE has a very concrete consequence - 还原 is greyed out by the system while
//     the window is not maximised, and the item of ours goes dead with it.
//
// Each of the three size groups gets a range of its own; item i within a group has the id
// base + i*0x10.
const (
	sysMenuCenter      = 0xF200
	sysMenuTopMost     = 0xF210
	sysMenuRestoreSize = 0xF220
	sysMenuRefresh     = 0xF230

	sysMenuAnchorBase = 0xF300
	sysMenuShareBase  = 0xF400
	sysMenuRatioBase  = 0xF500

	// SC_CLOSE, the command id Windows gives 关闭 in every system menu. It is how the place
	// this program's size settings go is found; see insertionBeforeClose.
	scClose = 0xF060
)

// sysMenu is the system menu's handle: the tick states are changed through it.
var sysMenu uintptr

// The tick sync for each group: after an item is picked, that group's ticks are laid out
// again.
var sysTicks struct {
	anchor func(anchor)
	share  func(share)
	ratio  func(ratio)
}

// menuItemCount is how many entries the menu holds right now.
func menuItemCount(menu uintptr) int {
	count, _, _ := procGetMenuItemCount.Call(menu)
	return int(count)
}

// menuItemID is the command id at one position in a menu.
func menuItemID(menu, position uintptr) uintptr {
	id, _, _ := procGetMenuItemID.Call(menu, position)
	return id
}

// menuItemLabel reads one entry's text back, ampersands and shortcut and all, exactly as
// Windows stores it. A separator has none, and that is how one is recognised here:
// GetMenuItemID cannot tell it, because a separator's id is whatever it was inserted with -
// this program's own go in with 0.
func menuItemLabel(menu, position uintptr) string {
	buf := make([]uint16, 128)
	n, _, _ := procGetMenuStringW.Call(
		menu, position, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), mfByPosition,
	)
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// insertionBeforeClose says where this program's size settings go, and whether a separator
// already stands between them and 关闭.
//
// 关闭 is found by its command id, SC_CLOSE, because that is Windows' own constant and does
// not move when items are inserted above it. Counting cannot do this job: a count says how
// many entries the menu holds, not where this program's own items have pushed the system's.
// The two agree only while the number of items inserted above stays the same - add one and
// the group lands in the wrong half of the menu, and the only symptom is a menu that looks a
// little odd. (That is not hypothetical: Windows 11's system menu holds no separator above
// 关闭, so the count the earlier code used put this group between 最小化 and 最大化.)
func insertionBeforeClose(menu uintptr) (at int, separator bool) {
	count := menuItemCount(menu)
	closeAt := count
	for i := 0; i < count; i++ {
		if menuItemID(menu, uintptr(i)) == scClose {
			closeAt = i
			break
		}
	}
	// 紧邻 关闭 上面那一格若是分隔线，我们插在它前面：那条线仍然贴着 关闭，它来当这一段的收尾。
	if closeAt > 0 && menuItemLabel(menu, uintptr(closeAt-1)) == "" {
		return closeAt - 1, true
	}
	return closeAt, false
}

// installSystemMenu puts this program's items into the system menu: the three window items at
// the very top, ahead of 还原 / 移动, and the size settings further down, between 最大化 and
// 关闭.
//
// InsertMenuW rather than AppendMenuW: only the former can insert at a given position, and
// the position argument has to carry MF_BYPOSITION, or Windows takes that number for a
// command id and goes looking for the item it names.
func installSystemMenu(win *webviewWindow) {
	menu, _, _ := procGetSystemMenu.Call(win.hwnd, 0)
	if menu == 0 {
		slog.Error("system menu: GetSystemMenu failed")
		return
	}
	sysMenu = menu

	// 位置由一个计数器推出来，不写死数字。插进一格会把后面每一项都往后挤一位，手写数字
	// 迟早漏掉一个——而漏掉不会报错，两项挤在同一位置上，菜单只是看起来少了一项。
	at := 0
	// 尺寸设置那一段之后要不要自己补一条分隔线，由 insertionBeforeClose 回答。
	separatorFollows := false
	insert := func(flags, id uintptr, text string) {
		insertSystemItem(menu, uintptr(at), flags, id, text)
		at++
	}
	// 标签里的 & 是 Win32 的助记符：Windows 不画那个 &，而是给后面这个字母加下划线，
	// Alt+空格 打开菜单后按它就选中。
	//
	// 上面三项的字母是这么来的：置顶取 Top 的 T，刷新取 F5 的 F，居中取第二个字 中 zhōng 的 Z。
	//
	// 居中本来有更顺手的两个字母：居 jū 的 J 空着，英文 Center 的 C 更贴题——C 不能用，它被
	// 系统自带的中文「关闭(&C)」占着，同一个菜单里两项共用一个字母时，那个字母就不再指向一处。
	// Z 是 置顶 改成 T 之后空出来的。
	//
	// 下面三组尺寸仍旧取**拼音**首字母：恢 H(huī)、考 K(kǎo)、比 B(bǐ)、高 G(gāo)——那几项
	// 用英文取不干净，Move/Size/Minimize/Maximize 把 M/S/N/X 都占着。
	//
	// 撞字母不会报错，只会让那个字母的表现变得说不清——所以往这一排添项之前，先看清系统那六个
	// 还原(R)/移动(M)/大小(S)/最小化(N)/最大化(X)/关闭(C) 占着哪些字母。
	//
	// 另外：标签里真要写一个 & 字符，得写两个（&&），否则 Windows 会把后面那个字符吃掉。
	// 这三项和系统的还原/移动排在一起，中间不隔线：它们调的是同一个东西——这个窗口。
	insert(mfString, sysMenuTopMost, "置顶(&T)")
	insert(mfString, sysMenuCenter, "居中(&Z)")
	insert(mfString, sysMenuRefresh, "刷新(&F)")

	// 尺寸设置落在最大化与关闭之间：系统在那两者之间有一条自己的分隔线，我们插在它前面，
	// 于是那条线成了这一段的收尾（下面不再自己加线）。
	//
	// 位置是**找**出来的，不是数出来的，见 insertionBeforeClose。
	at, separatorFollows = insertionBeforeClose(menu)
	insert(mfSeparator, 0, "")
	// 名字说的是它做的事：手动拖过之后，回到三组参数算出来的那个尺寸——loadAndStoreSize
	// 收到 nil 就是「参数不动，只按参数重摆一次」，而不是把参数重置回默认。
	insert(mfString, sysMenuRestoreSize, "恢复参数尺寸(&H)")

	if win.TopMost() {
		syncTopMostTick(true)
	}

	current := currentSize()
	sysTicks.anchor = insertGroup(menu, &at, "参考尺寸(&K)", anchors, current.anchor, sysMenuAnchorBase)
	sysTicks.share = insertGroup(menu, &at, "相对占比(&B)", shares, current.share, sysMenuShareBase)
	sysTicks.ratio = insertGroup(menu, &at, "宽高比(&G)", ratios, current.ratio, sysMenuRatioBase)

	// 关闭之前那条线：系统的菜单若自己有一条，上面那一段已经插在它前面，它接着当收尾；
	// 没有（Windows 11 的系统菜单只把 关闭 排在最后），就补一条，免得 关闭 和尺寸设置粘在一起。
	if !separatorFollows {
		insert(mfSeparator, 0, "")
	}
	slog.Info("system menu: window items inserted")
}

// insertGroup puts one settings group at the next position and moves the counter on, so the
// caller never has to count the items it has already added.
func insertGroup[T titled](menu uintptr, at *int, title string, values []T, current T, base uintptr) func(T) {
	tick := insertSystemGroup(menu, uintptr(*at), title, values, current, base)
	*at++
	return tick
}

// insertSystemItem inserts one item at the given position. A separator has neither an id nor
// a text, which is what those empty arguments mean; for a submenu the id argument becomes the
// submenu handle, which is how MF_POPUP is used.
func insertSystemItem(menu, position, flags, id uintptr, text string) {
	var label uintptr
	if text != "" {
		u, err := windows.UTF16PtrFromString(text)
		if err != nil {
			slog.Error("system menu: cannot build the label", "error", err)
			return
		}
		label = uintptr(unsafe.Pointer(u))
	}
	if ret, _, err := procInsertMenuW.Call(menu, position, flags|mfByPosition, id, label); ret == 0 {
		slog.Error("system menu: InsertMenu failed", "text", text, "position", position, "error", err)
	}
}

// insertSystemGroup turns a group of single-choice values into a submenu, and returns the
// function that puts that group's ticks right.
//
// A submenu in the system menu is no different from one in the tray, except that the ticks
// have to be drawn by hand: the Check/Uncheck pair belongs to the tray library, and
// CheckMenuItem is what is used here.
func insertSystemGroup[T titled](menu, position uintptr, title string, values []T, current T, base uintptr) func(T) {
	sub, _, _ := procCreatePopupMenu.Call()
	if sub == 0 {
		slog.Error("system menu: CreatePopupMenu failed")
		return func(T) {}
	}
	tick := func(v T) {
		for i, value := range values {
			mark := uintptr(mfUnchecked)
			if value == v {
				mark = mfChecked
			}
			procCheckMenuItem.Call(sub, base+uintptr(i)*0x10, mark)
		}
	}
	for i, value := range values {
		insertSystemItem(sub, uintptr(i), mfString, base+uintptr(i)*0x10, value.title())
	}
	tick(current)
	insertSystemItem(menu, position, mfPopup, sub, title)
	return tick
}

// handleSystemCommand carries out the commands we attached ourselves, and says whether this
// one was ours. When it was not it goes to Windows, which owns Close, Move and Minimise.
func handleSystemCommand(command uintptr) bool {
	// 全屏的窗口没有标题栏，这些菜单项本来就够不到；但别的入口仍可能把它们叫起来，所以先把窗口从
	// 全屏里放出来，再按菜单的意思去动它——否则会得到一个没有边框、又不再铺满的窗口。
	if fullScreenStyle != 0 {
		win.fullScreen(false)
	}
	switch command {
	case sysMenuCenter:
		win.Center()
	case sysMenuRefresh:
		win.Reload()
	case sysMenuTopMost:
		syncTopMostTick(win.ToggleTopMost())
	case sysMenuRestoreSize:
		loadAndStoreSize(nil)
	default:
		if v, ok := menuValue(anchors, sysMenuAnchorBase, command); ok {
			setAnchor(v)
			sysTicks.anchor(v)
			return true
		}
		if v, ok := menuValue(shares, sysMenuShareBase, command); ok {
			setShare(v)
			sysTicks.share(v)
			return true
		}
		if v, ok := menuValue(ratios, sysMenuRatioBase, command); ok {
			setRatio(v)
			sysTicks.ratio(v)
			return true
		}
		return false
	}
	return true
}

// menuValue turns a command id back into the value it stands for: id = base + index*0x10, and
// an index out of range means it is not this group.
func menuValue[T comparable](values []T, base, command uintptr) (T, bool) {
	var zero T
	if command < base {
		return zero, false
	}
	i := int((command - base) / 0x10)
	if i >= len(values) || base+uintptr(i)*0x10 != command {
		return zero, false
	}
	return values[i], true
}

// syncTopMostTick keeps the tick honest: of these items it is the only one that is a state
// rather than an action.
func syncTopMostTick(on bool) {
	if sysMenu == 0 {
		return
	}
	tick := uintptr(mfUnchecked)
	if on {
		tick = mfChecked
	}
	procCheckMenuItem.Call(sysMenu, sysMenuTopMost, tick)
}
