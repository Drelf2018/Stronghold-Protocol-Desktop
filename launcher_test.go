package main

// The one piece of the launcher that can be checked without a network, a Node.js or a running
// server: how a version is read, and the names a tar brings and the paths they are allowed to
// become. The archive is unpacked from bytes here, so the check does not depend on GitHub.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseNodeMajor(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"v24.16.0", 24, true},
		{"v22.0.0", 22, true},
		{"  v22.11.0  ", 22, true},
		{"v8.17.0", 8, true},
		{"not a version", 0, false},
		{"", 0, false},
	} {
		got, err := parseNodeMajor(c.in)
		if !c.ok {
			if err == nil {
				t.Errorf("parseNodeMajor(%q) = %d, want an error", c.in, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("parseNodeMajor(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
}

func TestArchivePath(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"Stronghold-Protocol-master/package.json", "package.json", true},
		{"Stronghold-Protocol-master/server/index.js", filepath.Join("server", "index.js"), true},
		{"Stronghold-Protocol-master/server", "server", true},
		{"Stronghold-Protocol-master\\server\\index.js", filepath.Join("server", "index.js"), true},
		{"Stronghold-Protocol-master/", "", false},
		{"Stronghold-Protocol-master", "", false},
		{"package.json", "", false},
		{"../escape.txt", "", false},
		{"Stronghold-Protocol-master/../../escape.txt", "", false},
		{"/etc/passwd", "", false},
	} {
		got, ok := archivePath(c.in)
		if ok != c.ok {
			t.Errorf("archivePath(%q) landed = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("archivePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 日志的轮转：别人还开着这个文件的时候，它必须让路——接着写同一份，而不是失败，更不是跑去
// %TEMP% 另开一份（那正是多开时会悄悄出的事）。
func TestOpenAtDoesNotRotateAFileInUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")

	held, err := openAt(path)
	if err != nil {
		t.Fatalf("openAt: %v", err)
	}
	// 撑过上限。写而不是 Truncate：这个句柄是以追加权限（FILE_APPEND_DATA）打开的，而截断需要
	// GENERIC_WRITE，Windows 会拒绝——这也顺便说明日志句柄写不坏别的东西。
	if _, err := held.Write(make([]byte, logLimit+1)); err != nil {
		t.Fatalf("write: %v", err)
	}

	second, err := openAt(path)
	if err != nil {
		t.Fatalf("别人开着的时候，第二份打不开日志：%v", err)
	}
	second.Close()
	held.Close()
	if _, err := os.Stat(path + ".old"); err == nil {
		t.Error("有人正开着的时候，日志还是被轮转走了")
	}

	// 都放手之后：这一次该轮转了。
	third, err := openAt(path)
	if err != nil {
		t.Fatalf("openAt: %v", err)
	}
	third.Close()
	if _, err := os.Stat(path + ".old"); err != nil {
		t.Errorf("没人开着的时候应当轮转，但 %s 不在：%v", path+".old", err)
	}
}

// 准备锁必须真的排他。文件锁属于句柄而不是线程，所以在同一个进程里用两条 goroutine 拿它，形状
// 和两份实例之间是一样的——这正是可以在这里测的原因。
func TestLockFileIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prepare.lock")
	release, err := lockFile(path)
	if err != nil {
		t.Fatalf("lockFile: %v", err)
	}

	second := make(chan error, 1)
	go func() {
		other, err := lockFile(path)
		if err == nil {
			other()
		}
		second <- err
	}()

	// 第一份还拿着，第二份不该进来。
	select {
	case err := <-second:
		t.Fatalf("第一份还拿着，第二份就拿到了：%v", err)
	case <-time.After(500 * time.Millisecond):
	}

	// 放开之后它必须拿到——只拿不放，多开就成了一次只能开一个。
	release()
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("放开之后第二份仍然拿不到：%v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("放开之后第二份还是没拿到")
	}
}

// 「输入加入链接」那一项把三种输入归一成窗口能开的地址，而这一段不碰窗口、不碰网络，可以真测。
// 规则要和游戏自己的大厅一致（public/js/screens/lobby.js 的 normalizeCode）。
func TestJoinURL(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"abcd", "http://127.0.0.1:3000/?room=ABCD"},
		{"AB12", "http://127.0.0.1:3000/?room=AB12"},
		{" abcd ", "http://127.0.0.1:3000/?room=ABCD"},
		{"http://192.168.1.5:3000/?room=ABCD", "http://192.168.1.5:3000/?room=ABCD"},
		{"192.168.1.5:3000/?room=ABCD", "http://192.168.1.5:3000/?room=ABCD"},
		{"https://example.com/play", "https://example.com/play"},
		{"about:blank", "about:blank"},
		{"", ""},
	} {
		if got := joinURL(c.in); got != c.want {
			t.Errorf("joinURL(%q) = %q，应当是 %q", c.in, got, c.want)
		}
	}
}

func TestReadGameVersion(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(body), 0o644); err != nil {
			t.Fatalf("write package.json: %v", err)
		}
	}

	// 更新游戏只靠这一行知道"到底更没更"，所以三种读不出来的情况都必须是 "?"，而不是空串或崩掉。
	if got := readGameVersion(dir); got != "?" {
		t.Errorf("no package.json yet: %q, want %q", got, "?")
	}
	write("{\"version\":\"0.1.1\"}")
	if got := readGameVersion(dir); got != "0.1.1" {
		t.Errorf("readGameVersion = %q, want %q", got, "0.1.1")
	}
	write("{not json")
	if got := readGameVersion(dir); got != "?" {
		t.Errorf("unparsable: %q, want %q", got, "?")
	}
	write("{\"name\":\"x\"}")
	if got := readGameVersion(dir); got != "?" {
		t.Errorf("no version field: %q, want %q", got, "?")
	}
}

