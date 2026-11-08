package ui

import (
	"context"
	"io.github/murmel/internal/audio/effects"
	"log/slog"
	"strconv"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/protocol/gumble"
	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui/resources"
)

// App owns the adw.Application lifecycle: the unique instance, the app-level
// actions and accelerators, mumble:// URL intake, and clean shutdown. It
// owns exactly one Window; later windows (preferences T25, about) attach to
// the same application.
type App struct {
	*adw.Application

	settings settings.Settings
	bus      *bus.Bus

	ctx    context.Context
	cancel context.CancelFunc

	window *Window

	// connectRequested is the T19 seam: set via OnConnect to open the
	// connect dialog, pre-filled from the request. Until then requests are
	// only logged.
	connectRequested func(req ConnectRequest)

	// prefsRequested is the T25 seam: set via OnPreferences to open the
	// preferences window. Until wired the action is a no-op.
	prefsRequested func()

	// banListRequested is the T21 seam: opens the server ban list dialog.
	banListRequested func()

	// userListRequested is the T21 seam: opens the registered users dialog.
	userListRequested func()

	// onOpenChat is the T17 seam: set via OnOpenChat to open a user's
	// conversation. Activating a PM notification routes here (T26).
	onOpenChat func(session uint32)

	// notifier implements the T26 desktop notifications.
	notifier *notifier
	// windowCreated wires the subsystems once the window exists.
	windowCreated func(*Window)
	// presence derives join/leave/move events for notifier and sounds.
	presence *presenceTracker
	// sounds plays the event sounds.
	sounds *eventSounds
	// soundPlayer is shared by the event sounds and the preferences
	// preview buttons.
	soundPlayer *effects.Player
}

// NewApp builds the application shell around the process settings and the
// event bus. The bus may still be client-less (bus.New(nil)) — the shell
// then renders its empty state and reacts to no events.
func NewApp(st settings.Settings, b *bus.Bus) *App {
	ctx, cancel := context.WithCancel(context.Background())
	app := &App{
		Application: adw.NewApplication(settings.SchemaID, gio.ApplicationHandlesOpen),
		settings:    st,
		bus:         b,
		ctx:         ctx,
		cancel:      cancel,
	}

	// The embedded GResource bundle carries the app icon; GtkApplication
	// adds <base-path>/icons to the icon theme on startup.
	resources.Register()
	app.ConnectStartup(func() { gtk.WindowSetDefaultIconName(settings.SchemaID) })

	app.ConnectActivate(func() { app.ensureWindow() })
	app.ConnectOpen(func(files []gio.Filer, _ string) {
		app.ensureWindow()
		for _, file := range files {
			if req, ok := ParseConnectURL(file.URI()); ok {
				app.RequestConnect(req)
			}
		}
	})
	// Shutdown covers session-ending paths where no CloseRequest fires;
	// normal window close cancels the window context first.
	app.ConnectShutdown(func() { app.cancel() })

	app.registerActions()
	app.presence = newPresenceTracker()
	app.notifier = newNotifier(app)
	app.soundPlayer = effects.NewPlayer()
	app.sounds = newEventSounds(app)
	app.presence.subscribe(app.ctx, app.bus)
	return app
}

// OnConnect installs the handler invoked for every connect request — both
// the empty-state Connect button and mumble:// URLs from the command line
// or a second instance. T19 points this at its dialog.
func (a *App) OnConnect(f func(req ConnectRequest)) { a.connectRequested = f }

// OnOpenChat installs the handler invoked when a conversation should be
// shown — activating a PM notification. T17 points this at the chat view.
func (a *App) OnOpenChat(f func(session uint32)) { a.onOpenChat = f }

// SoundPlayer returns the player for event sounds (preferences preview).
func (a *App) SoundPlayer() *effects.Player { return a.soundPlayer }

// OnPreferences installs the handler invoked when the user opens Preferences.
// T25 (main.go) points this at prefs.New().Present(win).
func (a *App) OnPreferences(f func()) { a.prefsRequested = f }

// OnBanList installs the handler invoked when the user opens the ban list.
// T21 (main.go) points this at admin.ShowBanList(win, bus).
func (a *App) OnBanList(f func()) { a.banListRequested = f }

// OnUserList installs the handler invoked when the user opens the user list.
// T21 (main.go) points this at admin.ShowUserList(win, bus).
func (a *App) OnUserList(f func()) { a.userListRequested = f }

// RequestConnect routes a connect request into the T19 seam. Main loop only.
func (a *App) RequestConnect(req ConnectRequest) {
	slog.Info("connect requested", "host", req.Host, "port", req.Port, "source", req.URL)
	if a.connectRequested != nil {
		a.connectRequested(req)
	}
}

// Context is cancelled once the application shuts down; long-lived UI-side
// workers (the T19 connector, T20 audio) watch it for teardown.
func (a *App) Context() context.Context { return a.ctx }

