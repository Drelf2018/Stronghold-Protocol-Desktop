package main

import (
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 系统菜单的命令 id 有两条规矩，破哪一条都会出问题，而出问题的表现离原因很远：
//
//  1. 低四位必须是 0：WM_SYSCOMMAND 拿它们当内部标记，收到时会抹掉，于是 0xF101 变成 0xF100，
//     两项就撞在一起了。
//  2. 要避开 0xF000–0xF180：那是系统自己的 SC_* 命令号。撞上 SC_RESTORE（0xF120）的后果非常
//     具体——窗口不是最大化时，系统会把「还原」置灰，而我们那一项跟着一起失效。
func TestSystemMenuIDs(t *testing.T) {
	ids := []struct {
		name string
		id   uintptr
	}{
		{"置顶", sysMenuTopMost},
		{"居中", sysMenuCenter},
		{"刷新", sysMenuRefresh},
		{"恢复参数尺寸", sysMenuRestoreSize},
	}
	for _, c := range ids {
		if c.id&0xF != 0 {
			t.Errorf("%s：id 0x%X 的低四位不是 0，抹掉之后会和别的项撞上", c.name, c.id)
		}
		if c.id >= 0xF000 && c.id <= 0xF180 {
			t.Errorf("%s：id 0x%X 落在系统的 SC_* 命令段里", c.name, c.id)
		}
	}
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			if a.id == b.id {
				t.Errorf("%s 和 %s 的 id 都是 0x%X，两项必须互不相同", a.name, b.name, a.id)
			}
		}
	}
}

// systemMenuForTest 造一个带系统菜单的窗口，把两者都交回来。它建了就扔，从不显示——
// measureFrameEdges 看边框用的是同一个办法。
func systemMenuForTest(t *testing.T) (uintptr, uintptr) {
	t.Helper()
	instance, _, _ := procGetModuleHandleW.Call(0)
	name, err := windows.UTF16PtrFromString("systrayExampleSystemMenuTest")
	if err != nil {
		t.Fatalf("窗口类名：%v", err)
	}
	class := wndClassEx{
		wndProc:   procDefWindowProcW.Addr(),
		instance:  instance,
		className: name,
	}
	class.size = uint32(unsafe.Sizeof(class))
	// 同一个名字注册第二次会失败，这没关系：算数的是第一次那个。
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class)))

	hwnd, _, _ := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(name)), 0, wsOverlappedWindow,
		0, 0, 200, 200, 0, 0, instance, 0,
	)
	if hwnd == 0 {
		t.Fatal("建不出一个带系统菜单的窗口")
	}
	t.Cleanup(func() { procDestroyWindow.Call(hwnd) })

	menu, _, _ := procGetSystemMenu.Call(hwnd, 0)
	if menu == 0 {
		t.Fatal("这个窗口没有系统菜单")
	}
	return menu, hwnd
}

