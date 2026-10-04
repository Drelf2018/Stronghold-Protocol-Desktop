package main

// Command genicon writes rsrc.syso, the icon resource the built .exe carries:
//
//	go run ./internal/genicon [arch]
//
// A program rather than a test, because the release workflow runs it inside a Linux container,
// where package main cannot be compiled. arch is the architecture the resource is FOR and has
// to match the workflow's goarch - runtime.GOARCH would say linux/amd64 there whatever is being
// built. It defaults to amd64, the only architecture this app is released for (rsrc also knows
// 386, arm and arm64). The file lands in the current directory, which has to be the package
// being built.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akavel/rsrc/rsrc"
	ico "github.com/biessek/golang-ico"

	"github.com/Drelf2018/Stronghold-Protocol-Launcher/internal/artwork"
)

// sysoName is the name the toolchain looks for.
const sysoName = "rsrc.syso"

func main() {
	arch := "amd64"
	if len(os.Args) > 1 {
		arch = os.Args[1]
	}

	blob, err := artwork.MultiSizeICO(artwork.Sizes)
	if err != nil {
		fail(err)
	}
	// 目录是手写出来的，所以按 Windows 读它的方式读回来验证：要了几个尺寸就有几张图。
	decoded, err := ico.DecodeAll(bytes.NewReader(blob))
	if err != nil {
		fail(err)
	}
	if len(decoded) != len(artwork.Sizes) {
		fail(fmt.Errorf("the .ico holds %d images, wanted %d", len(decoded), len(artwork.Sizes)))
	}
	for i, img := range decoded {
		if b := img.Bounds(); b.Dx() != artwork.Sizes[i] || b.Dy() != artwork.Sizes[i] {
			fail(fmt.Errorf("image %d is %dx%d, wanted %d", i, b.Dx(), b.Dy(), artwork.Sizes[i]))
		}
	}

	// rsrc 是从磁盘上读 .ico 的，所以得先落一个文件；落在临时目录里，免得在仓库里留渣。
	dir, err := os.MkdirTemp("", "genicon")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(dir)
	icoPath := filepath.Join(dir, "icon.ico")
	if err := os.WriteFile(icoPath, blob, 0o644); err != nil {
		fail(err)
	}
	// 第二个参数是清单，给空串就是没有：这个可执行文件只拿到图标，别的什么都不加。
	if err := rsrc.Embed(sysoName, arch, "", icoPath); err != nil {
		fail(err)
	}
	written, err := os.Stat(sysoName)
	if err != nil {
		fail(err)
	}
	fmt.Printf("%s: %d bytes for %s, from %d sizes of the embedded icon\n", sysoName, written.Size(), arch, len(artwork.Sizes))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genicon:", err)
	os.Exit(1)
}
