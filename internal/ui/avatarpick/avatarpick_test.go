package avatarpick

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writePNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	path := filepath.Join(t.TempDir(), "in.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProcessCropsAndScales(t *testing.T) {
	for _, tc := range []struct{ w, h, want int }{
		{300, 200, SidePx}, // landscape: centre square, scaled down
		{90, 140, 90},      // small portrait: cropped, never enlarged
	} {
		data, err := Process(writePNG(t, tc.w, tc.h))
		if err != nil {
			t.Fatalf("%dx%d: %v", tc.w, tc.h, err)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || format != "png" {
			t.Fatalf("%dx%d: output not PNG (%v, %q)", tc.w, tc.h, err, format)
		}
		if cfg.Width != tc.want || cfg.Height != tc.want {
			t.Errorf("%dx%d → %dx%d, want %dx%d", tc.w, tc.h, cfg.Width, cfg.Height, tc.want, tc.want)
		}
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "avatars"))
	key := AccountKey("Example.org", 64738, "simon")
	if key != AccountKey("example.org", 64738, "simon") {
		t.Error("host must be case-insensitive")
	}
	if key == AccountKey("example.org", 64738, "other") {
		t.Error("different users must differ")
	}
	if s.Load(key) != nil {
		t.Fatal("empty store returned data")
	}
	if err := s.Save(key, []byte("png")); err != nil {
		t.Fatal(err)
	}
	if got := string(s.Load(key)); got != "png" {
		t.Fatalf("Load = %q", got)
	}
	if err := s.Save(key, nil); err != nil {
		t.Fatal(err)
	}
	if s.Load(key) != nil {
		t.Fatal("Save(nil) did not remove")
	}
	if err := s.Save(key, nil); err != nil {
		t.Fatalf("removing a missing avatar: %v", err)
	}
}
