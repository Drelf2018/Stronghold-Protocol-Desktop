// Package artwork draws the embedded icon at whatever size is asked for.
//
// It is deliberately free of any platform, and not part of package main (which is Windows-only):
// the release workflow generates the resource icon inside a Linux container. An application wants
// more than one size - the .exe resource carries eight, a window asks for two more at run time -
// so the scaling lives here, where those sizes are known.

package artwork

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/png"
	"math"

	ico "github.com/biessek/golang-ico"
	"golang.org/x/image/draw"
)

// The icon source is the drawing the person who asked for this launcher handed over.
//
// A bitmap, unlike the vector this package used to carry: every size is a resample of the one
// picture rather than a fresh drawing, so the largest size it can be sharp at is the size of the
// file (75x75 here) - the 256 that Explorer draws a large icon at is an enlargement, and it looks
// like one. The file name is hard-coded rather than globbed - a pattern like icon.* allows exactly
// one match, and a second image dropped into this directory would break the build, silently.
//
//go:embed stronghold.png
var iconData []byte

// iconAt draws the embedded icon at size x size.
//
// A bitmap is scaled to the size, keeping its shape: a square png asked for at 16 comes back as
// 16, and one that is not square is letterboxed rather than stretched out of shape.
func iconAt(size int) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(iconData))
	if err != nil {
		return nil, fmt.Errorf("decode the embedded icon: %w", err)
	}
	if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
		img = fit(img, size)
	}
	return img, nil
}

// IconICO encodes the embedded icon at size as a one-image .ico.
//
// That is the form everything downstream wants: the entries of the multi-size .ico the resource
// carries are these images, and the image inside one of them, with the directory stripped off, is
// what Windows takes for an icon resource.
func IconICO(size int) ([]byte, error) {
	img, err := iconAt(size)
	if err != nil {
		return nil, err
	}
	// 编一次，为的是拿到像素：居中是在像素上做的，而像素要画出来才有。
	first, err := encodeICO(img)
	if err != nil {
		return nil, fmt.Errorf("draw the embedded icon at %dpx: %w", size, err)
	}
	bitmap, err := ico.Decode(bytes.NewReader(first))
	if err != nil {
		return nil, fmt.Errorf("read back the %dpx icon: %w", size, err)
	}
	return encodeICO(centre(bitmap))
}

// encodeICO turns an image into a one-image .ico.
func encodeICO(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := ico.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// centre puts the artwork in the middle of its canvas.
//
// The drawing is not assumed to be centred in its own file: the drawn pixels are measured and the
// drawing is moved, rather than the source being trusted. Nothing is scaled or cropped: the mark
// keeps its size and only the space around it is divided evenly.
func centre(src image.Image) image.Image {
	b := src.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			// 用阈值而不是「不透明就算数」：最淡的那一层抗锯齿并不属于图形本身，
			// 让它来做决定的话，图形会因为边缘取样方式不同而挪动一个像素。
			if _, _, _, a := src.At(x, y).RGBA(); a > 0x1000 {
				minX, minY = min(minX, x), min(minY, y)
				maxX, maxY = max(maxX, x), max(maxY, y)
			}
		}
	}
	if maxX < minX {
		return src // 什么都没画出来，也就没什么可居中的
	}
	// 目标是「图形中心落在画布中心」：先算它该在哪，再算出要挪多少。直接对
	// 「多出来的空白取一半」也行，但那个减法会出现负数，而 Go 的整数除法对负数向零
	// 截断——16 像素那种小尺寸就会差一个像素。
	wantX := b.Min.X + (b.Dx()-(maxX-minX+1))/2
	wantY := b.Min.Y + (b.Dy()-(maxY-minY+1))/2
	dx, dy := wantX-minX, wantY-minY
	if dx == 0 && dy == 0 {
		return src
	}
	dst := image.NewRGBA(b)
	draw.Draw(dst, b.Add(image.Pt(dx, dy)), src, b.Min, draw.Src)
	return dst
}

