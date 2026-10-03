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
		// code is what the box at the foot of the page has to carry. The port page carries no
		// box: the thing to do about a taken port is not a path or a command.
		code string
	}{
		{"preparing", preparingPage(), preparingTitle, logPath()},
		{"node", nodeMissingPage(), nodeTitle, "winget install OpenJS.NodeJS.LTS"},
		{"source", sourceFailedPage(), sourceTitle, logPath()},
		{"update", updateFailedPage(), updateTitle, logPath()},
		{"setup", setupFailedPage(), setupTitle, logPath()},
		{"start", startFailedPage(), startTitle, logPath()},
		{"port", portBusyPage(), portTitle, ""},
	} {
		html := decodeNotice(t, c.page)
		if !strings.Contains(html, c.title) {
			t.Errorf("%s page does not carry its title %q", c.name, c.title)
		}
		// 路径与命令都不是抄进文字里的一份，而是现给的：这里查的就是它有没有真的到页面上。
		if has := strings.Contains(html, "<code"); has != (c.code != "") {
			t.Errorf("%s page carries the box = %v, want %v:\n%s", c.name, has, c.code != "", html)
		}
		if c.code != "" && !strings.Contains(html, c.code) {
			t.Errorf("%s page does not carry %q:\n%s", c.name, c.code, html)
		}
	}
}

func TestNoticePageEscapesItsText(t *testing.T) {
	// 标题、正文、路径三者都过一遍引擎：路径里同样会有 < 和 &（用户名里就可能有）。
	html := decodeNotice(t, noticePage("T & T", "<not a tag>", "C:\\a & b\\log"))
	for _, want := range []string{"T &amp; T", "&lt;not a tag&gt;", "C:\\a &amp; b\\log"} {
		if !strings.Contains(html, want) {
			t.Fatalf("text handed to noticePage was not escaped as %q:\n%s", want, html)
		}
	}
}
