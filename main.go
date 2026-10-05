//go:build windows

// 启动器把托盘图标和 WebView2 窗口一起装在一个游戏外面：卫戍协议：盟约
// (Stronghold Protocol)，一个玩家自制的 1-4 人联机浏览器游戏，它的服务端是一个 Node.js
// 进程。窗口就是游戏——它打开的是这台机器上服务端自己的页面。
//
// 菜单按「它作用于什么」分成两半。窗口管游戏自己的那几项：置顶、居中、刷新与
// 恢复参数尺寸，都在标题栏的系统菜单里。托盘管程序自己的那几项：显示窗口、
// 开机自启动、打开与更多两个子菜单，以及排在最后的退出。
//
// 窗口一创建就显示，而关闭它只是把它藏起来，不是退出：托盘还在，显示窗口还能把它叫
// 回来，直到有人选了退出。窗口尺寸会跨运行记住——位置不会，因为每次运行都从居中开始。
//
// 编译时带上 -ldflags=-H=windowsgui：控制台子系统会在它旁边再放一个控制台窗口，
// 而它任务栏按钮上挂的是控制台的图标。构建的两步在 README.md 里。
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Drelf2018/Stronghold-Protocol-Launcher/internal/artwork"
	"github.com/Drelf2018/systray"
	"golang.org/x/sys/windows"
)

// 这个程序给自己两个名字，而它们的区别不是随手起的：**进程**是启动器，**窗口**是游戏。
//
// appName 是窗口的名字。窗口里装的是游戏："正在准备「卫戍协议：盟约」"、"游戏服务没能启动"——那
// 些话说的都是游戏，窗口也就是游戏。所以标题栏写游戏名，启动完之后它是什么，就写什么。
//
// launcherName 是那个 exe 的名字。报错弹窗是这个程序没能起来（窗口还没有呢），托盘里躺着的是这个
// 程序（它启动完也还在），所以那两处说的是启动器——那三个字在那里不是多余的。
//
// appID 是机器读的东西，也是这个程序归档的依据：%LOCALAPPDATA% 下的那个文件夹
// （游戏的源码也存在那里），以及自启动项的名字。它是 ASCII、小写，而且**永远**不能变：
// 它一变的当天，所有用户存下的窗口尺寸、浏览器配置和下载好的游戏都会留在旧名字底下。
// 不要让它从 appName 推出来——两者可以各自不同，事实上，它们也确实不同。
const (
	appName = "卫戍协议：盟约"

	// launcherName 是 exe 自己的名字，用在它的两个"身份"场合：起不来时的报错弹窗，以及托盘的
	// 悬停提示。窗口标题用的是 appName——那扇窗里装的是游戏。
	//
	// 托盘那一处还跟着版本号（见 trayName）：报错弹窗说的是"这个东西没能起来"，而托盘说的是"现在
	// 跑着的这一份是哪一版"——后一句话才有人会问。
	launcherName = "卫戍协议：盟约 启动器"
	appID        = "stronghold-protocol-launcher"
)

// Version 是构建的版本，也是这个程序里唯一会被链接器改写的东西：发布流程构建时带上
//
//	-ldflags "-H=windowsgui -X main.Version=${{ github.ref_name }}"
//
// 所以发布版带着它发布自的那个 tag，本地构建则停在 "dev"。
//
// 它必须保持是一个普通字符串变量：-X 只够得到一个初始化式是常量表达式的变量，而对运行时
// 算出来的变量什么都不做——而且是悄悄地什么都不做。
//
// 菜单里没有任何一项显示它，但本地构建确实会多出一个子菜单，所以这个值在两个地方都挣得了
// 它的位置。它会在启动时进日志，而它回答的那个问题（「这是哪一版」）本来就是在那里问的。
var Version = "dev"

// trayName 是托盘图标的悬停提示：程序名加上这一份的版本。
//
// 版本号在这里第一次被人看见——此前它只进日志。而"你装的是哪一版"正是排障时第一个被问的问题，问的
// 时候人正指着托盘。发布版拿到的是 tag（v0.3.0），自己编的那一份是 dev，两者都照原样写上去。
//
// 版本为空时只写名字，不留一个孤零零的分隔符。
func trayName(version string) string {
	if version == "" {
		return launcherName
	}
	return launcherName + " - " + version
}

// win 是主窗口，在 main 里设置一次，之后永不为 nil：WebView2 运行时缺失，或者窗口
// 建不出来时，newWindow 返回 nil，而 main 就停在那里，而不是守着一个身后什么都没有的
// 托盘继续跑。
var win *webviewWindow

// report 把一条消息摆到人面前。在有窗口之前，这是这个程序唯一能说话的方式：带上
// -H=windowsgui 就没有控制台可以打印。
func report(cause string) {
	if err := showError(cause); err != nil {
		slog.Error("show", "error", err)
	}
}

