// Command murmel launches the Murmel GTK4/Libadwaita client.
//
// main wires the process-wide pieces — settings, certificates, the event bus,
// the application shell, and the connection controller — then hands control
// to the GTK main loop. All GTK work happens on that loop; connection and
// audio work happen on goroutines and marshal back via glib.IdleAdd.
package main

import (
	"log/slog"
	"os"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/certs"
	"io.github/murmel/internal/protocol/gumble"
	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui"
	"io.github/murmel/internal/ui/admin"
	"io.github/murmel/internal/ui/channeltree"
	"io.github/murmel/internal/ui/chat"
	"io.github/murmel/internal/ui/connection"
	"io.github/murmel/internal/ui/prefs"
)

func main() {
	gumble.RegisterMurmelOpusCodec()

	st := settings.New()
	ui.ApplyLanguage(st.Language()) // before any UI text is created
	b := bus.New(nil)

	certsManager, err := certs.NewManager()
	if err != nil {
		slog.Error("certs init failed", "err", err)
		os.Exit(1)
	}

	app := ui.NewApp(st, b)

	// Wire up the subsystems once, when the main window is created: on a
	// normal start (activate) as well as on a mumble:// link (open).
	app.OnWindowCreated(func(win *ui.Window) {
		ctx := app.Context()

		ctrl := connection.NewController(ctx, win, b, st, certsManager)
		app.OnConnect(ctrl.OnConnectRequest)

		tree := channeltree.NewTree(ctx, b, win)
		win.Sidebar().Set(tree.Root())

		chatView := chat.NewView(ctx, b, ctrl)
		chatView.AddChannelHeaderWidget(win.NarrowMuteButton())
		chatView.UseSettings(st)
		win.Chat().Set(chatView.Root())
		app.OnOpenChat(chatView.OpenConversation)

		app.OnPreferences(func() {
			pw := prefs.New(st, certsManager, app.SoundPlayer(), b)
			pw.SetTransientFor(&win.ApplicationWindow.ApplicationWindow.Window)
			pw.Present()
		})
		app.OnBanList(func() { admin.ShowBanList(win, b) })
		app.OnUserList(func() { admin.ShowUserList(win, b) })
	})

	code := app.Run(os.Args)
	b.Close()
	os.Exit(code)
}
