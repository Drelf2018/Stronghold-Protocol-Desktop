package main

// 输出区域数据那一半的账：环形缓冲怎么收行、序号怎么走、以及那一层 handler 会不会把文件那一路
// 弄丢。页面的样子与画笔在 loading.html 里，由 notice_test.go 盯着。

import (
	"bytes"
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
	lines, total := s.since(0)
	if total != uint64(len(want)) {
		t.Fatalf("一共该有 %d 行，得到 %d", len(want), total)
	}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("内容不对：%q", lines)
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
	lines, total := s.since(0)
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
	if lines, _ := s.since(0); len(lines) != 3 || lines[0] != "line 2" {
		t.Fatalf("跟不上时该给还留着的那段：%q", lines)
	}
	// 取到一半。
	if lines, _ := s.since(3); len(lines) != 2 || lines[0] != "line 3" {
		t.Fatalf("取到一半：%q", lines)
	}
	// 已经取完。
	if lines, _ := s.since(5); len(lines) != 0 {
		t.Fatalf("取完了还给东西：%q", lines)
	}
	// 序号超过总数（缓冲清过、页面还记着旧序号）。
	if lines, _ := s.since(9); len(lines) != 0 {
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
	lines, total := logBuffer.since(0)
	if total != 1 {
		t.Fatalf("缓冲里该有一行，得到 %d 行：%q", total, lines)
	}
	if !strings.Contains(lines[0], "the game is ready") || !strings.Contains(lines[0], "address=http://127.0.0.1:3000/") {
		t.Fatalf("缓冲里那一行不对：%q", lines[0])
	}
}
