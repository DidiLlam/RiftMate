//go:build windows

package main

import (
	"image"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"
	"sync"
	"unsafe"
)

// The overlay's pictures (champion, spells, ult, Zhonya): Data Dragon's own icons, fetched once in the background,
// shrunk to the chip size and kept as Windows bitmaps (plain and darkened for a running timer).

var (
	pCreateDIBSection = gdi32.NewProc("CreateDIBSection")
	ovImgMu           sync.Mutex
	ovImgs            = map[string]*image.RGBA{} // url -> picture (nil while loading or if it failed)
	ovImgBusy         = map[string]bool{}
	ovBmps            = map[string]uintptr{} // url|size|dark -> HBITMAP (window thread only)
)

func ovIconOK(u string) bool {
	return strings.HasPrefix(u, "https://ddragon.leagueoflegends.com/cdn/") && len(u) < 300
}

// ovImg: the picture if it's here; otherwise starts fetching it and the next redraw has it
func ovImg(u string) *image.RGBA {
	if !ovIconOK(u) {
		return nil
	}
	ovImgMu.Lock()
	defer ovImgMu.Unlock()
	if im, ok := ovImgs[u]; ok {
		return im
	}
	if !ovImgBusy[u] {
		ovImgBusy[u] = true
		go func() {
			var out *image.RGBA
			if r, err := riotHTTP.Get(u); err == nil {
				if r.StatusCode == 200 {
					if im, _, err := image.Decode(io.LimitReader(r.Body, 2<<20)); err == nil {
						out = image.NewRGBA(im.Bounds())
						draw.Draw(out, out.Bounds(), im, im.Bounds().Min, draw.Src)
					}
				}
				r.Body.Close()
			}
			ovImgMu.Lock()
			if len(ovImgs) > 200 {
				ovImgs = map[string]*image.RGBA{}
			}
			ovImgs[u] = out
			delete(ovImgBusy, u)
			ovImgMu.Unlock()
		}()
	}
	return nil
}

// ovBitmap: the picture at n×n pixels as a Windows bitmap (area average, so small icons stay sharp)
func ovBitmap(u string, n int32, dark bool) uintptr {
	key := u + "|" + string(rune(n)) + map[bool]string{true: "d", false: ""}[dark]
	if b, ok := ovBmps[key]; ok {
		return b
	}
	im := ovImg(u)
	if im == nil || n <= 0 {
		return 0
	}
	type bih struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPels, YPels           int32
		ClrUsed, ClrImportant  uint32
	}
	hdr := bih{Width: n, Height: -n, Planes: 1, BitCount: 32}
	hdr.Size = uint32(unsafe.Sizeof(hdr))
	var bits unsafe.Pointer
	h, _, _ := pCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&hdr)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if h == 0 || bits == nil {
		return 0
	}
	px := unsafe.Slice((*byte)(bits), int(n*n*4))
	b := im.Bounds()
	sw, sh := b.Dx(), b.Dy()
	for y := 0; y < int(n); y++ {
		y0, y1 := y*sh/int(n), (y+1)*sh/int(n)
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < int(n); x++ {
			x0, x1 := x*sw/int(n), (x+1)*sw/int(n)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, c int
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					o := im.PixOffset(b.Min.X+xx, b.Min.Y+yy)
					r += int(im.Pix[o])
					g += int(im.Pix[o+1])
					bl += int(im.Pix[o+2])
					c++
				}
			}
			r, g, bl = r/c, g/c, bl/c
			if dark { // a running timer: the icon dimmed and grey-ish, the countdown goes on top
				l := (r*3 + g*6 + bl) / 10
				r, g, bl = (r+l)*3/10, (g+l)*3/10, (bl+l)*3/10
			}
			if r <= 1 && g <= 1 && bl <= 1 { // never the see-through colour
				r, g, bl = 2, 2, 2
			}
			o := (y*int(n) + x) * 4
			px[o], px[o+1], px[o+2], px[o+3] = byte(bl), byte(g), byte(r), 255
		}
	}
	if len(ovBmps) > 300 {
		for k, v := range ovBmps {
			pDeleteObject.Call(v)
			delete(ovBmps, k)
		}
	}
	ovBmps[key] = h
	return h
}

// ovBlit draws the bitmap at (x, y) into dc
func ovBlit(dc, bmp uintptr, x, y, n int32) {
	src, _, _ := pCreateCompatibleDC.Call(dc)
	old, _, _ := pSelectObject.Call(src, bmp)
	pBitBlt.Call(dc, uintptr(x), uintptr(y), uintptr(n), uintptr(n), src, 0, 0, 0x00CC0020)
	pSelectObject.Call(src, old)
	pDeleteDC.Call(src)
}