// parseArgs 读这个程序认的开关。
//
// -data-dir 在这里，而且比别的都先读，因为它决定这个程序写下的第一个文件去往哪儿。
// -no-game 在它被用到的地方说明。
func parseArgs() {
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-no-game" || arg == "--no-game":
			noGame = true
		case arg == "-data-dir" || arg == "--data-dir":
			if i+1 < len(args) {
				i++
				dataDirOverride = args[i]
			}
		case strings.HasPrefix(arg, "-data-dir="):
			dataDirOverride = strings.TrimPrefix(arg, "-data-dir=")
		case strings.HasPrefix(arg, "--data-dir="):
			dataDirOverride = strings.TrimPrefix(arg, "--data-dir=")
		}
	}
	// 相对路径按启动时的工作目录算，算完就固定下来：WebView2 拿到的是这个字符串，而它不认
	// 「相对于谁」这种问题
	if dataDirOverride != "" {
		if abs, err := filepath.Abs(dataDirOverride); err == nil {
			dataDirOverride = abs
		}
	}
}

// createWindow 建出窗口，并把那个不是错误的失败变成一个错误。
//
// WebView2 建不出环境时——没装运行时，或者给它一个它建不出的用户数据文件夹——它的完成
// handler 会被以一个 nil controller 调起，而 go-webview2 把它解引用了。那次 panic 发生在
// 这个 goroutine 上，正在 NewWithOptions 返回的路上，所以它在这里被抓住，而不是一路走到运行
// 时那里，把进程一起带走：没有这个，程序会一声不响地消失，而一声不响之外，人没有别的可依凭。
func createWindow(state windowState) (w *webviewWindow) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("creating the window panicked", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			w = nil
		}
	}()
	return newWindow(state)
}

func showError(cause string) error {
	title, err := windows.UTF16PtrFromString(launcherName)
	if err != nil {
		return err
	}
	body, err := windows.UTF16PtrFromString(cause)
	if err != nil {
		return err
	}
	_, err = windows.MessageBox(0, body, title, windows.MB_OK|windows.MB_ICONERROR|windows.MB_TASKMODAL|windows.MB_SETFOREGROUND)
	return err
}

// 窗口正在显示的地址。托盘菜单的那些 goroutine 读它，页面的绑定写它，而两者最终都落在跑消息循
// 环的那个线程上。
var (
	urlMu      sync.RWMutex
	currentURL = gameAddress
)

// url 是窗口正在显示的地址，或者即将显示的。
func url() string {
	urlMu.RLock()
	defer urlMu.RUnlock()
	return currentURL
}

// address 把输入的东西变成一个窗口打得开的地址。
//
// 输入进去的通常是一个主机名，可能带端口，前面什么都没有——而 WebView2 会把它读成一次搜
// 索或者一个文件路径，而不是一个站点。已经带着 scheme 的原样留着；其余的
// 一律在前面加上 http://。
func address(typed string) string {
	target := strings.TrimSpace(typed)
	if target == "" {
		return ""
	}
	if strings.Contains(target, "://") {
		return target
	}
	lower := strings.ToLower(target)
	for _, prefix := range []string{"about:", "data:"} {
		if strings.HasPrefix(lower, prefix) {
			return target
		}
	}
	return "http://" + target
}

