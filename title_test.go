package main

// 标题栏那一行怎么拼。它出错的方式很安静——屏幕上只是某一句没出现、或者闪一下就没——所以这里把
// 四种组合逐一钉住，尤其是"上游还没问到"那一种。

import (
	"strings"
	"testing"
)

func TestWindowTitle(t *testing.T) {
	const (
		local  = "bdb0765c5579c430cbe1ef79cf3d62831bb062a7"
		other  = "8f3a1c2de0000000000000000000000000000000"
		shortL = "bdb0765"
		shortO = "8f3a1c2"
	)
	for _, c := range []struct {
		name   string
		local  string
		remote string
		known  bool
		want   string
	}{
		{
			name: "本机 hash 还没有：只写程序名",
			want: appName,
		},
		{
			name:  "上游还没问到：不宣称新版",
			local: local,
			want:  appName + " - " + shortL,
		},
		{
			name:   "问到了，就是同一版",
			local:  local,
			remote: local,
			known:  true,
			want:   appName + " - " + shortL,
		},
		{
			name:   "问到了，不一样",
			local:  local,
			remote: other,
			known:  true,
			want:   appName + " - " + shortL + " - 检测到新版本 " + shortO + " （可在托盘菜单中更新）",
		},
		{
			// 问到了，但那次问到的正是"空"（理论上不该发生）：也不该说新版。
			name:  "问到了，却是个空 hash",
			local: local,
			known: true,
			want:  appName + " - " + shortL,
		},
	} {
		if got := windowTitle(c.local, c.remote, c.known); got != c.want {
			t.Errorf("%s: windowTitle = %q, want %q", c.name, got, c.want)
		}
	}
}

// 托盘图标的悬停提示：程序名 + 这一份的版本。它是版本号唯一露脸的地方，所以两头的写法都钉住。
func TestTrayName(t *testing.T) {
	for _, c := range []struct {
		version string
		want    string
	}{
		{"v0.3.0", "卫戍协议：盟约 启动器 - v0.3.0"},
		{"dev", "卫戍协议：盟约 启动器 - dev"},
		{"", "卫戍协议：盟约 启动器"},
	} {
		if got := trayName(c.version); got != c.want {
			t.Errorf("trayName(%q) = %q, want %q", c.version, got, c.want)
		}
	}
}

// 取回新源码之后，标题栏要跟着换。
//
// 这条盯的是一个真出过的毛病：更新把源码换掉了，而标题栏里本机那一半是**启动时**读的——于是它一直
// 显示旧的那个 hash；又因为上游那一半没动，它还指着一个刚被装上的 commit 说「检测到新版本」，让人
// 再去更新一次。屏幕上没有任何东西会说这是错的，所以只能用测试钉住。
func TestTitleAfterUpdate(t *testing.T) {
	const (
		before = "bdb0765c5579c430cbe1ef79cf3d62831bb062a7"
		after  = "8f3a1c2de0000000000000000000000000000000"
		later  = "11223344556677889900aabbccddeeff00112233"
	)
	// 这几个全局别的测试不读，但留着一份状态总是不好的。
	t.Cleanup(func() {
		localRevisionHash.Store("")
		upstreamRevisionID.Store("")
		upstreamKnown.Store(false)
	})

	title := func() string {
		local, _ := localRevisionHash.Load().(string)
		remote, _ := upstreamRevisionID.Load().(string)
		return windowTitle(local, remote, upstreamKnown.Load())
	}

	// 更新之前的实情：本机还在旧的那个上，上游已经到了新的那个。
	localRevisionHash.Store(before)
	upstreamRevisionID.Store(after)
	upstreamKnown.Store(true)
	if got := title(); !strings.Contains(got, "检测到新版本") {
		t.Fatalf("前提不成立：更新之前标题该说「检测到新版本」，得到 %q", got)
	}

	// 取回了新源码：本机那一半换成新的，而旧的上游答案**作废**——不是换成一个新答案，是作废。
	adoptRevision(after)
	local, _ := localRevisionHash.Load().(string)
	remote, _ := upstreamRevisionID.Load().(string)
	if local != after {
		t.Errorf("本机那一半该换成 %s，得到 %q", shortHash(after), local)
	}
	if remote != "" || upstreamKnown.Load() {
		t.Errorf("换源码之后旧的上游答案该作废：remote=%q known=%v", remote, upstreamKnown.Load())
	}
	got := title()
	if strings.Contains(got, "检测到新版本") {
		t.Errorf("刚换完源码还说有新版本：%q", got)
	}
	if !strings.Contains(got, shortHash(after)) {
		t.Errorf("标题里该有新的那个 hash %s：%q", shortHash(after), got)
	}

	// 重新问过之后：上游就在这一版上 → 仍旧不说新版；上游又往前走了 → 照说。
	upstreamRevisionID.Store(after)
	upstreamKnown.Store(true)
	if got := title(); strings.Contains(got, "检测到新版本") {
		t.Errorf("上游就是这一版，不该说新版：%q", got)
	}
	upstreamRevisionID.Store(later)
	upstreamKnown.Store(true)
	if got := title(); !strings.Contains(got, "检测到新版本") {
		t.Errorf("上游确实又往前走了，该说新版：%q", got)
	}
}
