package main

// The packing step, checked without writing into the repository: the .ico is built in a
// temporary directory, rsrc embeds it there, and the file is read back to see that something
// came out.
//
// It is the same work main does when it is run; what the test adds is that go test does it
// too, and on any platform - the generator itself runs in a Linux container at release time,
// where the module's own package main cannot be compiled at all.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/akavel/rsrc/rsrc"

	"github.com/Drelf2018/Stronghold-Protocol-Launcher/internal/artwork"
)

func TestEmbedIcon(t *testing.T) {
	blob, err := artwork.MultiSizeICO(artwork.Sizes)
	if err != nil {
		t.Fatalf("build a multi-size .ico: %v", err)
	}
	// rsrc 只认磁盘上的文件，所以先落一份；落在临时目录里，免得在仓库里留渣。
	dir := t.TempDir()
	icoPath := filepath.Join(dir, "icon.ico")
	if err := os.WriteFile(icoPath, blob, 0o644); err != nil {
		t.Fatalf("write %s: %v", icoPath, err)
	}
	// 第二个参数是清单，给空串就是没有：这个可执行文件只拿到图标，别的什么都不加。
	// 架构用运行测试的这台机器的：只有生成器需要把目标架构说清楚（容器里 runtime.GOARCH 是
	// linux/amd64，而不是要发布的那一个）。
	out := filepath.Join(dir, sysoName)
	if err := rsrc.Embed(out, runtime.GOARCH, "", icoPath); err != nil {
		t.Fatalf("embed icon in %s: %v", sysoName, err)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read back %s: %v", sysoName, err)
	}
	if len(written) == 0 {
		t.Fatalf("%s is empty", sysoName)
	}
	t.Logf("%s: %d bytes for %s, from %d sizes of the embedded icon", sysoName, len(written), runtime.GOARCH, len(artwork.Sizes))
}
