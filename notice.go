//go:build windows

package main

// 没有游戏可显示时，本程序拿出来的那几页。
//
// 它们在这里现画，然后当作 data URL 交给窗口——那是唯一一种不需要监听端口、不需要落成文件、
// 也不需要第二个服务的页面。所以游戏自己的服务还在下载、安装或者启动的那段时间里，它照样在
// 屏幕上；也不会像「一个背后什么都没有的地址」那样，回给你一个连接失败。
//
// 标记是隔壁的 loading.html：HTML 该待在文件里——编辑器在那里有高亮、缩进和检查。Go 只提供
// 那两段文字，其余不管；html/template 按上下文转义，所以这里没有一处手写的转义。

import (
	"bytes"
	"embed"
	"encoding/base64"
	"html/template"
	"log/slog"
)

const (
	preparingTitle = "正在准备「卫戍协议：盟约」"
	preparingBody  = "第一次启动或者更新时要先取回游戏源码、安装依赖，并下载美术与音频素材。"

	nodeTitle = "需要 Node.js 22 或更高版本"
	nodeBody  = "游戏服务由 Node.js 运行，而这台机器上找不到合格的 node。\n\n" +
		"任选一种方式进行安装，之后重新启动本程序。"

	sourceTitle = "无法获取游戏源码"
	sourceBody  = "没能从 GitHub 下载「卫戍协议：盟约」的源码。\n\n" +
		"请检查网络或代理，然后重新启动本程序。\n\n查看日志："

	updateTitle = "游戏没能更新"
	updateBody  = "取回新源码、清理旧的依赖或刷新上游索引时出错了。\n\n" +
		"网络或代理连接失败，或者 node_modules 删除失败。\n\n查看日志："

	setupTitle = "游戏文件没有准备完"
	setupBody  = "安装依赖或下载素材时出错了。\n\n" +
		"从托盘菜单选「重新准备游戏文件」重新尝试。\n\n查看日志："

	localTitle = "提取本地客户端素材"
	localBody  = "从本机安装的客户端里提取官方素材，素材目录："

	startTitle = "游戏服务进程未回应"
	startBody  = "查看日志："

	portTitle = "端口 3000 已经被占用"
	portBody  = "端口上已经有别的程序在监听，尝试关掉占用它的那个程序，再重新启动本程序。"
)

//go:embed loading.html
var noticeFS embed.FS

// noticeLayout 是上面每一页都倒进去的那一份版式，启动时解析一次。
//
// 用 Must 而不是把错误带回 main：版式是本仓库里的一个文件，想知道它坏了只有一条路——把程序跑
// 起来，或者跑测试（那个 panic 会连测试的二进制一起带走）。而且 -H=windowsgui 的构建没有标准
// 错误输出：这里 panic 就是无声地结束程序。这就是「不把一个没人能处理的错误往上传」的代价。
//
// 写文件名而不是用通配符是刻意的：用通配符的话，多出一份版式文件会被解析、然后被悄无声息地
// 忽略——解析出来的集合执行的是它被命名时的那一份，也就是第一个文件，而没有东西会说一句。
var noticeLayout = template.Must(template.ParseFS(noticeFS, "loading.html"))

// noticeData 是版式里填的东西。
//
// Name 是这一页叫什么。模板按它认那几个只属于某页的控件（见 loading.html）——一个能弹文件框的
// 按钮不是每张提示页都该有的东西，而页名是那件事唯一的判据。
//
// Disabled 是那个按钮的 disabled 属性：提取正在跑时它该按不动。用原生属性而不是把按钮拿掉——
// 拿掉会让人以为看错了地方，而按不动说的是同一件事，原因就写在下面那块输出区域里。
//
// 按钮自己不做事。它只调 _pickFolder 那个绑定（见 main.go），由 Go 去弹系统的文件框——浏览器
// 不给页面「自己弹框」的权利，而绝对路径也拿不到。
type noticeData struct {
	Log      bool
	Name     string
	Title    string
	Body     string
	Path     []string
	Disabled bool
}

