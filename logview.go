//go:build windows

package main

// 提示页上那块实时输出区域的数据那一半：日志怎么进来、攒在哪里、按什么节拍送进页面。
//
// 三件事分开，各管各的：
//
//	① 显示   静态：<section id="log-panel" log> 有没有那个属性。Go 渲染时定，CSS 认属性。
//	② 画     运行时：页面自己的那段静态脚本把行追加进 #log-box（见 loading.html）。
//	③ 送     运行时：这个文件。判据仍是那个 log 值，与②无关。
//
// ②不看①，③不等②。这三样曾经缠在一起，缠出来的每一次故障在屏幕上都只表现为"框没了"：
// 一次是面板等数据、数据等面板；一次是显示依赖一段脚本跑没跑。所以这里刻意只做③。

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// logLineLimit 是内存里留多少行。
	logLineLimit = 2000
	// logTail 是一次最多推多少行。页面那边也按这个数量封顶。
	logTail = 200
	// logTick 是推送的节拍：一次 tick 把这期间攒下的行一起送过去，所以输出再密，Eval 的次数
	// 也是每秒几次，而不是每行一次。
	logTick = 150 * time.Millisecond
)

// logStore 是日志行的环形缓冲，也是写日志的人与页面之间唯一的接口。
//
// 它在窗口之前就存在：窗口还没建好时写下的记录也在里面，等页面来取。
type logStore struct {
	mu      sync.Mutex
	lines   []string
	dropped uint64 // 被挤掉的旧行数：序号要能一路往上走，才说得清"读到哪儿了"
	limit   int
}

func newLogStore(limit int) *logStore {
	return &logStore{lines: make([]string, 0, 256), limit: limit}
}

// append 把一段文本按行拆开收下：子进程的输出是按块读进来的，一块里常常有好几个换行。
func (s *logStore) append(text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, strings.Split(text, "\n")...)
	if over := len(s.lines) - s.limit; over > 0 {
		s.lines = append(s.lines[:0:0], s.lines[over:]...)
		s.dropped += uint64(over)
	}
}

// count 是此刻的序号，也就是一共写下过多少行（含已被挤掉的）。
func (s *logStore) count() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped + uint64(len(s.lines))
}

// since 是序号 at 之后的行，以及此刻的序号。
//
// at 落在已经被挤掉的那一段里时，从最早还留着的那行开始——跟不上就尽量给新的，而不是报错或者
// 给空。at 超过总数（缓冲清过、页面还记着旧序号）时给空。
func (s *logStore) since(at uint64) ([]string, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := s.dropped + uint64(len(s.lines))
	if at < s.dropped {
		at = s.dropped
	}
	if at > total {
		at = total
	}
	return s.lines[at-s.dropped:], total
}

// logBuffer 是这一个进程唯一的那份日志缓冲。
var logBuffer = newLogStore(logLineLimit)

// logLine 往缓冲里写一行。日志的 handler 就从这个口进来，所以行只有一种写法。
func logLine(text string) { logBuffer.append(text) }

// logSink 是日志的第二条出口：一条记录落进文件之后，同一份内容也进缓冲。
//
// 它只读不改：写进文件的那一行仍由被包住的那个 handler 决定，这里是从记录里捡出页面需要的东西。
// 所以日志文件不会因为多了这块区域而变样。
type logSink struct {
	base slog.Handler
}

func (h *logSink) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

// Handle 先让文件那一路去写，再把这行送进缓冲。顺序是有意的：文件是这个程序的底稿，屏幕上那块
// 只是它的一层窗——这一步万一出意外，也不该影响到文件里那行。
func (h *logSink) Handle(ctx context.Context, r slog.Record) error {
	err := h.base.Handle(ctx, r)
	logLine(describeRecord(r))
	if err != nil {
		// 文件那一路的报错不会进记录，可它正是最该出现在屏幕上的一行。
		logLine("[" + err.Error() + "]")
	}
	return err
}

// WithAttrs 与 WithGroup 只转发给文件那一路。
//
// 这是有意的，而且有代价：setupLogging 给 base 挂的 pid、以及库自己挂的字段，都不会出现在页面上。
// 理由是那些属性对读日志文件的人有用（多开时哪一行是哪一份），对盯着屏幕看进展的人没用——页面上是
// 消息和记录自己的字段，那些才带着 what=setup / what=game 这样的来处。
func (h *logSink) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &logSink{base: h.base.WithAttrs(attrs)}
}

func (h *logSink) WithGroup(name string) slog.Handler {
	return &logSink{base: h.base.WithGroup(name)}
}

// installLogSink 把 base 包进 logSink 并设为默认日志，同时回答一个拆掉它的函数。
func installLogSink(base slog.Handler) func() {
	slog.SetDefault(slog.New(&logSink{base: base}))
	return func() { slog.SetDefault(slog.New(base)) }
}

// describeRecord 把一条记录写成页面上的一行：消息，后面按 slog 自己的写法接上它的字段。
func describeRecord(r slog.Record) string {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" ")
		b.WriteString(a.String())
		return true
	})
	if r.PC != 0 {
		b.WriteString(" @")
		b.WriteString(filepath.Base(r.Source().Function))
	}
	return b.String()
}

// showLog 是「此刻这一页要不要那块输出区域」。
//
// 它由画那一页的人顺手设置（notice.go 的 noticePage）：Go 在把页面交给窗口之前就知道答案，因为
// 答案是编译期定的——所以推送不必去问页面，也不必等页面说一句话。
var showLog bool

// logCursor 是已经推给页面的行序号，归推送那条 goroutine 自己管。
var logCursor uint64

// attachLogPanel 开始把日志推给页面，直到进程结束。
//
// 推送带着开关（见 pushLog）：这一页没有输出区域时就空转。所以它不必知道调用它的那条 goroutine
// 不是窗口线程——真正碰 WebView2 的那一步在 Dispatch 里。
func attachLogPanel(win *webviewWindow) {
	if win == nil || win.w == nil {
		return
	}
	go func() {
		for range time.Tick(logTick) {
			pushLog(win)
		}
	}()
}

// pushLog 把游标之后的新行送进页面。
func pushLog(win *webviewWindow) {
	if !showLog {
		return
	}
	lines, total := logBuffer.since(logCursor)
	if len(lines) == 0 {
		logCursor = total
		return
	}
	// 一次给太多也没有用：页面只画最近这么多行。
	if len(lines) > logTail {
		lines = lines[len(lines)-logTail:]
	}
	logCursor = total

	raw, err := json.Marshal(lines)
	if err != nil {
		slog.Warn("log panel: cannot encode the lines", "error", err)
		return
	}
	// 页面没有 __spLog（比如游戏页面，那里根本没有这段脚本）时这一句什么都不做，也不会报错。
	win.Dispatch(win.w.Eval, "window.__spLog && window.__spLog("+string(raw)+")")
}
