//go:build windows

package main

// 选一个文件夹：用系统的那个选择框。
//
// 这是「提取本地客户端素材」缺最后一块拼图时的出路——客户端不在默认位置（装在别的盘、别的目录），
// 与其让人照着路径手打，不如把这个活儿交给资源管理器。
//
// 为什么是 SHBrowseForFolder 而不是新的 IFileDialog：老的这套只要三个 syscall，没有 COM 接口要
// 自己摆 vtable 的偏移；配上 BIF_NEWDIALOGSTYLE，它在 Win10/11 上一样能拖、能粘贴路径、能新建
// 文件夹。两害相权，取那个不会因为偏移写错而崩的。
//
// 一处必须记住的事：调用它的那条线程要先 CoInitializeEx（COM 初始化）。进程里唯一确定初始化过的
// 是窗口那条线程，而这是个弹窗，会在这儿停住——所以调用方在它自己的 goroutine 上初始化，见
// pickClientFolder。

import (
	"log/slog"
	"strings"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	// shell32 在 window.go 里已经声明过了（ShellExecuteW 用它），这里只补 ole32。
	ole32 = windows.NewLazySystemDLL("ole32.dll")

	procSHBrowseForFolderW  = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree       = ole32.NewProc("CoTaskMemFree")
	procCoInitializeEx      = ole32.NewProc("CoInitializeEx")
	procCoUninitialize      = ole32.NewProc("CoUninitialize")
)

// COINIT_APARTMENTTHREADED：这个弹窗要一条按 STA 初始化的线程。
const coinitApartmentThreaded = 0x2

// quote 是**一个**双引号。粘贴路径时常常连着它一起粘进来，而它不该进 --game。
//
// 写成一个而不是那一对，因为引号是按「前后各一个」剪的（见 normalizePickedFolder）：要剪的是两边
// 各一个，而不是「凡是引号都剪」。
const quote = string(rune(34))

const (
	// BIF_RETURNONLYFSDIRS：只让选真实的文件夹。这里要的是一个装了客户端的目录，不是"桌面"或者
	// 某个虚拟位置。
	bifReturnOnlyFSDirs = 0x0001
	// BIF_NEWDIALOGSTYLE：新的那套界面（可缩放、能粘贴路径、能新建文件夹）。少了它得到的是
	// Windows 95 那个样子的框。
	bifNewDialogStyle = 0x0040
)

// browseInfoW 是 BROWSEINFOW。字段与顺序要和 Windows 那边一致：它是一块被直接读写的内存。
type browseInfoW struct {
	owner       uintptr
	pidlRoot    uintptr
	displayName *uint16
	title       *uint16
	flags       uint32
	// callback 是 BFFM_INITIALIZED 时要走的那条路；initial 同时是它的 lParam——API 就是这么复用
	// 这两个字段的（apiVersion:lParam）。
	callback uintptr
	initial  *uint16
	image    int32
}

// 显示名那个缓冲要按 SHBrowseForFolderW 认的写法来：MAX_PATH 那么长。
const browseNameLength = 260

// chooseFolder 打开系统的文件夹选择框，回答选中的那个目录。取消时 ok 是 false（框根本没弹起来时
// 也一样）——这两种没有值得分开说的差别：无论哪种，都什么都没选。
//
// owner 必须是调用它的那扇窗。这不是可选的：没有属主的框不属于谁，Windows 不保证它压在我们窗口
// 上面——实测过，它会被自己的窗口盖住；而有了属主，它才成为这扇窗的**模态**框：一直压在上面，
// 属主被禁用，于是那个按钮也点不动。
func chooseFolder(owner uintptr, title, initial string) (string, bool) {
	// COM 要在这条线程上初始化过。已经初始化过（S_FALSE）也无所谓，下面照常走。
	r, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	const (
		sOK    = 0
		sFalse = 1
	)
	if r != sOK && r != sFalse {
		slog.Warn("folder picker: cannot initialise COM on this thread", "result", r)
		return "", false
	}
	if r == sOK {
		defer procCoUninitialize.Call()
	}

	display := make([]uint16, browseNameLength)
	info := browseInfoW{
		owner:       owner,
		displayName: &display[0],
		flags:       bifReturnOnlyFSDirs | bifNewDialogStyle,
	}
	if title != "" {
		if p, err := windows.UTF16PtrFromString(title); err == nil {
			info.title = p
		}
	}
	// 上次那个目录怎么用：**不用 pidlRoot**，用回调去"选中"它。
	//
	// pidlRoot 不是"初始位置"，而是**浏览范围的根**——设了它，用户就只能在这个范围里往下走，
	// 上不去了（实测：给定上次那个目录之后，盘符与上级目录都点不到了）。而这里要的是"开在附近，
	// 但随便改"。
	//
	// 回调的做法是 shell 的老规矩：框建好（BFFM_INITIALIZED）时，用 BFFM_SETSELECTION 把那条
	// 路径选上。选中只影响光标与滚动，不限制能去哪儿。
	//
	// pidlRoot 因此留 0：范围从"此电脑"开始，所有盘符都在。
	if initial != "" {
		if p, err := windows.UTF16PtrFromString(initial); err == nil {
			info.initial = p
			info.callback = windows.NewCallback(browseCallback)
		}
	}

	pidl, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&info)))
	if pidl == 0 {
		return "", false
	}
	defer procCoTaskMemFree.Call(pidl)

	if r, _, _ := procSHGetPathFromIDList.Call(pidl, uintptr(unsafe.Pointer(&display[0]))); r == 0 {
		return "", false
	}
	folder := windows.UTF16ToString(display)
	if folder == "" {
		return "", false
	}
	return folder, true
}

