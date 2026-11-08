// Package connection implements the connect flow: the server browser, the
// connect dialog with identity picker, TOFU certificate trust, connection
// error dialogs, auto-reconnect, and the gumble client lifecycle.
//
// The Controller owns every gumble.Dial: dialing happens on its own
// goroutine. Before dialing, a per-connection event bus is attached to the
// gumble config (bus.NewForConfig) and forwarded into the application-wide
// bus the UI subscribed to at startup — gumble fires OnConnect from inside
// Dial, so attaching afterwards would lose it. All UI state changes travel
// exclusively through bus events dispatched on the GLib main loop.
//
// Threading: Controller methods prefixed with show/Open are main-loop-only;
// dial/verify callbacks run on connector goroutines and marshal UI work
// through glib.IdleAdd.
package connection

import "github.com/leonelquinteros/gotext"

// tr marks a user-facing string as translatable and returns its translation.
// Uses the same global gotext locale configured by internal/ui's init().
func tr(s string) string { return gotext.Get(s) }
