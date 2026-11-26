package ui

import (
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
)

// openDialog presents a plain dialog on the main window.
func openDialog(app *App) *adw.Dialog {
	d := adw.NewDialog()
	d.SetTitle("Test")
	d.SetChild(gtk.NewLabel("open"))
	d.Present(app.window)
	return d
}

func openDialogs(app *App) uint { return app.window.Dialogs().NItems() }

// Quitting with a dialog open and no connection closes the dialog and
// the window at once.
func TestGTKQuitClosesOpenDialogs(t *testing.T) {
	needGTK(t)
	app, _ := testApp(t)
	onMain(func() {
		openDialog(app)
		openDialog(app)
	})
	onMain(func() { app.ActivateAction("quit", nil) })
	waitMain(t, "the window to close", func() bool { return !app.window.IsVisible() })
	onMain(func() {
		if n := openDialogs(app); n != 0 {
			t.Errorf("%d dialogs still open after quit", n)
		}
	})
}

// Quitting while connected asks first, above an open dialog; asking again
// adds no second question; agreeing disconnects and closes everything.
func TestGTKQuitWhileConnectedAsksAboveDialog(t *testing.T) {
	needGTK(t)
	app, b := testApp(t)
	disconnects := make(chan struct{}, 4)
	unsub := bus.Subscribe(b, func(bus.DisconnectRequested) { disconnects <- struct{}{} })
	t.Cleanup(unsub)
	b.Emit(bus.Connected{})
	waitMain(t, "the window to see the connection", func() bool { return app.window.connected })

	onMain(func() {
		openDialog(app)
		app.ActivateAction("quit", nil)
	})
	var alert *adw.AlertDialog
	onMain(func() {
		if n := openDialogs(app); n != 2 {
			t.Errorf("open dialogs after quit = %d, want 2 (dialog and question)", n)
		}
		if n := openDialogs(app); n > 0 {
			alert, _ = app.window.Dialogs().Item(n - 1).Cast().(*adw.AlertDialog)
		}
		if !app.window.IsVisible() {
			t.Error("the window closed without asking")
		}
	})
	if alert == nil {
		t.Fatal("no question above the open dialog")
	}
	onMain(func() { app.ActivateAction("quit", nil) })
	onMain(func() {
		if n := openDialogs(app); n != 2 {
			t.Errorf("open dialogs after a second quit = %d, want 2", n)
		}
	})

	// Cancelling (Escape) and quitting again asks again.
	onMain(func() { alert.Close() })
	waitMain(t, "the question to close", func() bool { return openDialogs(app) == 1 })
	alert = nil
	onMain(func() {
		app.ActivateAction("quit", nil)
		if n := openDialogs(app); n == 2 {
			alert, _ = app.window.Dialogs().Item(n - 1).Cast().(*adw.AlertDialog)
		}
	})
	if alert == nil {
		t.Fatal("no question after cancelling and quitting again")
	}

	onMain(func() { alert.Emit("response", "quit") })
	waitMain(t, "the window to close", func() bool { return !app.window.IsVisible() })
	onMain(func() {
		if n := openDialogs(app); n != 0 {
			t.Errorf("%d dialogs still open after agreeing to quit", n)
		}
	})
	select {
	case <-disconnects:
	default:
		waitMain(t, "the disconnect request", func() bool { return len(disconnects) > 0 })
	}
}
