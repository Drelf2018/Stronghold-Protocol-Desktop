package artwork_test

// The .ico this package packs, read back the way Windows reads it. The directory in front of
// the images is written by hand (MultiSizeICO), so what it claims - how many images there are
// and how big each one is - is checked here rather than trusted.
//
// It runs on any platform, which is the point: internal/genicon runs inside a Linux container
// at release time, and this is the code it depends on.

import (
	"bytes"
	"testing"

	ico "github.com/biessek/golang-ico"

	"github.com/Drelf2018/Stronghold-Protocol-Launcher/internal/artwork"
)

func TestMultiSizeICO(t *testing.T) {
	blob, err := artwork.MultiSizeICO(artwork.Sizes)
	if err != nil {
		t.Fatalf("build a multi-size .ico: %v", err)
	}
	// 目录是手写出来的，所以按 Windows 读它的方式读回来验证：要了几个尺寸就有几张图，每张
	// 都是那个尺寸。
	decoded, err := ico.DecodeAll(bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("read the .ico back: %v", err)
	}
	if len(decoded) != len(artwork.Sizes) {
		t.Fatalf("the .ico holds %d images, wanted %d", len(decoded), len(artwork.Sizes))
	}
	for i, img := range decoded {
		if b := img.Bounds(); b.Dx() != artwork.Sizes[i] || b.Dy() != artwork.Sizes[i] {
			t.Fatalf("image %d is %dx%d, wanted %d", i, b.Dx(), b.Dy(), artwork.Sizes[i])
		}
	}
}