// Window returns the main application window. It is non-nil only after the
// first Activate signal (i.e. after ensureWindow has run). Callers that
// depend on the window being present should use ConnectActivate or OnActivate.
func (a *App) Window() *Window { return a.window }

// WindowActive reports whether the main window currently has focus. Used by
// the notifier to gate PM notifications, and by the QA harness to verify the
// focus gate is observable.
func (a *App) WindowActive() bool {
	return a.window != nil && a.window.IsActive()
}

// HideWindowForQA is a QA-only helper: lowers (unrealises) the window so the
// notifier's "window is unfocused" gate can be exercised in the headless Xvfb
// harness where there is no window manager to take focus away.
func (a *App) HideWindowForQA() {
	if a.window != nil {
		a.window.Unrealize()
	}
}

func (a *App) ensureWindow() {
	if a.window != nil {
		a.window.Present()
		return
	}
	a.window = newWindow(a)
	if a.windowCreated != nil {
		a.windowCreated(a.window)
	}
	a.window.Present()
}

// OnWindowCreated installs the handler that wires the subsystems (channel
// tree, chat, connection controller) into the main window. It runs exactly
// once, when the window is created — whether the app was started normally
// (activate) or by a mumble:// link (open, where activate never fires) —
// so repeated activations never wire anything twice.
func (a *App) OnWindowCreated(f func(win *Window)) { a.windowCreated = f }

func (a *App) registerActions() {
	quit := gio.NewSimpleAction("quit", nil)
	quit.ConnectActivate(func(*glib.Variant) {
		if a.window != nil {
			a.window.Close()
		} else {
			a.Quit()
		}
	})
	a.AddAction(quit)

	// open-chat target is the sender session serialized as a string variant
	// by the notifier's default action.
	openChat := gio.NewSimpleAction("open-chat", glib.NewVariantType("s"))
	openChat.ConnectActivate(func(v *glib.Variant) {
		session, err := strconv.ParseUint(v.String(), 10, 32)
		if err != nil {
			slog.Warn("open-chat: bad session target", "target", v.String())
			return
		}
		a.openChat(uint32(session))
	})
	a.AddAction(openChat)

	preferences := gio.NewSimpleAction("preferences", nil)
	preferences.ConnectActivate(func(*glib.Variant) {
		if a.prefsRequested != nil {
			a.prefsRequested()
		}
	})
	a.AddAction(preferences)

	banList := gio.NewSimpleAction("banlist", nil)
	banList.ConnectActivate(func(*glib.Variant) {
		if a.banListRequested != nil {
			a.banListRequested()
		}
	})
	a.AddAction(banList)

	about := gio.NewSimpleAction("about", nil)
	about.ConnectActivate(func(*glib.Variant) { a.showAbout() })
	a.AddAction(about)

	disconnect := gio.NewSimpleAction("disconnect", nil)
	disconnect.ConnectActivate(func(*glib.Variant) { a.bus.Emit(bus.DisconnectRequested{}) })
	a.AddAction(disconnect)

	userList := gio.NewSimpleAction("userlist", nil)
	userList.ConnectActivate(func(*glib.Variant) {
		if a.userListRequested != nil {
			a.userListRequested()
		}
	})
	a.AddAction(userList)

	a.trackServerActions(disconnect, banList, userList)

	// GNOME HIG accelerators; the win.* actions themselves live on Window.
	a.SetAccelsForAction("app.quit", []string{"<Primary>q"})
	a.SetAccelsForAction("app.preferences", []string{"<Primary>comma"})
	a.SetAccelsForAction("win.fullscreen", []string{"F11"})
	a.SetAccelsForAction("win.quick-switcher", []string{"<Primary>k"})
}

// trackServerActions enables the server actions only while connected,
// and the admin lists only with the matching root-channel permission
// (ban list: Ban, registered users: Register). Unknown permissions count
// as not granted.
func (a *App) trackServerActions(disconnect, banList, userList *gio.SimpleAction) {
	connected := false
	update := func() {
		var root gumble.Permission
		if ch, ok := a.bus.State().Channels[0]; ok && ch.Permissions != nil {
			root = gumble.Permission(*ch.Permissions)
		}
		disconnect.SetEnabled(connected)
		banList.SetEnabled(connected && root.Has(gumble.PermissionBan))
		userList.SetEnabled(connected && root.Has(gumble.PermissionRegister))
	}
	update()
	bus.SubscribeCtx(a.ctx, a.bus, func(bus.Connected) { connected = true; update() })
	bus.SubscribeCtx(a.ctx, a.bus, func(bus.Disconnected) { connected = false; update() })
	bus.SubscribeCtx(a.ctx, a.bus, func(bus.ConnectError) { connected = false; update() })
	bus.SubscribeCtx(a.ctx, a.bus, func(ev bus.ChannelUpdated) {
		if ev.Channel.ID == 0 {
			update()
		}
	})
}
