// Command murmel launches the Murmel GTK4/Libadwaita client.
//
// main wires the process-wide pieces — settings, certificates, the event bus,
// the application shell, and the connection controller — then hands control
// to the GTK main loop. All GTK work happens on that loop; connection and
// audio work happen on goroutines and marshal back via glib.IdleAdd.
package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/certs"
	"github.com/one337/murmel/internal/protocol/gumble"
	"github.com/one337/murmel/internal/settings"
	"github.com/one337/murmel/internal/ui"
	"github.com/one337/murmel/internal/ui/admin"
	"github.com/one337/murmel/internal/ui/channeltree"
	"github.com/one337/murmel/internal/ui/chat"
	"github.com/one337/murmel/internal/ui/connection"
	"github.com/one337/murmel/internal/ui/prefs"
	"github.com/one337/murmel/internal/ui/secrets"
)

func main() {
	slog.SetDefault(slog.New(newLogHandler(os.Stderr, os.Getenv("G_MESSAGES_DEBUG"))))
	gumble.RegisterMurmelOpusCodec()

	st := settings.New()
	ui.ApplyLanguage(st.Language()) // before any UI text is created
	b := bus.New(nil)

	certsManager, err := certs.NewManager()
	if err != nil {
		slog.Error("certs init failed", "err", err)
		os.Exit(1)
	}

	// Where passwords can be saved is found out while the window opens.
	var passwordFile *secrets.File
	if dir, err := settings.DataDir(); err == nil {
		passwordFile = secrets.NewFile(filepath.Join(dir, "passwords.json"))
	}
	passwords := secrets.NewManager(secrets.Keyring{}, passwordFile,
		func() secrets.State { return secrets.Detect(context.Background()) },
		func() { b.Emit(bus.PasswordStorageChanged{}) })

	app := ui.NewApp(st, b)

	// Wire up the subsystems once, when the main window is created: on a
	// normal start (activate) as well as on a mumble:// link (open).
	app.OnWindowCreated(func(win *ui.Window) {
		ctx := app.Context()

		ctrl := connection.NewController(ctx, win, b, st, certsManager, passwords)
		app.OnConnect(ctrl.OnConnectRequest)

		tree := channeltree.NewTree(ctx, b, win)
		win.Sidebar().Set(tree.Root())
		win.Sidebar().SetFocus(tree.FocusEntry)

		chatView := chat.NewView(ctx, b, ctrl)
		chatView.AddChannelHeaderWidget(win.NarrowMuteButton())
		chatView.UseSettings(st)
		win.Chat().Set(chatView.Root())
		win.Chat().SetFocus(chatView.FocusComposer)
		app.OnOpenChat(chatView.OpenConversation)

		app.OnPreferences(func() {
			prefs.Show(win, st, certsManager, passwords, app.SoundPlayer(), b, win.GlobalShortcuts())
		})
		app.OnBanList(func() { admin.ShowBanList(win, b) })
		app.OnUserList(func() { admin.ShowUserList(win, b) })
	})

	code := app.Run(os.Args)
	st.Flush()
	b.Close()
	os.Exit(code)
}