// fit draws img into a size x size square, keeping its shape and centring what is left over.
//
// CatmullRom rather than a cheaper kernel: the source is 75 pixels square, so every size below
// that is a reduction by a large factor, and this one widens its kernel to match instead of
// sampling a few pixels and hoping.
func fit(img image.Image, size int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	src := img.Bounds()
	if src.Empty() {
		return dst
	}
	scale := math.Min(float64(size)/float64(src.Dx()), float64(size)/float64(src.Dy()))
	w := int(math.Round(float64(src.Dx()) * scale))
	h := int(math.Round(float64(src.Dy()) * scale))
	at := image.Rect((size-w)/2, (size-h)/2, (size-w)/2+w, (size-h)/2+h)
	draw.CatmullRom.Scale(dst, at, img, src, draw.Over, nil)
	return dst
}

// Sizes are the edges a multi-size .ico carries, smallest first. The tray is the one that matters
// most: it asks the icon for SM_CXSMICON - 24 at 150% - and the library that hands it over looks
// for an exact match before it will scale anything. An .ico holding the size being asked for is
// handed over as it is, which is the whole difference between a sharp icon and a shrunken one. 16
// is the title bar next to it, 32 and 48 the taskbar and Alt+Tab, and 256 the size Explorer draws
// a large icon at.
var Sizes = []int{16, 20, 24, 32, 40, 48, 64, 256}

// MultiSizeICO packs the embedded icon, drawn once per size, into one .ico.
//
// Every size comes out of IconICO as a single-image .ico, so the images are already in the form an
// entry of a multi-size .ico wants; what is left is the directory in front of them. That directory
// is written here, and each entry is copied from the single-image .ico it came from with only its
// size and offset rewritten: the entry is what says whether the image inside it is a bitmap or a
// png, its planes and its bit depth, and there is nothing to gain by inventing any of that here.
func MultiSizeICO(sizes []int) ([]byte, error) {
	type image struct {
		dir  []byte // the 16 byte directory entry, as written
		data []byte // the image it points at
	}
	images := make([]image, 0, len(sizes))
	for _, size := range sizes {
		blob, err := IconICO(size)
		if err != nil {
			return nil, err
		}
		dir, data, err := IconEntry(blob, size)
		if err != nil {
			return nil, err
		}
		images = append(images, image{dir, data})
	}

	var out bytes.Buffer
	// bytes.Buffer 从不拒绝写入，所以这几行不会失败。
	_ = binary.Write(&out, binary.LittleEndian, uint16(0)) // 保留字段
	_ = binary.Write(&out, binary.LittleEndian, uint16(1)) // 1 是图标，2 是光标
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(images)))

	offset := 6 + 16*len(images) // 头部加上每张图一条目录项
	for _, img := range images {
		entry := bytes.Clone(img.dir)
		binary.LittleEndian.PutUint32(entry[8:12], uint32(len(img.data)))
		binary.LittleEndian.PutUint32(entry[12:16], uint32(offset))
		out.Write(entry)
		offset += len(img.data)
	}
	for _, img := range images {
		out.Write(img.data)
	}
	return out.Bytes(), nil
}

// IconEntry splits a single-image .ico into its 16 byte directory entry and the image it points
// at, checking that it really is one image of the edge that was asked for.
//
// The width in a directory entry is one byte wide, so 256 is written as 0: that is the value to
// expect back, not 256.
func IconEntry(data []byte, size int) ([]byte, []byte, error) {
	const header = 6 + 16 // ICONDIR 加上一条 ICONDIRENTRY
	if len(data) < header {
		return nil, nil, fmt.Errorf("%d bytes is too short for a .ico", len(data))
	}
	reserved := binary.LittleEndian.Uint16(data[0:2])
	kind := binary.LittleEndian.Uint16(data[2:4])
	count := binary.LittleEndian.Uint16(data[4:6])
	if reserved != 0 || kind != 1 || count != 1 {
		return nil, nil, fmt.Errorf("not a one-image .ico: reserved=%d type=%d images=%d", reserved, kind, count)
	}
	dir := data[6:header]
	if got := int(dir[0]); got != size%256 {
		return nil, nil, fmt.Errorf("the directory says %d pixels, %d were asked for", got, size)
	}
	length := binary.LittleEndian.Uint32(dir[8:12])
	offset := binary.LittleEndian.Uint32(dir[12:16])
	if offset != header || int(length) != len(data)-header {
		return nil, nil, fmt.Errorf("image at %d for %d bytes, in a %d byte file", offset, length, len(data))
	}
	return dir, data[header:], nil
}
