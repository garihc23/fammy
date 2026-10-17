// Package photo turns family photos into what Femi's 320x240 screen shows:
// decode (JPEG/PNG), apply EXIF orientation, center-crop to the screen's
// aspect ratio, area-average downscale (bilinear when upscaling), and convert
// to RGB565 little-endian.
package photo

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // register decoders
	_ "image/png"
	"math"
	"os"
)

// Load reads an image file and returns it as w x h RGB565 little-endian.
func Load(path string, w, h int) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	rgba := Orient(ToRGBA(img), EXIFOrientation(data))
	return RGB565(Fit(rgba, w, h)), nil
}

// ToRGBA converts any image to an *image.RGBA whose bounds start at 0,0.
func ToRGBA(img image.Image) *image.RGBA {
	b := img.Bounds()
	if r, ok := img.(*image.RGBA); ok && b.Min == (image.Point{}) {
		return r
	}
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out
}

type tap struct {
	idx int
	w   float32
}

// axisWeights maps each of dst output positions to weighted source
// positions in [0, src): exact area-average when shrinking, bilinear when
// enlarging.
func axisWeights(src, dst int) [][]tap {
	out := make([][]tap, dst)
	scale := float64(src) / float64(dst)
	for i := range out {
		if scale >= 1 {
			lo, hi := float64(i)*scale, float64(i+1)*scale
			for j := int(lo); j < src && float64(j) < hi; j++ {
				cover := math.Min(hi, float64(j+1)) - math.Max(lo, float64(j))
				if cover > 1e-9 {
					out[i] = append(out[i], tap{j, float32(cover / scale)})
				}
			}
			continue
		}
		c := (float64(i)+0.5)*scale - 0.5
		j0 := int(math.Floor(c))
		f := float32(c - float64(j0))
		j1 := j0 + 1
		j0 = max(0, min(j0, src-1))
		j1 = max(0, min(j1, src-1))
		out[i] = []tap{{j0, 1 - f}, {j1, f}}
	}
	return out
}

// Fit center-crops src to the w:h aspect ratio and scales it to exactly w x h.
func Fit(src *image.RGBA, w, h int) *image.RGBA {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	cw, ch := sw, sh
	if sw*h > sh*w { // too wide: crop the sides
		cw = max(1, sh*w/h)
	} else { // too tall: crop top and bottom
		ch = max(1, sw*h/w)
	}
	x0, y0 := (sw-cw)/2, (sh-ch)/2

	xw, yw := axisWeights(cw, w), axisWeights(ch, h)

	// Horizontal pass over the cropped rows → tmp (ch rows x w columns x 4).
	tmp := make([]float32, ch*w*4)
	for y := 0; y < ch; y++ {
		row := src.Pix[(y0+y)*src.Stride:]
		for x, taps := range xw {
			var r, g, b, a float32
			for _, t := range taps {
				p := row[(x0+t.idx)*4:]
				r += t.w * float32(p[0])
				g += t.w * float32(p[1])
				b += t.w * float32(p[2])
				a += t.w * float32(p[3])
			}
			o := (y*w + x) * 4
			tmp[o], tmp[o+1], tmp[o+2], tmp[o+3] = r, g, b, a
		}
	}
	// Vertical pass → dst.
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y, taps := range yw {
		for x := 0; x < w; x++ {
			var c [4]float32
			for _, t := range taps {
				o := (t.idx*w + x) * 4
				c[0] += t.w * tmp[o]
				c[1] += t.w * tmp[o+1]
				c[2] += t.w * tmp[o+2]
				c[3] += t.w * tmp[o+3]
			}
			p := dst.Pix[y*dst.Stride+x*4:]
			for k := range c {
				p[k] = clamp8(c[k])
			}
		}
	}
	return dst
}

func clamp8(v float32) uint8 {
	v += 0.5
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v)
}

// RGB565 packs an image as little-endian RGB565, row-major.
func RGB565(img *image.RGBA) []byte {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	out := make([]byte, w*h*2)
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			p := row[x*4:]
			binary.LittleEndian.PutUint16(out[(y*w+x)*2:], Pack565(p[0], p[1], p[2]))
		}
	}
	return out
}

// Pack565 rounds 8-bit channels to 5-6-5 bits.
func Pack565(r, g, b uint8) uint16 {
	r5 := (uint16(r)*31 + 127) / 255
	g6 := (uint16(g)*63 + 127) / 255
	b5 := (uint16(b)*31 + 127) / 255
	return r5<<11 | g6<<5 | b5
}

// Orient applies an EXIF orientation (1-8) so the image displays upright.
func Orient(src *image.RGBA, o int) *image.RGBA {
	if o < 2 || o > 8 {
		return src
	}
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := sw, sh
	if o >= 5 {
		dw, dh = sh, sw
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = sw-1-x, y
			case 3:
				sx, sy = sw-1-x, sh-1-y
			case 4:
				sx, sy = x, sh-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, sh-1-x
			case 7:
				sx, sy = sw-1-y, sh-1-x
			case 8:
				sx, sy = sw-1-y, x
			}
			copy(dst.Pix[y*dst.Stride+x*4:y*dst.Stride+x*4+4], src.Pix[sy*src.Stride+sx*4:])
		}
	}
	return dst
}

// EXIFOrientation returns the orientation tag of a JPEG (1 if absent).
func EXIFOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		switch {
		case marker == 0xFF: // fill byte
			i++
			continue
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			i += 2
			continue
		case marker == 0xDA || marker == 0xD9: // image data starts: no EXIF
			return 1
		}
		n := int(b[i+2])<<8 | int(b[i+3])
		if n < 2 || i+2+n > len(b) {
			return 1
		}
		seg := b[i+4 : i+2+n]
		if marker == 0xE1 && len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + n
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(t[2:]) != 42 {
		return 1
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[ifd:]))
	for k := 0; k < n; k++ {
		e := ifd + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
