// Package gfileio reads and writes files the user picked in a file dialog
// through GIO, so locations without a local path (network shares and other
// GVfs mounts) work too, and off the main loop, so a slow location does
// not freeze the window.
package gfileio

import (
	"context"
	"errors"

	"github.com/diamondburned/gotk4/pkg/gdkpixbuf/v2"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/i18n"
)

// ErrTooLarge is reported for a file above the caller's size limit.
var ErrTooLarge = errors.New("gfileio: file too large")

// Size limits for files the user picks.
const (
	MaxPEMBytes   = 1 << 20  // certificate and key
	MaxImageBytes = 50 << 20 // avatar, chat and description images
	MaxSoundBytes = 16 << 20 // event sounds (as much as playback reads)
)

// readChunk is how much one read asks for.
const readChunk = 64 << 10

// Load reads file, at most maxBytes, and calls done on the main loop. A
// larger file fails with ErrTooLarge: its reported size is checked first,
// and reading stops after maxBytes+1 bytes in any case. Once ctx ends
// (the dialog that asked was closed) the read is cancelled and done does
// not run.
func Load(ctx context.Context, file *gio.File, maxBytes int64, done func([]byte, error)) {
	go func() {
		data, err := read(ctx, file, maxBytes)
		glib.IdleAdd(func() {
			if ctx.Err() == nil {
				done(data, err)
			}
		})
	}()
}

func read(ctx context.Context, file *gio.File, maxBytes int64) ([]byte, error) {
	if info, err := file.QueryInfo(ctx, gio.FILE_ATTRIBUTE_STANDARD_SIZE, gio.FileQueryInfoNone); err == nil &&
		info.Size() > maxBytes {
		return nil, ErrTooLarge
	}
	in, err := file.Read(ctx)
	if err != nil {
		return nil, err
	}
	defer in.Close(context.Background())
	return readLimited(ctx, &in.InputStream, maxBytes)
}

// readLimited reads in to its end, but no more than maxBytes+1 bytes: a
// longer stream fails with ErrTooLarge.
func readLimited(ctx context.Context, in *gio.InputStream, maxBytes int64) ([]byte, error) {
	var data []byte
	buf := make([]byte, readChunk)
	for {
		n, err := in.Read(ctx, buf)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return data, nil
		}
		data = append(data, buf[:n]...)
		if int64(len(data)) > maxBytes {
			return nil, ErrTooLarge
		}
	}
}

// SaveRemote replaces file with data, as a new file readable only by the
// user where the location supports that, and calls done on the main loop.
// It is meant for locations without a local path. Once ctx ends the write
// is cancelled and done does not run.
func SaveRemote(ctx context.Context, file *gio.File, data []byte, done func(error)) {
	go func() {
		_, err := file.ReplaceContents(ctx, string(data), "", false,
			gio.FileCreatePrivate|gio.FileCreateReplaceDestination)
		glib.IdleAdd(func() {
			if ctx.Err() == nil {
				done(err)
			}
		})
	}()
}

// Message describes a Load or SaveRemote error for the user.
func Message(err error) string {
	if errors.Is(err, ErrTooLarge) {
		return i18n.Tr("The file is too large.")
	}
	return err.Error()
}

// Pixbuf decodes image file data (the orientation stored in it is kept as
// the pixbuf's option, see ApplyEmbeddedOrientation).
func Pixbuf(data []byte) (*gdkpixbuf.Pixbuf, error) {
	return gdkpixbuf.NewPixbufFromStream(context.Background(), gio.NewMemoryInputStreamFromBytes(glib.NewBytes(data)))
}

// Name is the file's name without its extension, e.g. for an imported
// identity.
func Name(file *gio.File) string {
	name := file.Basename()
	for i := len(name) - 1; i > 0; i-- {
		if name[i] == '.' {
			return name[:i]
		}
	}
	return name
}

// ForWidget returns a context that ends when w is unrealized: its dialog
// was closed or its window destroyed. Main loop only.
func ForWidget(w gtk.Widgetter) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	gtk.BaseWidget(w).ConnectUnrealize(cancel)
	return ctx
}
