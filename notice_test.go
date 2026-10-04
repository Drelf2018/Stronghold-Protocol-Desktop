package main

// 没有游戏可显示时，本程序拿出来的那几页。它们是在这里现画的，不是从哪个服务取来的——所以游戏
// 自己的服务还在下载、安装或者启动的那段时间里，它们照样在。

import (
	"encoding/base64"
	"strings"
	"testing"
)

// decodeNotice 把 data URL 拆成它装着的那张页面，做法与浏览器一样。
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
		{"local", localPage(), localTitle},
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
		// 前缀与正文是两段文字节点——都不拼 HTML。
		"document.createTextNode('[' + (level || 'INFO') + ']')",
		"row.appendChild(document.createTextNode(text))",
		"row.className = 'log-row ' + levelClass(level)",
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

// 「提取本地客户端素材」那两页：一张是过程（带输出框），一张是就位说明（也带）。
//
// 新加一页时最容易漏的正是那个 log 属性——漏了的表现就是"框没出现"，而屏幕上没有别的东西会告诉你
// 少了什么。所以这里两页都查。
// 本地素材那一页：带输出框、带那个按钮，而且渲染它时推送开关真的被打开。
//
// 这一页担着整段过程——点菜单进来是它、等人点按钮是它、提取中的输出也写在它上面——所以几条断言
// 写在一起：它们说的是同一页该长什么样。
func TestLocalPage(t *testing.T) {
	saved := showLog
	defer func() { showLog = saved }()

	showLog = false
	html := decodeNotice(t, localPage())

	// 输出框：那块区域是这块页面承载过程的地方。
	if !strings.Contains(html, `<section id="log-panel" log>`) {
		t.Error("这一页应当带输出框")
	}
	if !strings.Contains(html, "#log-box") {
		t.Error("这一页缺了画行的地方")
	}
	// 而推送那一路也要跟着开：显示与推送看的是同一个值。
	if !showLog {
		t.Error("渲染这一页时，推送开关该打开")
	}

	// 那个按钮：只有这一页有它，模板按页名认。
	if !strings.Contains(html, `id="pick-client"`) {
		t.Error("这一页该有「选择客户端目录」那个按钮")
	}
}

// 那个按钮的两个状态：等人指路时按得动，提取已经在跑时按不动。
//
// 用原生 disabled 而不是把按钮拿掉——拿掉之后人会以为看错了地方。这条测试就是钉住这一点：两页
// 渲染出来的按钮必须在，只有一个字的差别。
// buttonOf 把渲染结果里那个按钮标签抠出来，好在报告里看清它带了哪些属性。
func buttonOf(html string) string {
	const at = `<button type="button" id="pick-client"`
	i := strings.Index(html, at)
	if i < 0 {
		return "(没有这个按钮)"
	}
	j := strings.Index(html[i:], ">")
	if j < 0 {
		return html[i:]
	}
	return html[i : i+j+1]
}

func TestPickButtonIsDisabledWhileExtracting(t *testing.T) {
	idle := decodeNotice(t, localPage())
	busy := decodeNotice(t, localBusyPage())

	for name, html := range map[string]string{"等人指路": idle, "提取中": busy} {
		if !strings.Contains(html, `id="pick-client"`) {
			t.Errorf("%s：按钮该在（拿掉它比按不动更让人迷惑）", name)
		}
	}
	// 渲染完了就不该还剩模板动作——这一页每次都是现画的，留下一个说明数据没填对。
	// 这一条最容易被看漏，所以把两边的按钮原样打出来：-v 时就在报告里。
	t.Logf("等人指路：%s", buttonOf(idle))
	t.Logf("提取中：  %s", buttonOf(busy))

	if strings.Contains(idle, "{{") || strings.Contains(busy, "{{") {
		t.Error("渲染出来的页面里还剩模板动作")
	}
	if !strings.Contains(idle, `id="pick-client" >`) && !strings.Contains(idle, `id="pick-client" `) {
		t.Error("等人指路那一页的按钮不该带 disabled")
	}
	if strings.Contains(idle, `id="pick-client" disabled`) {
		t.Error("等人指路那一页的按钮是可点的")
	}
	if !strings.Contains(busy, `id="pick-client" disabled`) {
		t.Error("提取中那一页的按钮该带原生 disabled")
	}
}

// 输出框的等级着色：等级 → 类名，以及每个类名都真的有颜色。
//
// 这一条盯的是一处静默的退化：加了新的等级、忘了加样式，或者 Go 那边的写法变了而这里没跟上，
// 表现都只是"那一行是灰的"——不会报错，也不会有人发现。所以两边都查。
func TestLogPanelColoursEachLevel(t *testing.T) {
	html := decodeNotice(t, preparingPage())

	// 画笔按等级挑类名，四个等级都在。
	for _, want := range []string{
		"function levelClass(level) {",
		"'log-error'",
		"'log-warn'",
		"'log-debug'",
		"'log-info'",
		// 前缀是另一段文字节点，正文跟着它——两段都不拼 HTML。
		"className = 'log-level'",
		"row.appendChild(document.createTextNode(text))",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("画笔少了一部分：%q", want)
		}
	}

	// 每个类名都要真的落到颜色上，并且用在同一行上（子选择器认的是那一行自己的类）。
	for _, want := range []string{
		".log-info {",
		".log-warn {",
		".log-error {",
		".log-debug {",
		".log-error > .log-level {",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("CSS 少了一条：%q", want)
		}
	}
}

// 那个按钮只属于本地素材那一页：别的提示页上不该出现一个能弹系统文件框的东西。
func TestPickButtonBelongsToTheLocalPageOnly(t *testing.T) {
	for _, c := range []struct {
		name string
		page string
	}{
		{"准备", preparingPage()},
		{"需要 Node.js", nodeMissingPage()},
		{"端口", portBusyPage()},
	} {
		if strings.Contains(decodeNotice(t, c.page), `id="pick-client"`) {
			t.Errorf("%s 那一页不该有这个按钮", c.name)
		}
	}
}

func TestNoticePageEscapesItsText(t *testing.T) {
	// 标题、正文、路径三者都过一遍引擎：路径里同样会有 < 和 &（用户名里就可能有）。
	html := decodeNotice(t, noticePage("test", false, "T & T", "<not a tag>", "C:\\a & b\\log"))
	for _, want := range []string{"T &amp; T", "&lt;not a tag&gt;", "C:\\a &amp; b\\log"} {
		if !strings.Contains(html, want) {
			t.Fatalf("text handed to noticePage was not escaped as %q:\n%s", want, html)
		}
	}
}
