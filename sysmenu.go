//go:build windows

package main

// 窗口自己的菜单：右键标题栏（或按 Alt+空格）弹出的那个系统菜单，本程序自己的项挂在最上面。
//
// 为什么放这儿：右键标题栏的人要调的是**这个窗口**，右键托盘的人要弄的是**这个程序**。
// 窗口的尺寸、位置与置顶状态跟着窗口走；日志、自启和地址留在托盘里。
//
// 用系统菜单，不用手画的 TrackPopupMenu：外观、快捷键、弹出的时机与位置，
// 全都是系统的。

import (
	"log/slog"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 命令 id，两条规则一条都不能破：
//
//  1. **低四位必须是 0。** WM_SYSCOMMAND 拿它们当内部标记，比较前会清掉，于是 0xF101
//     变成 0xF100，两项就撞在一起了。
//  2. **要避开 0xF000-0xF180。** 那是系统自己的 SC_* 命令号：0xF100 是 SC_KEYMENU，
//     0xF110 是 SC_ARRANGE，0xF120 是 SC_RESTORE。落在 SC_RESTORE 上后果很具体——
//     窗口没有最大化时系统会把 还原 置灰，我们那一项也跟着一起失效。
//
// 三组尺寸各自占一段范围；组内第 i 项的 id 是 base + i*0x10。
const (
	sysMenuCenter      = 0xF200
	sysMenuTopMost     = 0xF210
	sysMenuRestoreSize = 0xF220
	sysMenuRefresh     = 0xF230

	sysMenuAnchorBase = 0xF300
	sysMenuShareBase  = 0xF400
	sysMenuRatioBase  = 0xF500

	// SC_CLOSE，Windows 在每个系统菜单里给 关闭 的命令 id。本程序尺寸设置该去的位置就是
	// 靠它找出来的；见 insertionBeforeClose。
	scClose = 0xF060
)

// sysMenu 是系统菜单的句柄：勾选状态都通过它改。
var sysMenu uintptr

// 每一组的勾选同步：某项被选中后，把那一组的勾选重新摆一遍。
var sysTicks struct {
	anchor func(anchor)
	share  func(share)
	ratio  func(ratio)
}

// menuItemCount 是菜单此刻有多少项。
func menuItemCount(menu uintptr) int {
	count, _, _ := procGetMenuItemCount.Call(menu)
	return int(count)
}

// menuItemID 是菜单里某个位置上的命令 id。
func menuItemID(menu, position uintptr) uintptr {
	id, _, _ := procGetMenuItemID.Call(menu, position)
	return id
}

// menuItemLabel 把某一项的文本原样读回来，连 & 和快捷键一起，跟 Windows 存的一模一样。
// 分隔线没有文本，这里就是靠这一点认出它的：GetMenuItemID 分不出来，因为分隔线的 id 就是
// 插入时给的任何东西——本程序自己的都拿 0 插进去。
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

// insertionBeforeClose 说明本程序的尺寸设置该去哪，以及它们和 关闭 之间是不是已经有一条
// 分隔线。
//
// 关闭 靠自己的命令 id SC_CLOSE 找，因为那是 Windows 自己的常量，在它上面插项也不会挪。
// 数数干不了这活：数量只说菜单有多少项，说不出本程序自己的项把系统的项挤到了哪儿。两者只在
// 上面插入的项数不变时才一致——多插一项，这一段就落到菜单的错误半边，唯一的症状是菜单看
// 起来有点怪。（这不是假设：Windows 11 的系统菜单在 关闭 上面没有分隔线，于是早先的代码
// 用数量算，把这一段放到了 最小化 和 最大化 中间。）
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

// installSystemMenu 把本程序的项放进系统菜单：三项窗口项目在最上面，排在 还原 / 移动 之前，
// 尺寸设置则在下面，夹在 最大化 与 关闭 之间。
//
// 用 InsertMenuW 而不是 AppendMenuW：只有前者能在指定位置插入，而且位置参数必须带上
// MF_BYPOSITION，否则 Windows 会把这个数字当成命令 id，去找它指名的那个项。
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

// insertGroup 把一个设置组放在下一个位置上，并把计数器往后推，调用方永远不用去数自己已经
// 加了几项。
func insertGroup[T titled](menu uintptr, at *int, title string, values []T, current T, base uintptr) func(T) {
	tick := insertSystemGroup(menu, uintptr(*at), title, values, current, base)
	*at++
	return tick
}

// insertSystemItem 在给定位置插入一项。分隔线既没有 id 也没有文本，那几个空参数就是这个意
// 思；对子菜单来说 id 参数变成子菜单句柄，MF_POPUP 就是这么用的。
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

// insertSystemGroup 把一组单选值变成一个子菜单，并返回把那一组勾选摆正的那个函数。
//
// 系统菜单里的子菜单和托盘里的没什么不同，只是勾选得自己画：Check/Uncheck 那一对是托盘库
// 的，这里用的是 CheckMenuItem。
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

// handleSystemCommand 执行我们自己挂上去的命令，并说明这一条是不是我们的。不是的时候就交给
// Windows，Close、Move 和 Minimise 归它管。
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

// menuValue 把命令 id 变回它代表的值：id = base + index*0x10，索引越界就说明它不是这一组。
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

// syncTopMostTick 让勾选保持诚实：这些项里只有它是一个状态，而不是一个动作。
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
