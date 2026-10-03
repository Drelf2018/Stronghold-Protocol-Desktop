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
	preparingBody  = "第一次启动要先取回游戏源码、安装依赖，并下载美术与音频素材（约 250 MB）。\n\n" +
		"这一步只走一次，中途断了也没关系：下次启动会从中断的地方继续。\n" +
		"进度都写在日志里："

	nodeTitle = "需要 Node.js 22 或更高版本"
	nodeBody  = "游戏服务由 Node.js 运行，而这台机器上找不到合格的 node。\n\n" +
		"装一个（任选一种）：\n" +
		"  winget install OpenJS.NodeJS.LTS\n" +
		"  https://nodejs.org/zh-cn/download\n\n" +
		"装好之后重新启动本程序——PATH 要新开一个进程才读得到。"

	sourceTitle = "无法获取游戏源码"
	sourceBody  = "没能从 GitHub 下载「卫戍协议：盟约」的源码。\n\n" +
		"请检查网络或代理，然后重新启动本程序。已经下过的部分不用重来，\n" +
		"失败的原因写在日志里："

	updateTitle = "游戏没能更新"
	updateBody  = "取回新源码、清理旧的依赖或刷新上游索引时出错了。\n\n" +
		"常见的是两件事：网络或代理取不回新源码；或者旧的 node_modules 删不掉\n" +
		"（还有 node 进程占着它）。再按一次会重试，已经写进去的文件会被覆盖。\n" +
		"出错的原因写在日志里："

	setupTitle = "游戏文件没有准备完"
	setupBody  = "安装依赖或下载素材时出错了。\n\n" +
		"修好网络之后，从托盘菜单选「重新准备游戏文件」就能接着来\n" +
		"（已经下载的部分不会重来）。出错的原因写在日志里："

	startTitle = "游戏服务没能启动"
	startBody  = "服务进程起来了，但一直没有在 3000 端口上回应。\n\n" +
		"原因写在日志里："

	portTitle = "端口 3000 已经被占用"
	portBody  = "3000 端口上已经有别的程序在监听，游戏服务起不来。\n\n" +
		"先关掉占用它的那个程序，再重新启动本程序。"
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
	Title string
	Body  string
	Path  string
}

// noticePage builds one small self-contained page. The markup is deliberately plain: this is a
// message, not a page anyone styles.
func noticePage(title, body, path string) string {
	page := bytes.NewBufferString("data:text/html;charset=utf-8;base64,")
	enc := base64.NewEncoder(base64.StdEncoding, page)
	err := noticeLayout.Execute(enc, noticeData{Title: title, Body: body, Path: path})
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
	return noticePage(preparingTitle, preparingBody, logPath())
}

// nodeMissingPage is shown when there is no Node.js new enough to run the game. The box at the
// bottom is the command that installs one, not a path: it is the thing to copy.
func nodeMissingPage() string {
	return noticePage(nodeTitle, nodeBody, "winget install OpenJS.NodeJS.LTS")
}

// sourceFailedPage is shown when the repository's archive could not be fetched.
func sourceFailedPage() string {
	return noticePage(sourceTitle, sourceBody, logPath())
}

// updateFailedPage is shown when 更新游戏 could not finish - fetching the new source, clearing the
// old dependencies, or refreshing the upstream index tables.
func updateFailedPage() string {
	return noticePage(updateTitle, updateBody, logPath())
}

// setupFailedPage is shown when the game's own preparation step failed.
func setupFailedPage() string {
	return noticePage(setupTitle, setupBody, logPath())
}

// startFailedPage is shown when the server was started but never answered.
func startFailedPage() string {
	return noticePage(startTitle, startBody, logPath())
}

// portBusyPage is shown when the server died because something else holds the port.
func portBusyPage() string {
	return noticePage(portTitle, portBody, "")
}
