package channeltree

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestDecodeAvatar(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 24)) // not square: adw.Avatar crops
	for x := 0; x < 40; x++ {
		for y := 0; y < 24; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 6), G: 120, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if decodeAvatar(buf.Bytes()) == nil {
		t.Error("PNG avatar not decoded")
	}
	// Raw pixels of pre-1.2 clients, garbage and empty data fall back to
	// the initials.
	for name, data := range map[string][]byte{
		"empty":   nil,
		"garbage": []byte("definitely not an image"),
		"raw":     make([]byte, 600*60*4/16),
	} {
		if decodeAvatar(data) != nil {
			t.Errorf("%s: decoded, want fallback", name)
		}
	}
}
