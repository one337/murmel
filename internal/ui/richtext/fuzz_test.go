package richtext

import (
	"bytes"
	"image"
	"testing"
)

// FuzzParse feeds arbitrary markup to Parse and PlainText; neither may
// panic.
func FuzzParse(f *testing.F) {
	f.Add(`<p>a<b>b</b><a href="https://example.org">link</a><br/><img src="data:image/png;base64,iVBORw0KGgo="/></p>`)
	f.Add(`<i><u>unclosed <a href="javascript:x">x</a>`)
	f.Add("plain &amp; text\n<script>x</script>")
	f.Fuzz(func(t *testing.T, s string) {
		_ = Parse(s)
		_ = PlainText(s)
	})
}

// FuzzCheckImage checks that every image CheckImage accepts decodes as an
// image of at most maxSide pixels per side.
func FuzzCheckImage(f *testing.F) {
	f.Add(pngOf(f, 40, 30), uint16(64))
	f.Add(pngOf(f, 40, 30), uint16(39))
	f.Add(webpHeader(40, 30), uint16(MaxImageSide))
	f.Add([]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), uint16(1))
	f.Add([]byte("\xff\xd8\xff\xc0\x00\x11\x08\x00\x01\x00\x01\x03\x01\x11\x00\x02\x11\x00\x03\x11\x00"), uint16(2))
	f.Fuzz(func(t *testing.T, data []byte, maxSide uint16) {
		if !CheckImage(data, int(maxSide)) {
			return
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("accepted image does not decode: %v", err)
		}
		if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > int(maxSide) || cfg.Height > int(maxSide) {
			t.Fatalf("accepted %d×%d image with limit %d", cfg.Width, cfg.Height, maxSide)
		}
	})
}
