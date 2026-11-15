package richtext

import (
	"bytes"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/webp"
)

// Bounds for images from other users, checked before GTK decodes them.
const (
	// MaxImageSide bounds chat and description images (pixels per side).
	MaxImageSide = 2048
	// MaxAvatarSide bounds avatars (pixels per side).
	MaxAvatarSide = 1024
	// maxImagesPerMessage bounds the images decoded for one message.
	maxImagesPerMessage = 8
)

// CheckImage reports whether data is a PNG, JPEG, GIF or WebP image of at
// most maxSide×maxSide pixels. It reads only the header, so an image that
// would need a lot of memory is refused before anything is decoded; other
// formats (SVG, TIFF, …) are refused outright.
func CheckImage(data []byte, maxSide int) bool {
	var decodeConfig func(io.Reader) (image.Config, error)
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		decodeConfig = png.DecodeConfig
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		decodeConfig = jpeg.DecodeConfig
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		decodeConfig = gif.DecodeConfig
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		decodeConfig = webp.DecodeConfig
	default:
		return false
	}
	cfg, err := decodeConfig(bytes.NewReader(data))
	if err != nil {
		return false
	}
	return cfg.Width > 0 && cfg.Height > 0 && cfg.Width <= maxSide && cfg.Height <= maxSide
}
