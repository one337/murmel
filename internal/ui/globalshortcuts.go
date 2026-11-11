package ui

import (
	"context"
	"errors"
	"log/slog"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui/shortcuts"
)

// globalShortcuts connects Murmel's global shortcuts (push-to-talk, mute,
// deafen, previous and next channel) to the desktop's Global Shortcuts
// portal. Murmel registers them only once the user sets them up (in
// Preferences, or by choosing push-to-talk); from then on at every start,
// silently. Main loop only, except inside the goroutines it starts.
type globalShortcuts struct {
	w         *Window
	portal    *shortcuts.Portal
	available bool // the desktop has the portal (probed at start)
	probed    bool
	bindings  []shortcuts.Binding
	busy      bool
	listeners map[int]func()
	nextID    int
}

func shortcutList() []shortcuts.Shortcut {
	return []shortcuts.Shortcut{
		{ID: shortcuts.PushToTalk, Description: tr("Push-to-talk")},
		{ID: shortcuts.ToggleMute, Description: tr("Mute or unmute the microphone")},
		{ID: shortcuts.ToggleDeafen, Description: tr("Turn the sound off or on")},
		{ID: shortcuts.PreviousChannel, Description: tr("Join the previous channel")},
		{ID: shortcuts.NextChannel, Description: tr("Join the next channel")},
	}
}

func newGlobalShortcuts(w *Window) *globalShortcuts {
	g := &globalShortcuts{w: w, listeners: map[int]func(){}}
	g.portal = shortcuts.New(settings.SchemaID, shortcutList())
	g.portal.OnActivated = g.activated
	g.portal.OnChanged = func(b []shortcuts.Binding) { g.setBindings(b) }

	go func() {
		ok := shortcuts.Available(w.ctx)
		glib.IdleAdd(func() bool {
			g.available, g.probed = ok, true
			g.changed()
			if ok && w.app.settings.GlobalShortcuts() {
				g.bind(false)
			}
			return false
		})
	}()
	// Choosing push-to-talk sets the shortcuts up if that has not
	// happened yet: without a key it could not be used.
	w.app.settings.ConnectChanged(func(key string) {
		if key == "transmission-mode" && w.app.settings.TransmissionMode() == "push-to-talk" &&
			!w.app.settings.GlobalShortcuts() {
			g.SetUp()
		}
	})
	return g
}

// activated runs a shortcut (main loop).
func (g *globalShortcuts) activated(id string, pressed bool) {
	switch id {
	case shortcuts.PushToTalk:
		g.w.app.bus.Emit(bus.PushToTalk{Pressed: pressed})
	case shortcuts.ToggleMute:
		if pressed {
			g.w.status.selfMute.SetActive(!g.w.status.SelfMuted())
		}
	case shortcuts.ToggleDeafen:
		if pressed {
			g.w.status.selfDeafen.SetActive(!g.w.status.SelfDeafened())
		}
	case shortcuts.PreviousChannel:
		if pressed {
			g.w.app.bus.Emit(bus.ChannelStepRequested{Delta: -1})
		}
	case shortcuts.NextChannel:
		if pressed {
			g.w.app.bus.Emit(bus.ChannelStepRequested{Delta: 1})
		}
	}
}

// bind registers the shortcuts; configure opens the desktop's dialog for
// changing the keys instead (they are registered already).
func (g *globalShortcuts) bind(configure bool) {
	if g.busy {
		return
	}
	g.busy = true
	g.changed()
	go func() {
		var b []shortcuts.Binding
		var err error
		ctx := context.Background()
		if configure {
			err = g.portal.Configure(ctx)
			if err == nil {
				b, err = g.portal.List(ctx)
			}
		} else {
			b, err = g.portal.Bind(ctx)
		}
		glib.IdleAdd(func() bool {
			g.busy = false
			switch {
			case err == nil:
				g.w.app.settings.SetGlobalShortcuts(true)
				g.setBindings(b)
			case errors.Is(err, shortcuts.ErrCancelled):
				g.changed()
			default:
				slog.Info("global shortcuts", "err", err)
				g.changed()
			}
			return false
		})
	}()
}

func (g *globalShortcuts) setBindings(b []shortcuts.Binding) {
	g.bindings = b
	g.changed()
}

func (g *globalShortcuts) changed() {
	for _, f := range g.listeners {
		f()
	}
}

// The methods below are what the preferences window uses.

// Available reports whether the desktop offers global shortcuts, and
// whether that is known yet.
func (g *globalShortcuts) Available() (available, known bool) { return g.available, g.probed }

// Shortcuts lists the shortcuts with their descriptions.
func (g *globalShortcuts) Shortcuts() []shortcuts.Shortcut { return shortcutList() }

// Bindings returns the keys the desktop reported (empty before set-up).
func (g *globalShortcuts) Bindings() []shortcuts.Binding { return g.bindings }

// IsSetUp reports whether the shortcuts were registered with the desktop.
func (g *globalShortcuts) IsSetUp() bool { return g.w.app.settings.GlobalShortcuts() }

// Busy reports a pending request (the desktop's dialog may be open).
func (g *globalShortcuts) Busy() bool { return g.busy }

// SetUp registers the shortcuts (the desktop asks for the keys) or, once
// they are registered, opens the desktop's dialog to change them.
func (g *globalShortcuts) SetUp() {
	if g.available {
		g.bind(g.w.app.settings.GlobalShortcuts())
	}
}

// OnChange calls f whenever availability, bindings or Busy change; the
// returned function stops that.
func (g *globalShortcuts) OnChange(f func()) (remove func()) {
	id := g.nextID
	g.nextID++
	g.listeners[id] = f
	return func() { delete(g.listeners, id) }
}
