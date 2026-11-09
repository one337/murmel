// Package avatarpick stores the user's own avatar images (one per saved
// connection plus a default) and provides the row that picks one. Avatars
// are sent to the server as the user's texture after connecting; other
// Mumble clients show them next to the name.
package avatarpick

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gdkpixbuf/v2"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// SidePx is the edge of the stored square avatar: sharp at the channel
// list's size on HiDPI screens, small enough (PNG, typically 10–40 KB) for
// every server's texture limit.
const SidePx = 128

// DefaultKey names the default avatar (Preferences → Identity).
const DefaultKey = "default"

// Store keeps avatar images as PNG files in one directory.
type Store struct{ dir string }

// NewStore uses dir (created on first save).
func NewStore(dir string) *Store { return &Store{dir: dir} }

// AccountKey identifies a saved connection's avatar.
func AccountKey(host string, port int, username string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", strings.ToLower(host), port, username)))
	return hex.EncodeToString(sum[:12])
}

func (s *Store) path(key string) string { return filepath.Join(s.dir, key+".png") }

// Load returns the stored avatar for key (nil if none).
func (s *Store) Load(key string) []byte {
	data, err := os.ReadFile(s.path(key))
	if err != nil {
		return nil
	}
	return data
}

// Save stores data for key; nil data removes the avatar.
func (s *Store) Save(key string, data []byte) error {
	if data == nil {
		err := os.Remove(s.path(key))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp := s.path(key) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(key))
}

// Process loads an image file, crops its centre to a square and scales it
// to SidePx (never enlarging), as PNG.
func Process(path string) ([]byte, error) {
	src, err := gdkpixbuf.NewPixbufFromFile(path)
	if err != nil {
		return nil, err
	}
	if t := src.ApplyEmbeddedOrientation(); t != nil {
		src = t
	}
	w, h := src.Width(), src.Height()
	side := min(w, h)
	if side <= 0 {
		return nil, errors.New("empty image")
	}
	sq := src.NewSubpixbuf((w-side)/2, (h-side)/2, side, side)
	out := min(side, SidePx)
	if out != side {
		sq = sq.ScaleSimple(out, out, gdkpixbuf.InterpHyper)
	}
	return sq.SaveToBufferv("png", nil, nil)
}

// Row is an action row that shows and picks an avatar. Changes are held
// until the caller reads them (Data) unless it saves them itself.
type Row struct {
	*adw.ActionRow
	preview  *adw.Avatar
	remove   *gtk.Button
	data     []byte
	fallback []byte // shown dimmed when no own avatar is set
	unset    string // subtitle without an own avatar
	onChange func([]byte)
}

// NewRow builds the row. name sets the preview's initials; unset is the
// subtitle while no avatar is chosen. onChange (optional) runs after every
// pick or removal with the new data (nil = removed).
func NewRow(title, name, unset string, onChange func([]byte)) *Row {
	r := &Row{ActionRow: adw.NewActionRow(), unset: unset, onChange: onChange}
	r.SetTitle(title)
	r.preview = adw.NewAvatar(36, name, true)
	r.AddPrefix(r.preview)

	pick := gtk.NewButtonFromIconName("document-open-symbolic")
	pick.AddCSSClass("flat")
	pick.SetVAlign(gtk.AlignCenter)
	pick.SetTooltipText(tr("Choose image…"))
	pick.ConnectClicked(r.choose)
	r.AddSuffix(pick)

	r.remove = gtk.NewButtonFromIconName("edit-clear-symbolic")
	r.remove.AddCSSClass("flat")
	r.remove.SetVAlign(gtk.AlignCenter)
	r.remove.SetTooltipText(tr("Remove avatar"))
	r.remove.ConnectClicked(func() { r.set(nil, true) })
	r.AddSuffix(r.remove)

	r.SetActivatable(true)
	r.SetActivatableWidget(pick)
	r.set(nil, false)
	return r
}

// SetName updates the initials shown without an image.
func (r *Row) SetName(name string) { r.preview.SetText(name) }

// SetFallback is shown (dimmed) while no own avatar is set, e.g. the
// default avatar under a connection.
func (r *Row) SetFallback(data []byte) {
	r.fallback = data
	r.refresh()
}

// SetData shows data without reporting a change (loading a saved one).
func (r *Row) SetData(data []byte) { r.set(data, false) }

// Data is the chosen avatar (nil = none).
func (r *Row) Data() []byte { return r.data }

func (r *Row) set(data []byte, changed bool) {
	r.data = data
	r.refresh()
	if changed && r.onChange != nil {
		r.onChange(data)
	}
}

func (r *Row) refresh() {
	shown := r.data
	if shown == nil {
		shown = r.fallback
	}
	var tex gdk.Paintabler
	if shown != nil {
		if t, err := gdk.NewTextureFromBytes(glib.NewBytes(shown)); err == nil {
			tex = t
		}
	}
	r.preview.SetCustomImage(tex)
	r.preview.SetOpacity(1)
	if r.data == nil && r.fallback != nil {
		r.preview.SetOpacity(0.55)
	}
	r.remove.SetVisible(r.data != nil)
	if r.data != nil {
		r.SetSubtitle(tr("Shown to others next to your name"))
	} else {
		r.SetSubtitle(r.unset)
	}
}

func (r *Row) choose() {
	filter := gtk.NewFileFilter()
	filter.SetName(tr("Images"))
	filter.AddPixbufFormats()
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(filter.Object)
	fd := gtk.NewFileDialog()
	fd.SetTitle(tr("Choose Avatar"))
	fd.SetModal(true)
	fd.SetFilters(filters)
	fd.SetDefaultFilter(filter)
	fd.Open(context.Background(), windowOf(r), func(res gio.AsyncResulter) {
		file, err := fd.OpenFinish(res)
		if err != nil || file == nil {
			return // cancelled
		}
		path := file.Path()
		go func() {
			data, err := Process(path)
			glib.IdleAdd(func() {
				if err != nil {
					slog.Info("avatar: image not usable", "file", path, "err", err)
					r.SetSubtitle(tr("This image could not be read."))
					return
				}
				r.set(data, true)
			})
		}()
	})
}

// windowOf finds the toplevel window of w for the file dialog's parent
// (nil when w is not shown; the dialog then has no parent).
func windowOf(w gtk.Widgetter) *gtk.Window {
	root := gtk.BaseWidget(w).Root()
	if root == nil {
		return nil
	}
	switch win := coreglib.InternObject(root).Cast().(type) {
	case *adw.PreferencesWindow:
		return &win.Window.Window
	case *adw.ApplicationWindow:
		return &win.ApplicationWindow.Window
	case *adw.Window:
		return &win.Window
	case *gtk.ApplicationWindow:
		return &win.Window
	case *gtk.Window:
		return win
	}
	return nil
}