// roomCode 是输入内容里的房间密钥，不是密钥时返回 ""。
//
// 光秃秃的四个字母或数字会被当成密钥，而不是主机名："ABCD" 不是谁真有的机器，而它恰恰就是
// 有人会念出来的那串东西。游戏自己的大厅对被粘进里面的东西也作同样的判断
// （public/js/screens/lobby.js）。
func roomCode(typed string) string {
	key := strings.ToUpper(strings.TrimSpace(typed))
	if len(key) != roomCodeLen {
		return ""
	}
	for _, r := range key {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return key
}

// joinURL 把输入的东西变成一个窗口打得开的地址。三种形状，因为三种都是人真会粘进来的东西：
// 一整个邀请链接——它可能指向别人的机器，那就照原样用——一个光秃秃的主机名或主机名:端口，以及
// 单独一个四字符密钥，它会补全到这台机器自己的游戏上。
func joinURL(typed string) string {
	if key := roomCode(typed); key != "" {
		return gameAddress + "?room=" + key
	}
	return address(typed)
}

// showGame 把窗口指向游戏：这一次运行所在的地址，有存下的就用存下的——用链接加入的房间，
// 或者朋友的服务器——否则就用这台机器自己的。
func showGame() {
	if win == nil {
		return
	}
	slog.Info("opening the game", "url", url())
	win.Dispatch(win.Navigate, url())
}

// showNotice 把一个本程序自己搭的页面放进窗口，别的什么都不记。
func showNotice(page string) {
	if win == nil || page == "" {
		return
	}
	slog.Info("showing a page of our own", "page", page[:min(len(page), 48)])
	win.Dispatch(win.Navigate, page)
}

// 标题栏那两半 hash。本机那一半是"磁盘上这份代码来自哪个 commit"（取源码时记下的，见
// upstream.go），上游那一半要问一次网络。
//
// 两个 goroutine 会碰它们：问上游的那条写，画标题的那条读。单个写者听上去够用，但"够用"不是一个
// 值得依赖的性质——race detector 会盯上它，而 Load/Store 在这里是零代价的。
var (
	localRevisionHash  atomic.Value // string
	upstreamRevisionID atomic.Value // string
	// upstreamKnown 记的是"上游那个 hash 到底问到了没有"。
	//
	// 少了它就会把"还没问到"和"问到了、而且不一样"当成同一件事：上游没回来时 ID 是空串，而空串按
	// 约定不等于任何东西，于是标题会在问回来之前就先宣称"检测到新版本"，一秒后又悄悄撤掉——屏幕上
	// 看到的就是那句一闪而过。不知道的事不该说成知道。
	upstreamKnown atomic.Bool
)

// adoptRevision 把标题栏换成「磁盘上那份源码现在是这个 commit」。
//
// 本机那一半是**启动时**读的（见 main 里那两次 Store），而取回新源码会把它换掉。不刷新的话，标题
// 会一直显示旧的那个 hash；又因为上游那一半没动，它还会指着一个刚被装上的 commit 说「检测到新
// 版本」——屏幕上没有任何东西会说这是错的。
//
// 上游那一半在这里**作废**，而不是换成这个 hash：它答的是启动时（也就是换源码之前）上游在哪，
// 现在不作数了。作废而不是猜，是因为「不知道」在这行标题里有专门的一句话（只写本机那一半），
// 而拿旧答案硬说「有新版本」，指的很可能就是刚刚装上的那个 commit。调用方接着会重新问一次。
func adoptRevision(hash string) {
	localRevisionHash.Store(hash)
	upstreamRevisionID.Store("")
	upstreamKnown.Store(false)
	applyTitle()
}

// applyTitle 用那两个 hash 写出窗口标题。
//
//	卫戍协议：盟约 - bdb0765
//	卫戍协议：盟约 - bdb0765 - 检测到新版本 8f3a1c2
//
// 本机那份还没有时（第一次启动、归档还在下）只写程序名：写一串猜出来的字符比什么都不写更糟。
func applyTitle() {
	if win == nil {
		return
	}
	local, _ := localRevisionHash.Load().(string)
	remote, _ := upstreamRevisionID.Load().(string)
	title := windowTitle(local, remote, upstreamKnown.Load())
	slog.Info("window title", "title", title, "local", shortHash(local), "upstream", shortHash(remote),
		"asked", upstreamKnown.Load())
	win.SetTitle(title)
}

// windowTitle 是那行标题的唯一出处，也是这件事里唯一值得测的一段：三个输入拼出一句话。
//
// 拆出来是因为"标题写错了"在屏幕上的表现只是闪一下或者少一句，没有别的地方看得出来。Win32 那一
// 步（SetTitle）留给 applyTitle，这里只管说哪句话。
//
//	本机 hash 还没有            → 只写程序名
//	上游还没问到（known=false） → 只写本机那一半，**不**说新版
//	问到了，同一版              → 只写本机那一半
//	问到了，不同                → 添上"检测到新版本"
//
// 第二条是这条测试盯的坑：空串按约定"不等于"任何东西，所以少了 known 这一位，上游没回来时标题就
// 会先宣称检测到新版本，一秒后再悄悄撤掉——屏幕上正是那句一闪而过。
//
// remote 也要求非空：问到了却拿到空串是理论上不该有的事，真发生时那句话会以"检测到新版本 "收尾，
// 一个没写完的句子比不写更糟。
func windowTitle(local, remote string, known bool) string {
	if local == "" {
		return appName
	}
	title := appName + " - " + shortHash(local)
	if known && remote != "" && !sameRevision(local, remote) {
		title += " - 检测到新版本 " + shortHash(remote) + " （可在托盘菜单中更新）"
	}
	return title
}

// startupCheck 问一次 master 在哪个 commit 上，再用答案把标题补齐。
//
// 它在自己的 goroutine 里跑（由 main 起）：那是一次网络请求，而窗口已经该出来了。查不到就什么都
// 不说——一次失败的请求不是一个 commit，不该让标题栏宣称检测到新版本。
//
// 这一问每次启动只做一次，更新游戏取回新源码之后再问一次（由 main 起）。它不是一个更新器：知道了也不动
// 任何文件，只是让标题栏说一句实话——上游的代码只有「更新游戏」被按下时才会真的被取回来。
func startupCheck() {
	hash, err := upstreamRevision()
	if err != nil {
		slog.Info("upstream revision: cannot ask", "error", err)
		return
	}
	local, _ := localRevisionHash.Load().(string)
	slog.Info("upstream revision", "upstream", shortHash(hash), "local", shortHash(local))
	upstreamRevisionID.Store(hash)
	// 先记"问到了"，再画标题：顺序反过来的话，画标题那一句可能读到旧的 false，于是有一次标题少写
	// 那句"检测到新版本"。
	upstreamKnown.Store(true)
	applyTitle()
}

// openStartup 把窗口指向这一次运行打开的地址：上一次运行留在的那个（游戏自己的地址、用链接
// 加入的房间，或者朋友的服务器），什么都没存时就指向这台机器自己的游戏。
func openStartup(saved string) {
	if target := address(saved); target != "" {
		urlMu.Lock()
		currentURL = target
		urlMu.Unlock()
	}
	slog.Info("opening the saved address", "url", url())
	win.Navigate(url())
}

// setURL 把窗口指向一个新地址并记下它。加入提示就是从这里回来的，所以它是落在跑消息循环的那个
// 线程上——而那正是 Navigate 该待的地方。
func setURL(raw string) {
	target := joinURL(raw)
	if target == "" || target == url() {
		return
	}
	urlMu.Lock()
	currentURL = target
	urlMu.Unlock()
	slog.Info("opening a new address", "url", target)
	win.Navigate(target)
	remember()
}

// reprepare 把游戏停掉，再让它走游戏自己的准备步骤重新起来。
//
// update 是两个菜单项之间的全部区别：更新游戏先取回最新的源码（updateGame），重新准备游戏文
// 件则直接用现有的这棵树。准备周围的一切是特意共用的——停服务、说明进展的那个页面、把游戏摆回
// 去或者说明为什么不行——因为它们之间绝不能各走各的。
//
// 它跑在自己的 goroutine 上：准备要持续一次 250 MB 下载那么久，而那个遍历某个菜单项 channel
// 的 goroutine 不是该坐等这件事的那一个。
func reprepare(update bool) {
	go func() {
		stopGame()
		showNotice(preparingPage())
		if update {
			if err := updateGame(); err != nil {
				slog.Error("updating the game", "error", err)
				showNotice(updateFailedPage())
				return
			}
		}
		page := prepareGame(true)
		if page != "" {
			showNotice(page)
			return
		}
		// 游戏起来了——标题栏这时候才换。
		//
		// 源码在 updateGame 那一步就换了，可那会儿屏幕上还是准备页，游戏也还没起来：先报一个新 hash，
		// 等于替一个还没跑起来的版本说话。换标题与切回主界面是同一件事的两个动作，所以一起做。
		//
		// 准备没成时不换（上面那条 return）：那会儿该说的是「游戏文件没有准备完」，而本机那一半会在
		// 下一次启动时照常从磁盘上读回来。
		if update {
			adoptRevision(localRevision())
			go startupCheck()
		}
		showGame()
	}()
}

// openLocalPage 就是菜单项「提取本地客户端素材」做的事，而且它只做这一件事：把游戏
// 停掉，把那一页摆上去。
//
// 它**不**提取、也不弹选择框。人要按的是页面上那个按钮——取哪个目录是人的事，而这个入口只是把
// 那件事摆到他面前。先前这里进来就跑，用的是上次那个目录：目录已知时那一页只闪一下，人根本没有
// 机会改。三个动作因此分开：点菜单 → 给页面；点按钮 → 问目录；选定了（不是取消）→ 提取。
func openLocalPage() {
	go func() {
		stopGame()
		showNotice(localPage())
	}()
}

// extractLocalAssets 干的是活，干不成时回答该显示哪一页。空答案表示它走通了，游戏
// 可以摆回来了。
//
// 空串这个约定与更新那一套是同一个（见 reprepare）：成了就什么都不用说，把游戏摆回来就是最好的
// 交代；失败了才需要一页解释。
//
// 取材目录按这个顺序定：上次选的 → 常见位置里找得到的。两者都没有时不猜、也不弹框——交回就绪页，
// 那里有一个「选择客户端目录」的按钮，由人点（见 pickClientFolder）。
//
// 定下来之后用 --game <dir> 交给 setup.mjs，它就不必自己猜了。选过一次就记下来：装在别的盘的人
// 不必每次都回答同一个问题。
//
// 还没接上的一处：判断成没成。脚本对"没找到客户端"这类非致命项也返回 0，所以退出码不能当答案——
// 要看的是 data\local-assets.json 在不在。那一条留到下面那处调用旁边一起改。
func extractLocalAssets() string {
	client := savedClientFolder()
	if client == "" {
		client = defaultClientFolder()
	}
	// 不知道该去哪儿取：把决定交回给人。就绪页上那个按钮是唯一弹框的地方。
	if client == "" {
		slog.Info("local assets: the client folder is not known; waiting for one to be picked")
		return localPage()
	}
	slog.Info("local assets: extracting", "client", client)

	node, err := findNode()
	if err != nil {
		slog.Error("node", "error", err)
		return nodeMissingPage()
	}
	// 提取要写 public\assets\local 与 data\local-assets.json，所以和准备步骤共用那把锁。
	release, err := lockFile(prepareLockPath())
	if err != nil {
		slog.Error("local assets: cannot take the preparation lock", "error", err)
		return setupFailedPage()
	}
	defer release()

	// 跑的是游戏自己的脚本：客户端检测、Python 检测、提取，全在里面，输出一路进那块区域。
	//
	// --local 是"不问，直接提取"，正好抵掉 runSetup 里那个 --no-local——那个开关存在的原因就是
	// 这一步会提问，而这个程序没有终端。现在那个提问换成了上面那个选择框。
	if err := runSetupStep(node, "本地客户端素材提取完成", localWait,
		"--quiet", "--local", "--game", client); err != nil {
		slog.Error("local assets: extraction failed", "error", err)
		return setupFailedPage()
	}

	// 提取完了就回主界面，和「更新游戏」跑完之后一样：这一步的结果是"游戏里多了几张贴图"，不是
	// 一句要读的通知——素材已经在磁盘上，看不看得出来去游戏里看。输出框里那几十行仍留在日志文件里，
	// 页面上就没有必要再停一下。
	return ""
}

// pickAndExtract 就是页面上的「选择客户端目录」按钮跑的东西：问目录 → 记住了 → 提取。
//
// 取消就停在原地：那一页还在，按钮还在，人可以再点一次。除此之外没有别的答案，所以这里没有
// "取消之后该怎么办"这一问。
func pickAndExtract() {
	folder, ok := pickClientFolder()
	if !ok {
		slog.Info("local assets: the folder picker was cancelled")
		return
	}
	rememberClientFolder(folder)

	// 拿到了路径才动手，而动手这件事只有这一条路会走到。
	//
	// 先换成"正忙"的那一页：按钮从这一刻起就不该还在，否则再点一下就排进第二次提取。
	showNotice(localBusyPage())
	if page := extractLocalAssets(); page != "" {
		showNotice(page)
	} else {
		prepareAndShow()
	}
}

// rememberClientFolder 记下选中的目录，并顺手说一句它看起来像不像。
//
// 不像也照记：那是用户的原始输入，而真正认不认它的是 extract.py——由我们替它否决，用户只会看到
// "选了却没反应"。记下来它至少会出现在下一次的 --game 里，日志里也看得到。
func rememberClientFolder(folder string) {
	if err := saveClientFolder(folder); err != nil {
		slog.Warn("local assets: cannot remember the chosen folder", "path", folder, "error", err)
	}
	if !clientFolderLooksRight(folder) {
		slog.Warn("local assets: the chosen folder does not look like an AssetBundle root",
			"path", folder, "expected", `…\Arknights_Data\StreamingAssets\AB\Windows`)
	}
}

// prepareAndShow 在某件事把游戏停掉之后把它摆回来，摆不回来时说明为什么。
func prepareAndShow() {
	if page := prepareGame(true); page != "" {
		showNotice(page)
	} else {
		showGame()
	}
}

// remember 把窗口此刻的尺寸写下来。矩形读不出来的窗口会放着那个文件不动，而不是把一个全零
// 的尺寸写在一份好的上面。
func remember() {
	s := windowState{URL: url(), Size: currentSize().onDisk(), OnTop: win.TopMost()}
	rect, ok := win.WindowRect()
	if !ok {
		slog.Warn("window state not saved: no window rectangle to read")
		return
	}
	s.Width, s.Height, s.Maximized = rect.Width, rect.Height, rect.Maximized
	saveWindowState(s)
}

func main() {
	// 参数先看 -data-dir 决定下面这些写到哪儿，setupLogging 是第一个要写的
	parseArgs()

	// 放在最前面，这样下面的一切——包括库自己报的错——都会留下痕迹，之后有人能读到
	setupLogging()

	// 版本只进日志：它是「这一份是哪一版」唯一的答案，而日志正是排障时第一个被打开的地方
	slog.Info("starting", "version", Version, "data", appDataDir(), "log", logPath(), "integrity", processIntegrity())

	// WebView2 要在数据目录里放它的 EBWebView，而它建不出来的时候，go-webview2 会在回调里空指针
	// panic——一行日志都没有，程序已经没了。所以先自己试一次：这个错误有窗口可以说话
	if err := ensureDataDir(); err != nil {
		slog.Error("cannot create the data directory", "path", appDataDir(), "error", err)
		report("无法创建数据目录：\n" + appDataDir() + "\n\n" + err.Error() +
			integrityNote(processIntegrity()) +
			"\n\n换个位置再试：\nStronghold Protocol Launcher.exe -data-dir <目录>")
		os.Exit(3)
	}

	// 只在 Windows 上，而且必须在任何窗口出现之前：不调的话，缩放过的显示器报出来的图标
	// 度量还是 16 像素，系统只好把结果拉伸
	if err := systray.EnableDPIAwareness(); err != nil {
		slog.Warn("dpi awareness", "error", err)
	}

	// 托盘图标来自内置图形：凡是有可能被要到的尺寸都现画一张，打包成一个 .ico（internal/artwork）
	// 托盘只要一张，就是通知区域此刻要的那个尺寸——所以这里不必预置一整张尺寸表，125% 缩放的显示器也
	// 不会退而让系统去缩。需要那张表的是 exe 的资源图标，它只能构建时定
	icon, err := artwork.IconICO(smallIconSize())
	if err != nil {
		slog.Error("icon", "error", err)
		report("无法加载内置图标。")
		os.Exit(1)
	}

	// 默认尺寸：锚在屏幕宽度上，占它的 3/4，宽高比 16:9。菜单在启动时显示的就是这一套，
	// 所以先把它交给菜单——菜单才会勾在正确的位置上，之后点任何一项也才有数可算。窗口尺
	// 优先用记住的那个	//
	// 菜单算出来的*可见**尺寸，创建窗口要的是 window rect：有记录时不用换算（文件里存
	// 本来就是 window rect），第一次运行则先量一次边框再换算，这样第一帧就是菜单承诺的尺寸
	screenW, screenH := displaySize()
	preset := sizeState{anchor: screenWidth, share: share{3, 4}, ratio: ratio{16, 9}}

	// 文件只读一次：它同时带着窗口矩形和三组参数。参数在这里就要装回菜单，菜单构建时
	// 才能勾对位置
	saved, haveState := loadWindowState()
	if haveState {
		if s, ok := saved.Size.state(); ok {
			preset = s
		}
	}
	storeSize(preset)

	var state windowState
	if haveState {
		state = saved
	} else if edges, ok := measureFrameEdges(); ok {
		width, height := preset.windowSize(screenW, screenH)
		state.Width = width + int(edges.left+edges.right)
		state.Height = height + int(edges.top+edges.bottom)
		slog.Info("first run: frame measured",
			"edgeLeft", edges.left, "edgeTop", edges.top, "edgeRight", edges.right, "edgeBottom", edges.bottom,
			"windowWidth", state.Width, "windowHeight", state.Height,
			"visibleWidth", width, "visibleHeight", height)
	}

	win = createWindow(state)
	if win == nil {
		slog.Error("webview2: the runtime is missing, or the window could not be created")
		report("无法创建窗口。\n\n" +
			"多半是缺少 WebView2 运行时（Windows 11 自带，Windows 10 多数随 Edge 装过）；\n" +
			"也可能是数据目录不可写。" +
			integrityNote(processIntegrity()) +
			"\n\n详情见日志：\n" + logPath())
		os.Exit(2)
	}

	// 窗口自己的图标：任务栏和 Alt+Tab 画的是它，跟 exe 里那份资源是两回事
	if err := win.setWindowIcons(); err != nil {
		slog.Warn("window icons", "error", err)
	}

	// 窗口自己的四项（置顶 / 居中 / 刷新 / 恢复参数尺寸）接在系统菜单上：右键标题栏或者
	// Alt+空格，见 sysmenu.go。
	//
	// 上次是不是置顶，装回去：先落到窗口上，再装菜单——菜单上那个勾是从窗口读的，不是从 state 读的。
	if state.OnTop {
		win.setTopMost(true)
	}
	installSystemMenu(win)

	// 页面里那套声音是页面自己建的：public/js/audio.js 拿着它的 AudioContext，Go 这边够不着，
	// 所以往每个文档里装一段脚本，由它收放（见 setMuted）。全屏那一项也是这样。
	//
	// 必须赶在 Run 之前：Init 注册的是「之后创建的每一个文档」，已经打开的这个不算。它只收一段
	// 脚本，所以两段拼起来——各自都是独立的作用域，拼在一起不会互相看见。
	win.w.Init(muteJS + "\n" + fullscreenJS)

	// 页面把地址交回来靠这个绑定：Eval 不回传值，所以 askForRoom 的答案要从这里回来。绑定必须赶
	// Run 之前——它注册的是「之后创建的每一个文档」，已经打开的这个文档不算
	if err := win.Bind("_join", func(raw string) { setURL(raw) }); err != nil {
		slog.Warn("binding _join", "error", err)
	}
	// 提示页上那块输出区域的数据那一路：缓冲区在 setupLogging 里就接上了，这里只是开始推。
	attachLogPanel(win)

	// 标题栏：先写上磁盘上这一份的 hash——取源码时记下来的，读文件就有——上游那一半留给下面的
	// startupCheck，那是网络，不该挡着窗口出来。
	localRevisionHash.Store(localRevision())
	applyTitle()

	// 页面里的全屏按钮走标准 Fullscreen API，而那只让页面填满控件：要让窗口自己变
	if err := win.Bind("_fullscreen", func(on bool) { win.fullScreen(on) }); err != nil {
		slog.Warn("binding _fullscreen", "error", err)
	}

	// 就绪页上那个「选择客户端目录」按钮调的是这个。框要由 Go 弹：浏览器不给页面自己弹文件框的
	// 权利（要用户激活），而 <input webkitdirectory> 也只给相对路径——绝对路径是它故意不给的，
	// 而提取要的正是绝对路径。
	//
	// 绑定跑在窗口线程上，而下面那个函数会停住一整个弹窗的生命周期，所以它在自己的 goroutine 里走。
	if err := win.Bind("_pickFolder", func() { go pickAndExtract() }); err != nil {
		slog.Warn("binding _pickFolder", "error", err)
	}

	// 先认下上一次的地址：可能是朋友发来的加入链接，那就直接开到那一局；没有就用本机自己的
	openStartup(saved.URL)

	// 游戏：端口上已经有游戏在回应就直接开它，否则由本程序把它拉起来。准备工作——第一次要下载
	// 源码、装依赖、拉 250 MB 素材——全在窗口背后做，这期间窗口显示的是本程序自己的一页
	//
	// 联机的模型是「所有人连同一个后端」：同一个端口上的第二条连接才是第二个玩家，所以这里不另起
	// 一个服务端去避开占用——那是另一局，不是队友
	if noGame {
		// 什么都不启动：窗口对着上面那个地址，服务由别处提供
		showGame()
	} else {
		showNotice(preparingPage())
		go func() {
			page := prepareGame(false)
			// 第一次启动时上面那一次 applyTitle 还写不出 hash（游戏正被下下来，那个小文件也还没
			// 写）。等源码落地了再写一次，然后把上游那一问发出去——它要等本机这份 hash 有了才有
			// 意义，早了只能比出"不知道"。
			localRevisionHash.Store(localRevision())
			applyTitle()
			go startupCheck()
			if page != "" {
				showNotice(page)
			} else {
				showGame()
			}
		}()
	}

	onReady := func() {
		systray.SetIcon(icon)
		systray.SetTooltip(trayName(Version))
		systray.SetOnLeftClick(win.Show)
		addMenuItems()
	}

	// WebView2 自己跑消息循环，而托盘的隐藏窗口挂在同一个线程上：它的消息得由那一个循环分发
	// 出去——这正是 systray.Register 存在的理由。用 systray.Run 会和 WebView2 抢那个循环。
	systray.Register(onReady, nil)
	win.Run()
	slog.Info("the message loop ended")
	// 窗口没了，推送那一路也停下来：之后写下的几行记录不该再往一个不存在的页面里发。
	stopLog()

	// 退出前把窗口尺寸记下来。窗口最大化着退出也记得对：WindowRect 取的是「还原后的那个矩
	// 形」，并把最大化这件事一起记下
	remember()
	// 游戏服务跟着窗口一起走：把它留下，就等于让一个屏幕上已经没有前端对着的服务器继续跑。本
	// 程序启动之前就已经在跑的服务不是我们的，stopGame 不会碰它
	stopGame()
	// 下面不能再有任何东西绕过这里结束进程：本程序里每一个 os.Exit 都发生在 win 存在之前，也
	// 因为这样，Destroy 可以就是一个放在最后的普通调用，而不必 defer
	win.Destroy()
}

// addMenuItems 构建菜单。systray 按调用的先后次序追加，所以下面这些调用从上到下跑，
// 和菜单画出来的样子分毫不差：
//
//	显示窗口
//	开机自启动
//	────────────────
//	打开 ▸
//	  打开日志文件
//	  打开游戏目录
//	  打开程序目录
//	更多 ▸
//	  更新游戏
//	  输入加入链接
//	  在浏览器中打开
//	  重新准备游戏文件
//	────────────────
//	测试 （本地构建才有）
//	退出
func addMenuItems() {
	mShow := systray.AddMenuItem("显示窗口", "显示主窗口")
	go func() {
		for range mShow.ClickedCh {
			win.Show()
		}
	}()

	// 开机自启动紧随显示窗口。它是这张菜单里唯一的状态（勾选框），摆在最上面，勾没勾
	// 一眼就看得见，不必为了确认这一点把菜单读到底。
	//
	// 勾的是注册表里的真实状态，不是记在内存里的意图：setAutostart 写完之后会再读一次，
	// 写不进去（策略、权限）时勾就上不去，用户一眼能看出来没生效
	mStartup := systray.AddMenuItemCheckbox("开机自启动", "登录时自动运行本程序", autostartEnabled())
	go func() {
		for range mStartup.ClickedCh {
			if setAutostart(!autostartEnabled()) {
				mStartup.Check()
			} else {
				mStartup.Uncheck()
			}
		}
	}()

	// 窗口那几项（置顶 / 居中 / 刷新 / 恢复参数尺寸）和三组尺寸参数搬到了窗口自己的
	// 菜单里（右键标题栏、Alt+空格，见 sysmenu.go）：它们调的就是这个窗口，跟托盘没关系
	// 窗口的事跟着窗口走，托盘只留这个程序自己的事
	systray.AddSeparator()

	// 加入与在浏览器打开这一对是从「更多」搬上来的：它们说的是游戏**本身**（换一台机器接着玩、
	// 或者绕过这个窗口直接开），而「更多」里留下的那几件事都会动磁盘上的文件。两条分隔线把这一组
	// 从上面那个勾和下面那些子菜单之间夹出来。
	//
	// 加入这一项回到网页原来那套「改地址」的做法：问题在页面里问（Go 这边没有控制台，消息框也
	// 不回一行字），答案由 _join 绑定回来（见 setURL 与 js/askForRoom.js）。输入进去的可以是完整
	// 的加入链接、主机名，或者光秃秃的 4 位密钥——怎么补全由 joinURL 决定
	mJoin := systray.AddMenuItem("输入加入链接", "粘贴朋友发来的加入链接，或直接输入 4 位同盟密钥")
	go func() {
		for range mJoin.ClickedCh {
			// 先把窗口叫出来，再把脚本交给窗口的线程：Eval 最终落在 WebView2 controller 的调用上，
			// 那不是菜单 goroutine 该碰的东西
			win.Show()
			win.Dispatch(win.w.Eval, askForRoomJS+"askForRoom("+fmt.Sprintf("%q", url())+");")
		}
	}()

	mBrowser := systray.AddMenuItem("在浏览器中打开", "用系统默认浏览器打开游戏地址")
	go func() {
		for range mBrowser.ClickedCh {
			shellOpen(gameAddress)
		}
	}()

	systray.AddSeparator()

	mOpen := systray.AddMenuItem("打开", "打开文件或目录")

	// 这三个都是「打开…」，是同一件事，所以收进「打开」这一个子菜单里，不占托盘的一行
	mLog := mOpen.AddSubMenuItem("打开日志文件", "用默认程序打开运行日志")
	go func() {
		for range mLog.ClickedCh {
			shellOpen(logPath())
		}
	}()

	// 「打开游戏目录」开的是本程序自己那份（%LOCALAPPDATA% 下的 game\），不是谁另外 clone 的一份：
	// 源码、依赖、素材都由本程序管
	mFolder := mOpen.AddSubMenuItem("打开游戏目录", "打开游戏源码与素材所在的那个目录")
	go func() {
		for range mFolder.ClickedCh {
			shellOpen(gameDir())
		}
	}()

	// 程序目录和上面那些不是一回事：上面都在 %LOCALAPPDATA% 下，是这个程序管的地方；
	// 程序目录是这个 .exe 被解开放着的地方，属于把它放在那里的人（见 appdata.go）
	// 「程序」不是随手挑的词：原来叫「运行目录」，而「运行」在这个菜单里什么都不特指
	mProgramDir := mOpen.AddSubMenuItem("打开程序目录", "打开本程序 exe 所在的那个目录")
	go func() {
		for range mProgramDir.ClickedCh {
			dir, err := programDir()
			if err != nil {
				slog.Warn("program dir", "error", err)
				continue
			}
			shellOpen(dir)
		}
	}()

	// 「更多」装的是动这个游戏的三件事：取新版、重新准备、以及拿到浏览器里再开一份
	mMore := systray.AddMenuItem("更多", "更多操作")

	// 「更新」和「重新准备」共用一条路（reprepare），只差开头那一步：更新先把最新源码取回来
	// 并把必须重建的东西清掉，重新准备则直接用现有这棵树
	mUpdate := mMore.AddSubMenuItem("更新游戏", "从 GitHub 取回最新源码，重装依赖、补齐素材，然后重启游戏服务")
	go func() {
		for range mUpdate.ClickedCh {
			reprepare(true)
		}
	}()

	// 「打开项目仓库」就在取源码那一项旁边：更新是从这个仓库取的，而这一项是去**读**它——想知道这一版
	// 里有什么、或者要报个问题，都是从这里走。它开的是游戏那一份源码的仓库，不是本程序自己的。
	mRepo := mMore.AddSubMenuItem("打开项目仓库", "在浏览器里打开游戏源码所在的仓库")
	go func() {
		for range mRepo.ClickedCh {
			shellOpen(upstreamRepo)
		}
	}()

	// 「重新准备」是唯一能把中断的素材下载接下去的入口：那一半的状态只有游戏自己知道，
	// setupNeeded（launcher.go）看不见缺的是哪一个文件
	mPrepare := mMore.AddSubMenuItem("重新准备游戏文件", "重新安装依赖并下载美术素材，然后重启游戏服务")
	go func() {
		for range mPrepare.ClickedCh {
			reprepare(false)
		}
	}()

	// 「提取本地客户端素材」是另一个来源的素材：公开镜像里没有的那部分，要从本机装的《明日方舟》
	// 客户端里取出来。上游的 setup.mjs 本来就会问一次要不要提取，而这个程序没有终端能回答那个问题，
	// 所以一直是用 --no-local 关掉的——这一项就是那个问题的答案：改由菜单来问。
	//
	// 点下去先给一张加载页，页面上那块输出区域会把过程显示出来（与「更新游戏」同一套）。
	mLocal := mMore.AddSubMenuItem("提取本地客户端素材", "从本机安装的《明日方舟》客户端里提取官方棋盘与界面素材")
	go func() {
		for range mLocal.ClickedCh {
			openLocalPage()
		}
	}()

	systray.AddSeparator()

	// 本地构建里多一个「测试」子菜单：几张提示页要等真实的故障才看得到，这里能直接调出来看
	// 判断的是 Version —— 发布时的 -X main.Version=<tag> 一定把它填上，所以只有自己编的那一份还是 "dev"
	if Version == "dev" {
		mTest := systray.AddMenuItem("测试", "调出几张本地提示页")
		for _, c := range []struct {
			title   string
			tooltip string
			page    func() string
		}{
			{"正在准备", "首次启动、下载与安装时显示的那一张", preparingPage},
			{"需要 Node.js", "找不到合格的 node 时显示的那一张", nodeMissingPage},
			{"无法获取源码", "下载游戏源码失败时显示的那一张", sourceFailedPage},
			{"更新失败", "「更新游戏」没能完成时显示的那一张", updateFailedPage},
			{"准备失败", "安装依赖或下载素材出错时显示的那一张", setupFailedPage},
			{"启动失败", "服务始终没有回应时显示的那一张", startFailedPage},
			{"端口被占用", "3000 上已经有别的程序时显示的那一张", portBusyPage},
		} {
			item := mTest.AddSubMenuItem(c.title, c.tooltip)
			page := c.page
			go func() {
				for range item.ClickedCh {
					win.Show()
					showNotice(page())
				}
			}()
		}
	}

	mQuit := systray.AddMenuItem("退出", "退出程序")
	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()
}
