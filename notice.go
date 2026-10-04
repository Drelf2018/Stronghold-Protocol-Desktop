//go:build windows

package main

// The pages this app shows when there is no game to show.
//
// They are built here and handed to the window as data URLs, which is the one kind of page that
// needs no listener, no file and no second server - so it is still there while the game's own
// server is being downloaded, installed or started, and it cannot answer with a connection error
// the way an address with nothing behind it does.
//
// The markup is loading.html, beside this file: HTML belongs in a file, where an editor
// highlights, indents and checks it. Go contributes the two texts and nothing else -
// html/template escapes by context, so nothing here escapes by hand.

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

	startTitle = "游戏服务进程未回应"
	startBody  = "查看日志："

	portTitle = "端口 3000 已经被占用"
	portBody  = "端口上已经有别的程序在监听，尝试关掉占用它的那个程序，再重新启动本程序。"
)

//go:embed loading.html
var noticeFS embed.FS

// noticeLayout is the one page layout every notice above is poured into, parsed once at startup.
//
// Must, and not an error carried up to main: the layout is a file in this repository, and the way
// to find out it is broken is to run the program - or the tests, whose binary the panic takes down
// with it. A -H=windowsgui build has no stderr, so a panic here ends the program in silence; that
// is the price of not carrying an error nobody can act on to the top of main.
//
// The file is named rather than globbed on purpose: with a glob, a second layout file would be
// parsed and then quietly ignored, because Execute on a parsed set runs whichever template the set
// was named after - the first file - and nothing would say so.
var noticeLayout = template.Must(template.ParseFS(noticeFS, "loading.html"))

// noticeData is what the layout is filled with.
type noticeData struct {
	Log   bool
	Title string
	Body  string
	Path  []string
}

// noticePage builds one small self-contained page. The markup is deliberately plain: this is a
// message, not a page anyone styles.
func noticePage(log bool, title, body string, path ...string) string {
	// 这一页带不带输出区域，推送那一路也从这里知道——同一个布尔值，同一个地方说出去。显示是
	// 标记里那个属性、CSS 认它；推送是这里的一个开关。两件事都不经过页面。
	showLog = log

	page := bytes.NewBufferString("data:text/html;charset=utf-8;base64,")
	enc := base64.NewEncoder(base64.StdEncoding, page)
	err := noticeLayout.Execute(enc, noticeData{Title: title, Body: body, Path: path, Log: log})
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

// preparingPage is what the window shows while the game is being fetched, prepared and started.
// It is also what 重新准备游戏文件 puts up, which is the one place the interrupted 250 MB
// download is resumed from.
func preparingPage() string {
	return noticePage(true, preparingTitle, preparingBody)
}

// nodeMissingPage is shown when there is no Node.js new enough to run the game. The box at the
// bottom is the command that installs one, not a path: it is the thing to copy.
func nodeMissingPage() string {
	return noticePage(false, nodeTitle, nodeBody, "winget install OpenJS.NodeJS.LTS", "https://nodejs.org/zh-cn/download")
}

// sourceFailedPage is shown when the repository's archive could not be fetched.
func sourceFailedPage() string {
	return noticePage(false, sourceTitle, sourceBody, logPath())
}

// updateFailedPage is shown when 更新游戏 could not finish - fetching the new source, clearing the
// old dependencies, or refreshing the upstream index tables.
func updateFailedPage() string {
	return noticePage(false, updateTitle, updateBody, logPath())
}

// setupFailedPage is shown when the game's own preparation step failed.
func setupFailedPage() string {
	return noticePage(false, setupTitle, setupBody, logPath())
}

// startFailedPage is shown when the server was started but never answered.
func startFailedPage() string {
	return noticePage(false, startTitle, startBody, logPath())
}

// portBusyPage is shown when the server died because something else holds the port.
func portBusyPage() string {
	return noticePage(false, portTitle, portBody)
}