// noticePage 画一张小巧而自足的页面。标记刻意朴素：这是一条消息，不是一张有人会去做样式的页面。
func noticePage(name string, log bool, title, body string, path ...string) string {
	return noticePageFor(noticeData{Name: name, Title: title, Body: body, Path: path, Log: log})
}

// noticePageFor 是真正画那一页的地方：调用方把整份数据交过来，包括好几个只属于某页的开关。
func noticePageFor(data noticeData) string {
	// 这一页带不带输出区域，推送那一路也从这里知道——同一个布尔值，同一个地方说出去。显示是
	// 标记里那个属性、CSS 认它；推送是这里的一个开关。两件事都不经过页面。
	showLog = data.Log

	page := bytes.NewBufferString("data:text/html;charset=utf-8;base64,")
	enc := base64.NewEncoder(base64.StdEncoding, page)
	err := noticeLayout.Execute(enc, data)
	if err != nil {
		slog.Error("notice page", "error", err)
		return ""
	}
	err = enc.Close()
	if err != nil {
		slog.Error("notice page", "error", err)
		return ""
	}
	return page.String()
}

// preparingPage 是游戏正在取回、准备、启动的那段时间里窗口显示的东西。它也是「重新准备游戏
// 文件」摆出来的那一页，而那是把中断的 250 MB 下载接下去的入口。
func preparingPage() string {
	return noticePage("preparing", true, preparingTitle, preparingBody)
}

// nodeMissingPage 是没有够新的 Node.js 来跑游戏时显示的东西。最下面那条框里是装它的命令，不是
// 一条路径：那是用来复制的东西。
func nodeMissingPage() string {
	return noticePage("nodeMissing", false, nodeTitle, nodeBody, "winget install OpenJS.NodeJS.LTS", "https://nodejs.org/zh-cn/download")
}

// sourceFailedPage 是取不到仓库那份归档时显示的东西。
func sourceFailedPage() string {
	return noticePage("sourceFailed", false, sourceTitle, sourceBody, logPath())
}

// updateFailedPage 是「更新游戏」没能走完时显示的东西——取新源码、清旧依赖、刷新上游索引，
// 其中任何一步出错都算。
func updateFailedPage() string {
	return noticePage("updateFailed", false, updateTitle, updateBody, logPath())
}

// setupFailedPage 是游戏自己的准备步骤失败时显示的东西。
func setupFailedPage() string {
	return noticePage("setupFailed", false, setupTitle, setupBody, logPath())
}

// localPage is the one page this feature has：这是干什么的、客户端里那个目录长什么样、一个按钮让人
// 把它指出来、以及下面那块输出区域载着提取过程。
//
// 它担着从"等人指路"到"提取完成"的整段过程，而这是有意的：那些状态之间只有文案与一个按钮的差别，
// 为它们各写一页只会得到三份几乎相同的说明。
//
// 三处名字是同一个词，一眼能对上：函数 localPage、常量 localTitle / localBody、以及写进页面的页名
// "local"——而模板按那个页名认这个按钮（见 loading.html）。
func localPage() string { return localPageWith(false) }

// localBusyPage 是同一页在"提取已经开始了"的样子：文案与输出框都在，那个按钮按不动。
//
// 按不动的理由是具体的：框选完就开跑，而按钮还能按的话，再按一下就排进第二次提取——那个人多半
// 只是以为没反应。取哪个目录这件事在跑起来之后已经没有第二个答案，所以按钮该把这件事说出来。
func localBusyPage() string { return localPageWith(true) }

func localPageWith(disabled bool) string {
	return noticePageFor(noticeData{
		Name: "local", Log: true, Disabled: disabled,
		Title: localTitle, Body: localBody,
		Path: []string{"<游戏目录>\\Arknights_Data\\StreamingAssets\\AB\\Windows"},
	})
}

// startFailedPage 是服务启动了、却始终没回应时显示的东西。
func startFailedPage() string {
	return noticePage("startFailed", false, startTitle, startBody, logPath())
}

// portBusyPage 是服务因为端口被别人占着而死掉时显示的东西。
func portBusyPage() string {
	return noticePage("portBusy", false, portTitle, portBody)
}
