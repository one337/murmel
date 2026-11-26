package richtext

import (
	"bytes"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func encoded(t testing.TB, w, h int, enc func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := enc(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pngOf(t testing.TB, w, h int) []byte {
	return encoded(t, w, h, func(b *bytes.Buffer, m image.Image) error { return png.Encode(b, m) })
}

// webpHeader is a lossless WebP (VP8L) header for w×h; CheckImage reads no
// further than the header.
func webpHeader(w, h int) []byte {
	bits := uint32(w-1) | uint32(h-1)<<14
	return []byte{'R', 'I', 'F', 'F', 30, 0, 0, 0, 'W', 'E', 'B', 'P',
		'V', 'P', '8', 'L', 5, 0, 0, 0, 0x2f,
		byte(bits), byte(bits >> 8), byte(bits >> 16), byte(bits >> 24), 0}
}

func TestCheckImage(t *testing.T) {
	jpg := encoded(t, 40, 30, func(b *bytes.Buffer, m image.Image) error { return jpeg.Encode(b, m, nil) })
	gifImg := encoded(t, 40, 30, func(b *bytes.Buffer, m image.Image) error { return gif.Encode(b, m, nil) })
	big := pngOf(t, MaxImageSide+1, 1)
	cases := []struct {
		name string
		data []byte
		max  int
		want bool
	}{
		{"png", pngOf(t, 40, 30), MaxImageSide, true},
		{"jpeg", jpg, MaxImageSide, true},
		{"gif", gifImg, MaxImageSide, true},
		{"webp", webpHeader(40, 30), MaxImageSide, true},
		{"png at the limit", pngOf(t, MaxAvatarSide, MaxAvatarSide), MaxAvatarSide, true},
		{"png too wide", big, MaxImageSide, false},
		{"png too large for an avatar", pngOf(t, MaxAvatarSide+1, 10), MaxAvatarSide, false},
		{"webp too tall", webpHeader(10, MaxImageSide+1), MaxImageSide, false},
		{"truncated png", pngOf(t, 40, 30)[:12], MaxImageSide, false},
		{"truncated webp", webpHeader(40, 30)[:16], MaxImageSide, false},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`), MaxImageSide, false},
		{"bmp", []byte("BM\x00\x00\x00\x00\x00\x00\x00\x00"), MaxImageSide, false},
		{"empty", nil, MaxImageSide, false},
	}
	for _, c := range cases {
		if got := CheckImage(c.data, c.max); got != c.want {
			t.Errorf("%s: CheckImage = %v, want %v", c.name, got, c.want)
		}
	}
}