// browseCallback 是那个框的回调。它只做一件事：框刚建好时，把上次那个目录选上。
//
// BFFM_SETSELECTION 收一个路径字符串，而那个字符串从 lParam 传进来——browseInfoW.lparam 那个字段
// 在 API 里正是"给回调用的参数"（它同时兼着 apiVersion:lParam 的用途，见那边的注释）。
func browseCallback(hwnd, msg, lparam, data uintptr) uintptr {
	const (
		bffmInitialized  = 1
		bffmSetSelection = 0x0467
	)
	if msg == bffmInitialized && data != 0 {
		procSendMessageW.Call(hwnd, bffmSetSelection, 1, data)
	}
	return 0
}

// pickerBusy 记的是"那个框正开着"。
//
// 页面上那个按钮每点一下都会走到这里，而框会一直停住直到有人关掉它——所以第二次点击必须被挡下，
// 否则就是好几个框叠在一起。有了属主之后模态已经能挡住窗口上的点击，这一道是给别的入口（托盘那
// 两项）留的，也是给"万一模态没生效"留的。
var pickerBusy atomic.Bool

// pickClientFolder 是"问一个人客户端在哪"的整件事：那条要准备好的线程、那个框、以及记账。
//
// 重入闸放在这里而不是调用方：规矩跟着被保护的东西走，才不会漏掉某一个入口。
func pickClientFolder() (string, bool) {
	if !pickerBusy.CompareAndSwap(false, true) {
		slog.Info("folder picker: one is already open")
		return "", false
	}
	defer pickerBusy.Store(false)

	// 标题里第二行是给中文界面的一点提示：那个框自己不会说明它要的是哪个目录。
	const title = "选择游戏客户端素材目录\n\n... \\Arknights_Data\\StreamingAssets\\AB\\Windows"
	folder, ok := chooseFolder(pickOwner(), title, savedClientFolder())
	if !ok {
		return "", false
	}
	folder = normalizePickedFolder(folder)
	if folder == "" {
		return "", false
	}
	slog.Info("local assets: client folder chosen", "path", folder)
	return folder, true
}

// pickOwner 是那个框该属于谁：主窗口。取不到时给 0——那时行为退回到"没有属主"，也就是修之前的
// 样子，而不是什么都不做。
func pickOwner() uintptr {
	if win == nil {
		return 0
	}
	return win.hwnd
}

// normalizePickedFolder 把选中之后带回来的那串字符清理成能直接用的路径。
//
// 三件都见过：粘贴路径时带上一对引号、末尾多一个反斜杠（有的选择框就是这么给的）、以及正斜杠。
// 它们对 extract.py 未必有害，但会原样进日志、进 --game，让人以为路径不对。
func normalizePickedFolder(folder string) string {
	// 反斜杠写成 string(rune(92))，引号写成 string(rune(34))：这一段里全是引号与转义，写一个看得懂
	// 的东西比让读者去数斜杠强。
	sep := string(rune(92))
	folder = strings.TrimSpace(folder)
	// 引号只剪**成对**的那一对，不能用 strings.Trim(folder, sep+quote)：那个函数剪的是字符集，
	// 于是 `E:\` 末尾那个反斜杠也会被当成"要剪的字符"剪掉，变成 `E:`——那不是一条路径。
	if len(folder) >= 2 && strings.HasPrefix(folder, quote) && strings.HasSuffix(folder, quote) {
		folder = folder[1 : len(folder)-1]
	}
	folder = strings.ReplaceAll(folder, "/", sep)
	// 末尾多一个分隔符（有的选择框就是这么给的）剪掉，但盘根那一个要留着：`C:\Arknights\` 剪成
	// `C:\Arknights`，而 `E:\` 留着——剪掉它就成了 `E:`。
	for len(folder) > 3 && strings.HasSuffix(folder, sep) {
		folder = folder[:len(folder)-1]
	}
	return folder
}

// clientFolderLooksRight 粗略看一眼选来的是不是那个目录。
//
// 刻意宽松：真正认不认它由游戏自己的 extract.py 决定（它要的是 AssetBundle 根）。这里只拦下"选了
// 一个明显不相干的目录"这种一眼可见的错——比如桌面、或者客户端安装目录而不是 AB 根。
func clientFolderLooksRight(folder string) bool {
	if strings.TrimSpace(folder) == "" {
		return false
	}
	lower := strings.ToLower(filepathSlash(folder))
	return strings.HasSuffix(lower, "ab/windows") ||
		strings.HasSuffix(lower, "ab/playcover") ||
		strings.HasSuffix(lower, "bundles") ||
		strings.Contains(lower, "arknights")
}

// filepathSlash 让上面那几处后缀判断不必在意路径用的是哪种斜杠。
func filepathSlash(p string) string { return strings.ReplaceAll(p, "\\", "/") }
