package main

// 输出区域数据那一半的账：环形缓冲怎么收行、序号怎么走、以及那一层 handler 会不会把文件那一路
// 弄丢。页面的样子与画笔在 loading.html 里，由 notice_test.go 盯着。

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"testing"
)

func TestLogStoreKeepsLinesInOrder(t *testing.T) {
	s := newLogStore(100)
	s.append("first")
	s.append("second\nthird")
	s.append("fourth\n")
	s.append("\n") // 只有换行：什么都不该收

	want := []string{"first", "second", "third", "fourth"}
	lines, total := s.textsSince(0)
	if total != uint64(len(want)) {
		t.Fatalf("一共该有 %d 行，得到 %d", len(want), total)
	}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("内容不对：%q", lines)
	}
}

// 等级跟着行走完全程：记录 → entry → 页面收到的 JSON。
//
// 这是给那块区域上色的全部依据，而它错了只有眼睛看得出来（一整页全按 info 着色，或者全是红的）。
// 序列化那一步尤其要盯：页面按行取 level，任何一个让它变成空串的改动，都会让颜色悄悄退回去。
func TestLogStoreKeepsTheLevelOfEachLine(t *testing.T) {
	s := newLogStore(100)
	s.appendEntry(entry{Level: levelError, Text: "一行坏消息"})
	s.append("一行普通的") // 没有等级的入口按 info 算
	s.appendEntry(entry{Level: levelError, Text: "第一行\n第二行"})

	entries, _ := s.since(0)
	want := []struct{ level, text string }{
		{levelError, "一行坏消息"},
		{levelInfo, "一行普通的"},
		{levelError, "第一行"},
		{levelError, "第二行"},
	}
	if len(entries) != len(want) {
		t.Fatalf("该有 %d 行，得到 %d 行：%+v", len(want), len(entries), entries)
	}
	for i, w := range want {
		if entries[i].Level != w.level || entries[i].Text != w.text {
			t.Errorf("第 %d 行 = {%q %q}，want {%q %q}", i, entries[i].Level, entries[i].Text, w.level, w.text)
		}
	}

	// 拆行不该把等级弄丢，也不该因为"跟上一行一样"就省略掉它在 JSON 里的样子。
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("序列化：%v", err)
	}
	if !strings.Contains(string(raw), `"l":"ERROR"`) {
		t.Errorf("送到页面的 JSON 里应当带着等级：%s", raw)
	}
}

func TestLogStoreDropsOldestAndKeepsCounting(t *testing.T) {
	s := newLogStore(3)
	for i := 0; i < 5; i++ {
		s.append("line " + strconv.Itoa(i))
	}

	// 序号一路往上走：被挤掉的两行也算在内，否则说不清"读到哪儿了"。
	if got := s.count(); got != 5 {
		t.Fatalf("序号该是 5，得到 %d", got)
	}
	lines, total := s.textsSince(0)
	if total != 5 || strings.Join(lines, "|") != "line 2|line 3|line 4" {
		t.Fatalf("该留最后三行：%q（序号 %d）", lines, total)
	}
}

func TestLogStoreSinceIsForgiving(t *testing.T) {
	s := newLogStore(3)
	for i := 0; i < 5; i++ {
		s.append("line " + strconv.Itoa(i))
	}

	// 跟不上（序号落在被挤掉的那段里）：从最早还留着的那行开始，不报错也不给空。
	if lines, _ := s.textsSince(0); len(lines) != 3 || lines[0] != "line 2" {
		t.Fatalf("跟不上时该给还留着的那段：%q", lines)
	}
	// 取到一半。
	if lines, _ := s.textsSince(3); len(lines) != 2 || lines[0] != "line 3" {
		t.Fatalf("取到一半：%q", lines)
	}
	// 已经取完。
	if lines, _ := s.textsSince(5); len(lines) != 0 {
		t.Fatalf("取完了还给东西：%q", lines)
	}
	// 序号超过总数（缓冲清过、页面还记着旧序号）。
	if lines, _ := s.textsSince(9); len(lines) != 0 {
		t.Fatalf("序号超前该给空：%q", lines)
	}
}

// 那一层 handler 是"文件不变、多分一份出去"：文件那一路仍旧拿到同一条记录，缓冲也拿到一行。
func TestLogSinkKeepsTheFileRouteIntact(t *testing.T) {
	var file bytes.Buffer
	base := slog.NewTextHandler(&file, &slog.HandlerOptions{Level: slog.LevelInfo})

	saved := logBuffer
	defer func() { logBuffer = saved }()
	logBuffer = newLogStore(100)

	stop := installLogSink(base)
	defer stop()

	slog.Info("the game is ready", "address", "http://127.0.0.1:3000/")

	// 文件那一路：还是原来那套文本格式。
	if !strings.Contains(file.String(), "msg=\"the game is ready\"") {
		t.Fatalf("文件那一路没有拿到这条记录：%q", file.String())
	}
	if !strings.Contains(file.String(), "address=http://127.0.0.1:3000/") {
		t.Fatalf("文件那一路丢了字段：%q", file.String())
	}

	// 缓冲那一路：消息后面接着记录的字段，用的是 slog 自己的写法。
	lines, total := logBuffer.textsSince(0)
	if total != 1 {
		t.Fatalf("缓冲里该有一行，得到 %d 行：%q", total, lines)
	}
	if !strings.Contains(lines[0], "the game is ready") || !strings.Contains(lines[0], "address=http://127.0.0.1:3000/") {
		t.Fatalf("缓冲里那一行不对：%q", lines[0])
	}
}
