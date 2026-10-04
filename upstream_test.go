package main

// commit hash 那几件小事：怎么缩短、怎么算同一个、怎么在数据目录里存和读，以及 atom feed 那个后备
// 怎么解析。窗口标题里那两段 hash 就靠它们，而出错的方式很安静——标题栏上只是少了一段或者多了一段，
// 没有人会为此去翻日志。

import (
	"os"
	"path/filepath"
	"testing"
)

// withDataDir 把本程序写自己东西的地方换到一个临时目录，测完还原。
func withDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	saved := dataDirOverride
	dataDirOverride = dir
	t.Cleanup(func() { dataDirOverride = saved })
	return dir
}

func TestShortHash(t *testing.T) {
	const full = "bdb0765c5579c430cbe1ef79cf3d62831bb062a7"
	for _, c := range []struct{ in, want string }{
		{full, "bdb0765"},
		{"abc", "abc"},
		{"", ""},
	} {
		if got := shortHash(c.in); got != c.want {
			t.Errorf("shortHash(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSameRevision(t *testing.T) {
	const a = "bdb0765c5579c430cbe1ef79cf3d62831bb062a7"
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{a, a, true},
		// hex 的大小写是写法问题，不是另一个 commit。
		{a, "BDB0765C5579C430CBE1EF79CF3D62831BB062A7", true},
		{a, "8f3a1c2de0000000000000000000000000000000", false},
		// 空的那一侧是"不知道"，不是"一样"。
		{"", a, false},
		{a, "", false},
		{"", "", false},
	} {
		if got := sameRevision(c.a, c.b); got != c.want {
			t.Errorf("sameRevision(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSaveAndReadRevision(t *testing.T) {
	withDataDir(t)

	// 还没有这个文件时：不是错误，只是"不知道"。
	if got := localRevision(); got != "" {
		t.Fatalf("什么都还没记时 localRevision() = %q, want 空", got)
	}

	const hash = "bdb0765c5579c430cbe1ef79cf3d62831bb062a7"
	if err := saveRevision(hash); err != nil {
		t.Fatalf("saveRevision: %v", err)
	}
	if got := localRevision(); got != hash {
		t.Fatalf("localRevision() = %q, want %q", got, hash)
	}

	// 文件坏了：也只是"不知道"，不该崩，也不该编一个出来。
	path := filepath.Join(dataDirOverride, "game-source.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := localRevision(); got != "" {
		t.Errorf("坏文件时 localRevision() = %q, want 空", got)
	}

	// 空 hash 没什么可记的，该当面拒绝。
	if err := saveRevision(""); err == nil {
		t.Error("saveRevision(\"\") 该报错")
	}
}

// atom feed 那个后备：从一条 entry 的 <id> 里把 hash 抠出来。
func TestAtomToken(t *testing.T) {
	const feed = `<?xml version="1.0"?>
<feed>
  <entry>
    <id>tag:github.com,2008:Grit::Commit/bdb0765c5579c430cbe1ef79cf3d62831bb062a7</id>
    <title>雷达扫描方向反了</title>
  </entry>
  <entry>
    <id>tag:github.com,2008:Grit::Commit/8f3a1c2de0000000000000000000000000000000</id>
  </entry>
</feed>`

	match := atomToken.FindStringSubmatch(feed)
	if match == nil {
		t.Fatal("没找到 commit hash")
	}
	if match[1] != "bdb0765c5579c430cbe1ef79cf3d62831bb062a7" {
		t.Errorf("取到的是 %q，want 最新那条", match[1])
	}
	if atomToken.MatchString("feed 里没有 hash") {
		t.Error("没有 hash 的文本不该命中")
	}
}
