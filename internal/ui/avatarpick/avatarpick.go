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
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gdkpixbuf/v2"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/ui/gfileio"
	"github.com/one337/murmel/internal/ui/weakref"
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

// Process decodes image file data, crops its centre to a square and scales
// it to SidePx (never enlarging), as PNG.
func Process(data []byte) ([]byte, error) {
	src, err := gfileio.Pixbuf(data)
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
	s *rowState
}

// rowState is the state behind a Row. The row's signal closures capture
// it, so it holds the row's widgets only weakly (see internal/ui/weakref).
type rowState struct {
	row      weakref.Ref[*adw.ActionRow]
	preview  weakref.Ref[*adw.Avatar]
	remove   weakref.Ref[*gtk.Button]
	data     []byte
	fallback []byte // shown dimmed when no own avatar is set
	unset    string // subtitle without an own avatar
	onChange func([]byte)
}

// NewRow builds the row. name sets the preview's initials; unset is the
// subtitle while no avatar is chosen. onChange (optional) runs after every
// pick or removal with the new data (nil = removed); it is kept as long
// as the row, so it must not hold the row or its dialog strongly.
func NewRow(title, name, unset string, onChange func([]byte)) *Row {
	row := adw.NewActionRow()
	row.SetTitle(title)
	preview := adw.NewAvatar(36, name, true)
	row.AddPrefix(preview)

	pick := gtk.NewButtonFromIconName("document-open-symbolic")
	pick.AddCSSClass("flat")
	pick.SetVAlign(gtk.AlignCenter)
	pick.SetTooltipText(i18n.Tr("Choose image…"))

	remove := gtk.NewButtonFromIconName("edit-clear-symbolic")
	remove.AddCSSClass("flat")
	remove.SetVAlign(gtk.AlignCenter)
	remove.SetTooltipText(i18n.Tr("Remove avatar"))

	s := &rowState{
		row:      weakref.New(row),
		preview:  weakref.New(preview),
		remove:   weakref.New(remove),
		unset:    unset,
		onChange: onChange,
	}
	pick.ConnectClicked(s.choose)
	remove.ConnectClicked(func() { s.set(nil, true) })
	row.AddSuffix(pick)
	row.AddSuffix(remove)

	row.SetActivatable(true)
	row.SetActivatableWidget(pick)
	s.set(nil, false)
	return &Row{ActionRow: row, s: s}
}

// SetName updates the initials shown without an image.
func (r *Row) SetName(name string) {
	r.s.preview.Do(func(p *adw.Avatar) { p.SetText(name) })
}

// SetFallback is shown (dimmed) while no own avatar is set, e.g. the
// default avatar under a connection.
func (r *Row) SetFallback(data []byte) {
	r.s.fallback = data
	r.s.refresh()
}

// SetData shows data without reporting a change (loading a saved one).
func (r *Row) SetData(data []byte) { r.s.set(data, false) }

// Data is the chosen avatar (nil = none).
func (r *Row) Data() []byte { return r.s.data }

func (s *rowState) set(data []byte, changed bool) {
	s.data = data
	s.refresh()
	if changed && s.onChange != nil {
		s.onChange(data)
	}
}

func (s *rowState) refresh() {
	shown := s.data
	if shown == nil {
		shown = s.fallback
	}
	var tex gdk.Paintabler
	if shown != nil {
		if t, err := gdk.NewTextureFromBytes(glib.NewBytes(shown)); err == nil {
			tex = t
		}
	}
	s.preview.Do(func(p *adw.Avatar) {
		p.SetCustomImage(tex)
		p.SetOpacity(1)
		if s.data == nil && s.fallback != nil {
			p.SetOpacity(0.55)
		}
	})
	s.remove.Do(func(b *gtk.Button) { b.SetVisible(s.data != nil) })
	s.row.Do(func(row *adw.ActionRow) {
		if s.data != nil {
			row.SetSubtitle(i18n.Tr("Shown to others next to your name"))
		} else {
			row.SetSubtitle(s.unset)
		}
	})
}

func (s *rowState) choose() {
	row, ok := s.row.Get()
	if !ok {
		return
	}
	filter := gtk.NewFileFilter()
	filter.SetName(i18n.Tr("Images"))
	filter.AddPixbufFormats()
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(filter.Object)
	fd := gtk.NewFileDialog()
	fd.SetTitle(i18n.Tr("Choose Avatar"))
	fd.SetModal(true)
	fd.SetFilters(filters)
	fd.SetDefaultFilter(filter)
	fd.Open(context.Background(), gfileio.WindowOf(row), func(res gio.AsyncResulter) {
		file, err := fd.OpenFinish(res)
		if err != nil || file == nil {
			return // cancelled
		}
		row, ok := s.row.Get()
		if !ok {
			return
		}
		name := file.Basename()
		ctx := gfileio.ForWidget(row)
		gfileio.Load(ctx, file, gfileio.MaxImageBytes, func(data []byte, err error) {
			if err != nil {
				slog.Info("avatar: image not read", "file", name, "err", err)
				s.row.Do(func(row *adw.ActionRow) { row.SetSubtitle(gfileio.Message(err)) })
				return
			}
			go func() {
				data, err := Process(data)
				glib.IdleAdd(func() {
					if ctx.Err() != nil {
						return
					}
					if err != nil {
						slog.Info("avatar: image not usable", "file", name, "err", err)
						s.row.Do(func(row *adw.ActionRow) { row.SetSubtitle(i18n.Tr("This image could not be read.")) })
						return
					}
					s.set(data, true)
				})
			}()
		})
	})
}