// tarball builds the smallest archive that has the shape this reads: one top-level folder, files
// under it, and one name that tries to climb out of it.
func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write the header for %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("write the body of %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close the tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close the gzip: %v", err)
	}
	return buf.Bytes()
}

func TestUnpackArchive(t *testing.T) {
	dir := t.TempDir()
	blob := tarball(t, map[string]string{
		"Stronghold-Protocol-master/package.json":     "one\n",
		"Stronghold-Protocol-master/server/index.js":  "// the server\n",
		"Stronghold-Protocol-master/../../escape.txt": "never written\n",
	})

	files, err := unpackArchive(bytes.NewReader(blob), dir)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if files != 2 {
		t.Errorf("wrote %d files, want 2: the name that climbs out is not one of them", files)
	}
	if body, err := os.ReadFile(filepath.Join(dir, "package.json")); err != nil || string(body) != "one\n" {
		t.Errorf("package.json = %q, %v; want %q", body, err, "one\n")
	}
	if body, err := os.ReadFile(filepath.Join(dir, "server", "index.js")); err != nil || string(body) != "// the server\n" {
		t.Errorf("server/index.js = %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Error("a name that climbs out of the archive was written next to the game")
	}

	// 解第二次是覆盖，不是重来：改掉的那个文件要换成新的，别的文件要原样留着——素材和
	// node_modules 就靠这一点活过每一次更新。
	second := tarball(t, map[string]string{"Stronghold-Protocol-master/package.json": "two\n"})
	if _, err := unpackArchive(bytes.NewReader(second), dir); err != nil {
		t.Fatalf("unpack again: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(dir, "package.json")); err != nil || string(body) != "two\n" {
		t.Errorf("after the second unpack package.json = %q, %v; want %q", body, err, "two\n")
	}
	if _, err := os.Stat(filepath.Join(dir, "server", "index.js")); err != nil {
		t.Errorf("the second unpack removed a file it did not carry: %v", err)
	}
}
