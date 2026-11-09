package ui

import (
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"io.github/murmel/internal/bus"
)

// Bus handlers run on the GLib main loop. The bus package itself stays
// GTK-free (the audio layer uses it), so the UI hands it glib.IdleAdd here,
// before any bus can dispatch an event.
func init() {
	bus.SetMainLoop(func(f func() bool) { glib.IdleAdd(f) })
}
