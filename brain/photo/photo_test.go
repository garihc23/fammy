package photo

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

func TestPack565(t *testing.T) {
	cases := []struct {
		r, g, b uint8
		want    uint16
	}{
		{255, 0, 0, 0xF800},
		{0, 255, 0, 0x07E0},
		{0, 0, 255, 0x001F},
		{255, 255, 255, 0xFFFF},
		{0, 0, 0, 0x0000},
		{128, 128, 128, 16<<11 | 32<<5 | 16},
	}
	for _, c := range cases {
		if got := Pack565(c.r, c.g, c.b); got != c.want {
			t.Errorf("Pack565(%d,%d,%d) = %04x, want %04x", c.r, c.g, c.b, got, c.want)
		}
	}
}

func TestRGB565LittleEndian(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	img.Set(1, 0, color.RGBA{0, 0, 255, 255})
	got := RGB565(img)
	want := []byte{0x00, 0xF8, 0x1F, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestFitCropsCenter(t *testing.T) {
	// 8x3: red | green (middle 4) | blue. Fit to 4x3 must keep only green.
	src := image.NewRGBA(image.Rect(0, 0, 8, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 8; x++ {
			c := color.RGBA{0, 255, 0, 255}
			if x < 2 {
				c = color.RGBA{255, 0, 0, 255}
			} else if x >= 6 {
				c = color.RGBA{0, 0, 255, 255}
			}
			src.Set(x, y, c)
		}
	}
	out := Fit(src, 4, 3)
	if out.Rect.Dx() != 4 || out.Rect.Dy() != 3 {
		t.Fatalf("size %v", out.Rect)
	}
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			if c := out.RGBAAt(x, y); c != (color.RGBA{0, 255, 0, 255}) {
				t.Fatalf("pixel %d,%d = %v, want green", x, y, c)
			}
		}
	}
}

func TestFitAreaAverage(t *testing.T) {
	// 4x2 with alternating black/white columns averages to mid grey at 2x1.
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			v := uint8(0)
			if x%2 == 1 {
				v = 254
			}
			src.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	out := Fit(src, 2, 1)
	for x := 0; x < 2; x++ {
		if c := out.RGBAAt(x, 0); c.R != 127 || c.G != 127 || c.B != 127 || c.A != 255 {
			t.Fatalf("pixel %d = %v, want grey 127", x, c)
		}
	}
}

func TestFitUpscaleAndSolid(t *testing.T) {
	c := color.RGBA{10, 200, 30, 255}
	for _, sz := range [][2]int{{3, 2}, {1000, 750}, {641, 480}, {200, 900}} {
		out := Fit(solid(sz[0], sz[1], c), 320, 240)
		if out.Rect.Dx() != 320 || out.Rect.Dy() != 240 {
			t.Fatalf("%v: size %v", sz, out.Rect)
		}
		for _, p := range [][2]int{{0, 0}, {319, 239}, {160, 120}} {
			if got := out.RGBAAt(p[0], p[1]); got != c {
				t.Fatalf("%v: pixel %v = %v, want %v", sz, p, got, c)
			}
		}
	}
}

func TestOrient(t *testing.T) {
	// 2x1: A B. Orientation 6 (rotate 90° CW) → 1x2: A on top, B below.
	a, b := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, a)
	src.Set(1, 0, b)
	r := Orient(src, 6)
	if r.Rect.Dx() != 1 || r.Rect.Dy() != 2 || r.RGBAAt(0, 0) != a || r.RGBAAt(0, 1) != b {
		t.Fatalf("orient 6: %v %v %v", r.Rect, r.RGBAAt(0, 0), r.RGBAAt(0, 1))
	}
	r = Orient(src, 8) // 90° CCW: B on top
	if r.RGBAAt(0, 0) != b || r.RGBAAt(0, 1) != a {
		t.Fatal("orient 8")
	}
	r = Orient(src, 3)
	if r.RGBAAt(0, 0) != b || r.RGBAAt(1, 0) != a {
		t.Fatal("orient 3")
	}
}

func TestEXIFOrientation(t *testing.T) {
	// SOI, APP1 Exif with a little-endian TIFF holding Orientation=6, then SOS.
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0}
	entry := make([]byte, 12)
	binary.LittleEndian.PutUint16(entry[0:], 0x0112)
	binary.LittleEndian.PutUint16(entry[2:], 3)
	binary.LittleEndian.PutUint32(entry[4:], 1)
	binary.LittleEndian.PutUint16(entry[8:], 6)
	tiff = append(tiff, entry...)
	tiff = append(tiff, 0, 0, 0, 0)
	seg := append([]byte("Exif\x00\x00"), tiff...)
	data := []byte{0xFF, 0xD8, 0xFF, 0xE1, byte((len(seg) + 2) >> 8), byte(len(seg) + 2)}
	data = append(data, seg...)
	data = append(data, 0xFF, 0xDA, 0, 2)
	if got := EXIFOrientation(data); got != 6 {
		t.Fatalf("got %d, want 6", got)
	}
	if got := EXIFOrientation([]byte{0x89, 'P', 'N', 'G'}); got != 1 {
		t.Fatalf("png: got %d", got)
	}
}

func TestLoadJPEGAndPNG(t *testing.T) {
	dir := t.TempDir()
	img := solid(64, 48, color.RGBA{255, 255, 255, 255})
	var buf bytes.Buffer
	png.Encode(&buf, img)
	os.WriteFile(filepath.Join(dir, "a.png"), buf.Bytes(), 0o644)
	buf.Reset()
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95})
	os.WriteFile(filepath.Join(dir, "b.jpg"), buf.Bytes(), 0o644)
	for _, name := range []string{"a.png", "b.jpg"} {
		out, err := Load(filepath.Join(dir, name), 320, 240)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 320*240*2 {
			t.Fatalf("%s: %d bytes", name, len(out))
		}
		if v := binary.LittleEndian.Uint16(out[1000:]); v < 0xF7BE { // near white
			t.Fatalf("%s: pixel %04x not white", name, v)
		}
	}
}
