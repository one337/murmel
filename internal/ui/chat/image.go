package chat

import (
	"encoding/base64"
	"errors"
	"strconv"

	"github.com/diamondburned/gotk4/pkg/gdkpixbuf/v2"
)

// defaultImageMessageLength is murmur's default limit for messages that
// carry an image (bytes), used until the server announces its own.
const defaultImageMessageLength = 131072

// imageMarkupOverhead is room left in the image limit for the <img> tag
// and accompanying text.
const imageMarkupOverhead = 512

var errImageTooLarge = errors.New("the image cannot be made small enough for this server")

// encodeImage loads an image file and returns it as a JPEG data: URI that
// fits limit bytes (the server's image message length): it scales the
// image down and lowers the quality step by step until it fits.
// Transparent areas become white (JPEG has no alpha).
func encodeImage(path string, limit int) (string, error) {
	src, err := gdkpixbuf.NewPixbufFromFile(path)
	if err != nil {
		return "", err
	}
	budget := limit - imageMarkupOverhead
	for _, side := range []int{800, 640, 480, 360, 240, 160} {
		w, h := scaleTo(src.Width(), src.Height(), side)
		scaled := flatten(src, w, h)
		for _, q := range []int{85, 70, 55, 40} {
			data, err := scaled.SaveToBufferv("jpeg", []string{"quality"}, []string{strconv.Itoa(q)})
			if err != nil {
				return "", err
			}
			uri := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
			if len(uri) <= budget {
				return uri, nil
			}
		}
	}
	return "", errImageTooLarge
}

// scaleTo fits w×h into side×side (never enlarging).
func scaleTo(w, h, side int) (int, int) {
	if w <= side && h <= side {
		return w, h
	}
	if w >= h {
		return side, max(1, h*side/w)
	}
	return max(1, w*side/h), side
}

// flatten scales src to w×h onto an opaque white background.
func flatten(src *gdkpixbuf.Pixbuf, w, h int) *gdkpixbuf.Pixbuf {
	dst := gdkpixbuf.NewPixbuf(gdkpixbuf.ColorspaceRGB, false, 8, w, h)
	dst.Fill(0xffffffff)
	sx := float64(w) / float64(src.Width())
	sy := float64(h) / float64(src.Height())
	src.Composite(dst, 0, 0, w, h, 0, 0, sx, sy, gdkpixbuf.InterpBilinear, 255)
	return dst
}
