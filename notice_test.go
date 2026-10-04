package main

// The pages the app shows when there is no game to show. They are built here rather than served,
// so they are still there while the game's own server is being downloaded, installed or started.

import (
	"encoding/base64"
	"strings"
	"testing"
)

// decodeNotice unwraps the data URL into the page it carries, the way the browser does.
func decodeNotice(t *testing.T, page string) string {
	t.Helper()
	const prefix = "data:text/html;charset=utf-8;base64,"
	if !strings.HasPrefix(page, prefix) {
		t.Fatalf("page = %.60q..., want a base64 data URL: it has to survive being handed to a browser with no listener behind it", page)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(page, prefix))
	if err != nil {
		t.Fatalf("decode the page: %v", err)
	}
	return string(raw)
}

func TestNoticePages(t *testing.T) {
	if logPath() == "" {
		t.Fatal("logPath is empty, so the pages that name it cannot be checked")
	}
	for _, c := range []struct {
		name  string
		page  string
		title string
	}{
		{"preparing", preparingPage(), preparingTitle},
		{"node", nodeMissingPage(), nodeTitle},
		{"source", sourceFailedPage(), sourceTitle},
		{"update", updateFailedPage(), updateTitle},
		{"setup", setupFailedPage(), setupTitle},
		{"start", startFailedPage(), startTitle},
		{"port", portBusyPage(), portTitle},
	} {
		html := decodeNotice(t, c.page)
		if !strings.Contains(html, c.title) {
			t.Errorf("%s page does not carry its title %q", c.name, c.title)
		}
	}
}

// 「哪几页带输出框」：只靠那个属性有没有出现在标签上，而 CSS 只认这个属性。
//
// 这条测试盯的是一处真出过的错：Go 渲染的和 CSS 认的必须是同一件东西。曾经 Go 写属性 log="true"、
// 而 CSS 认的是类名 .log-on——两半对不上，于是没有任何规则把框显示出来，屏幕上只表现为「全都不
// 显示」，看不出原因。
//
// 它只做静态检查，不模拟 CSS：页面里必须既有那个属性、又有认这个属性的规则。
func TestNoticePanelMarkMatchesTheCSS(t *testing.T) {
	on := decodeNotice(t, preparingPage())
	if !strings.Contains(on, `<section id="log-panel" log>`) {
		t.Error("带输出框的页，标签上应当有 log 这个属性")
	}
	if !strings.Contains(on, "#log-panel[log] {") {
		t.Error("CSS 里应当有一条认 log 属性的规则")
	}
	if !strings.Contains(on, "display: flex !important") {
		t.Error("那条规则要把框显示出来")
	}

	// 不带输出框的页：标签上一个 log 也没有，于是只有基础规则里的 display:none 生效。
	off := decodeNotice(t, portBusyPage())
	if !strings.Contains(off, `<section id="log-panel" >`) {
		t.Error("不带输出框的页，那一处不该有 log 属性")
	}
}

// 输出区域的画笔是页面自己的静态脚本，随文档一起发。
//
// 这条测试盯两件事：脚本确实在页面上（不是靠注入、不是靠模板值），以及它里面没有模板动作——
// 在 <script> 里放一个 {{ ... }}，编辑器会报「Declaration or statement expected」，而浏览器
// 拿到的是被换过值的脚本。
func TestNoticePageCarriesThePainter(t *testing.T) {
	html := decodeNotice(t, preparingPage())
	for _, want := range []string{
		"window.__spLog = function (lines) {",
		"document.createTextNode(line)",
		"row.className = 'log-row'",
		"#log-box",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("页面上缺了画笔的一部分：%q", want)
		}
	}
	if strings.Contains(html, "{{") || strings.Contains(html, "}}") {
		t.Error("渲染出来的页面里还剩模板动作——脚本里不该有 {{ }}")
	}
}

// noticePage 是唯一知道「这一页要不要输出框」的地方，推送那一路也从它这里拿这个值。
func TestNoticePageSetsTheLogSwitch(t *testing.T) {
	saved := showLog
	defer func() { showLog = saved }()

	preparingPage()
	if !showLog {
		t.Error("准备页带输出框，推送开关该打开")
	}
	portBusyPage()
	if showLog {
		t.Error("端口那一页不带输出框，推送开关该关掉")
	}
}

func TestNoticePageEscapesItsText(t *testing.T) {
	// 标题、正文、路径三者都过一遍引擎：路径里同样会有 < 和 &（用户名里就可能有）。
	html := decodeNotice(t, noticePage(false, "T & T", "<not a tag>", "C:\\a & b\\log"))
	for _, want := range []string{"T &amp; T", "&lt;not a tag&gt;", "C:\\a &amp; b\\log"} {
		if !strings.Contains(html, want) {
			t.Fatalf("text handed to noticePage was not escaped as %q:\n%s", want, html)
		}
	}
}