// 菜单真正被装出来的样子，装在一个真实窗口的真实系统菜单上。它钉住的是人看到的那副形状：
// 最上面是本程序那三项窗口操作，然后是 Windows 自己的，再接着是本程序的尺寸设置，最后是系统
// 那一条分隔线和「关闭」。
//
// 这是一条测试而不是注释，因为那副形状是算术。尺寸那一组该插在哪里是从菜单本身推出来的，而推
// 错了不会响亮地失败——它只会把那一组放进菜单的错误一半，比如夹在「最小化」和「最大化」之间，
// 而菜单只是看着有点怪。
func TestSystemMenuLayout(t *testing.T) {
	menu, hwnd := systemMenuForTest(t)

	// installSystemMenu 会去要菜单所描述的那几组尺寸。
	before := currentSize()
	t.Cleanup(func() { storeSize(before) })
	storeSize(sizeState{})

	installSystemMenu(&webviewWindow{hwnd: hwnd})

	count := menuItemCount(menu)
	labels := make([]string, count)
	ids := make([]uintptr, count)
	for i := 0; i < count; i++ {
		labels[i] = strings.ReplaceAll(menuItemLabel(menu, uintptr(i)), "&", "")
		ids[i] = menuItemID(menu, uintptr(i))
	}
	t.Logf("system menu: %v", labels)

	// 上面三项是我们插的，顺序就是它们被读到的顺序。
	for i, want := range []string{"置顶(T)", "居中(Z)", "刷新(F)"} {
		if i >= count || labels[i] != want {
			t.Fatalf("第 %d 项是 %q，应当是 %q（整条菜单：%v）", i+1, labelAt(labels, i), want, labels)
		}
	}

	// 尺寸设置那一段：自己在一条分隔线后面，三组尺寸紧跟着它。
	restore := indexOf(labels, "恢复参数尺寸(H)")
	if restore < 4 {
		t.Fatalf("恢复参数尺寸在第 %d 位，应当在系统那一排之后（整条菜单：%v）", restore+1, labels)
	}
	if menuItemLabel(menu, uintptr(restore-1)) != "" {
		t.Errorf("恢复参数尺寸上面那一项不是分隔线（整条菜单：%v）", labels)
	}
	for i, want := range []string{"参考尺寸(K)", "相对占比(B)", "宽高比(G)"} {
		if got := labelAt(labels, restore+1+i); got != want {
			t.Errorf("恢复参数尺寸下面第 %d 项是 %q，应当是 %q", i+1, got, want)
		}
	}

	// 我们那一段之上、前三项之下，只能是系统自己的命令：这一段有我们任何一项，就说明
	// 插入位置算错了。
	for i := 3; i < restore-1; i++ {
		if ids[i] < 0xF000 || ids[i] > 0xF180 {
			t.Errorf("第 %d 项（%q，id 0x%X）不是系统自己的命令，夹在了系统项中间（整条菜单：%v）",
				i+1, labels[i], ids[i], labels)
		}
	}

	// 关闭 永远在最后，紧邻它上面的是分隔线：我们插在系统那条线之前，它仍然贴着关闭。
	if ids[count-1] != scClose {
		t.Errorf("最后一项的 id 是 0x%X，应当是 SC_CLOSE（整条菜单：%v）", ids[count-1], labels)
	}
	if menuItemLabel(menu, uintptr(count-2)) != "" {
		t.Errorf("关闭上面那一项不是分隔线（整条菜单：%v）", labels)
	}

	// 助记符不能重复。同一个菜单里两项共用一个字母时，那个字母就不再指向一处，按下去落到哪一项
	// 要看系统怎么解这个歧义——而这件事不会报错，只会让按键的表现变得说不清。系统那几项也一起
	// 算进来：用户眼里只有一张菜单，不是「我们的部分」和「系统的部分」。
	//
	// 这一条是有来历的：居中 一度用过 Center 的 C，和系统自带的中文「关闭(&C)」撞在一起，
	// 是靠人眼在菜单里看出来的。现在它会自己红。
	if letter, first, second, repeated := firstRepeatedAccelerator(labels); repeated {
		t.Errorf("助记符 %c 同时属于 %q 和 %q（整条菜单：%v）", letter, first, second, labels)
	}
}

// 上面那条检查兜底：一个永远不会红的检查等于没有检查。拿一份已知会撞的标签，确认它真能找到
// 撞在哪两项上——用真的菜单去验做不到这一点，因为真菜单修好了，而它一旦坏掉，别的断言会先红。
func TestRepeatedAcceleratorIsFound(t *testing.T) {
	letter, first, second, found := firstRepeatedAccelerator(
		[]string{"置顶(T)", "居中(C)", "", "还原(R)", "关闭(C)	Alt+F4"},
	)
	if !found {
		t.Fatal("两个 C 没有被发现")
	}
	if letter != 'C' || first != "居中(C)" || second != "关闭(C)	Alt+F4" {
		t.Errorf("报的是 %c / %q / %q，应当是 C / 居中(C) / 关闭(C)…", letter, first, second)
	}

	if _, _, _, found := firstRepeatedAccelerator([]string{"置顶(T)", "", "关闭(C)"}); found {
		t.Error("没有重复的菜单被报成了有重复")
	}
}

// acceleratorOf 读出某个标签应答的那个字母：Windows 会给它加下划线，而标签把它写在括号里。
// 分隔线、以及任何没有助记键的东西，都没有。
func acceleratorOf(label string) (rune, bool) {
	at := strings.Index(label, "(")
	if at < 0 {
		return 0, false
	}
	letters := []rune(label[at+1:])
	if len(letters) == 0 {
		return 0, false
	}
	return letters[0], true
}

// firstRepeatedAccelerator 报出第一个被两个标签共用的字母，附带那两个标签，按它们在菜单里出现
// 的先后。
func firstRepeatedAccelerator(labels []string) (rune, string, string, bool) {
	used := make(map[rune]string, len(labels))
	for _, label := range labels {
		letter, ok := acceleratorOf(label)
		if !ok {
			continue
		}
		if first, taken := used[letter]; taken {
			return letter, first, label, true
		}
		used[letter] = label
	}
	return 0, "", "", false
}

func indexOf(values []string, want string) int {
	for i, v := range values {
		if v == want {
			return i
		}
	}
	return -1
}

func labelAt(values []string, i int) string {
	if i < 0 || i >= len(values) {
		return "（没有这一项）"
	}
	return values[i]
}
