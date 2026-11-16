package connection

import (
	"context"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/certs"
	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui"
)

// The leak tests need GTK and a display. tools/verify.sh runs them under
// xvfb-run with MURMEL_GTK_TESTS=1 in a throwaway HOME; without it they
// are skipped, so a plain `go test` never opens a display.

var gtkReady bool

// GTK must run on the main thread: TestMain keeps it there and runs the
// GLib main loop while the tests run on other goroutines.
func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	if os.Getenv("MURMEL_GTK_TESTS") == "" || !gtk.InitCheck() {
		os.Exit(m.Run())
	}
	adw.Init()
	gtkReady = true
	bus.SetMainLoop(func(f func() bool) { glib.IdleAdd(f) })
	loop := glib.NewMainLoop(nil, false)
	code := 0
	go func() {
		code = m.Run()
		glib.IdleAdd(func() bool { loop.Quit(); return false })
	}()
	loop.Run()
	os.Exit(code)
}

// needGTK skips a test without GTK and a display, except in CI, where a
// GTK test that cannot run is a failure.
func needGTK(t *testing.T) {
	t.Helper()
	if gtkReady {
		return
	}
	if os.Getenv("CI") != "" {
		t.Fatal("GTK test cannot run in CI: GTK did not start (tools/verify.sh runs it under xvfb-run with MURMEL_GTK_TESTS=1)")
	}
	t.Skip("needs GTK and a display: run with MURMEL_GTK_TESTS=1 under xvfb-run (tools/verify.sh)")
}

func onMain(f func()) {
	done := make(chan struct{})
	glib.IdleAdd(func() bool { f(); close(done); return false })
	<-done
}

// leakWatch counts the watched GObjects finalized and holds the watched
// Go structs weakly.
type leakWatch struct {
	objects   int
	finalized atomic.Int32
	structs   []func() bool // reports whether a struct is still alive
}

func (w *leakWatch) object(o coreglib.Objector) {
	w.objects++
	coreglib.WeakRefObject(o, func() { w.finalized.Add(1) })
}

func watchStruct[T any](w *leakWatch, p *T) {
	if p == nil {
		return
	}
	wp := weak.Make(p)
	w.structs = append(w.structs, func() bool { return wp.Value() != nil })
}

// check collects several times with main-loop turns in between (gotk4
// releases nested widgets one level per collection, on the main loop) and
// fails if more than a small remainder is still alive.
func (w *leakWatch) check(t *testing.T, what string) {
	t.Helper()
	const tolerance = 2
	var structs, objects int
	for range 3 {
		for range 12 {
			runtime.GC()
			time.Sleep(20 * time.Millisecond)
			onMain(func() {})
		}
		structs = 0
		for _, alive := range w.structs {
			if alive() {
				structs++
			}
		}
		objects = w.objects - int(w.finalized.Load())
		if structs <= tolerance && objects <= tolerance {
			return
		}
	}
	t.Fatalf("%s: %d of %d owner structs still held, %d of %d dialogs not finalized (tolerance %d)",
		what, structs, len(w.structs), objects, w.objects, tolerance)
}

func testWindow(t *testing.T) *adw.Window {
	t.Helper()
	var win *adw.Window
	onMain(func() {
		win = adw.NewWindow()
		win.SetDefaultSize(900, 700)
		win.Present()
	})
	t.Cleanup(func() { onMain(func() { win.Destroy() }) })
	return win
}

const cycles = 50

// Opening and closing the connect dialog 50 times frees every dialog with
// its form and its saved list.
func TestGTKConnectDialogIsFreed(t *testing.T) {
	needGTK(t)
	mgr, err := certs.NewManager(certs.WithDataDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	st := settings.NewMemory()
	// Saved connections on this machine only (the list pings them).
	st.SetServers([]settings.ServerEntry{
		{Name: "a@one.localhost", Host: "one.localhost", Port: 64738, Username: "a"},
		{Name: "b@two.localhost", Host: "two.localhost", Port: 64738, Username: "b"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	win := testWindow(t)
	c := &Controller{win: win, appBus: bus.New(nil), settings: st, certs: mgr, appCtx: ctx}

	var w leakWatch
	for range cycles {
		var d *adw.Dialog
		onMain(func() {
			dialog, form, saved := c.newConnectDialog(ui.ConnectRequest{Host: "one.localhost", Username: "a"})
			if saved == nil {
				t.Error("no saved list")
			}
			watchStruct(&w, form)
			watchStruct(&w, saved)
			w.object(dialog)
			dialog.Present(win)
			d = dialog
		})
		onMain(func() { d.ForceClose() })
		d = nil
	}
	w.check(t, "connect dialog")
}

// Connecting from the dialog (here to a port nobody listens on) frees the
// dialog with its form and saved list too.
func TestGTKConnectDialogIsFreedAfterConnecting(t *testing.T) {
	needGTK(t)
	mgr, err := certs.NewManager(certs.WithDataDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	st := settings.NewMemory()
	st.SetServers([]settings.ServerEntry{
		{Name: "a@one.localhost", Host: "one.localhost", Port: 64738, Username: "a"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	win := testWindow(t)
	c := &Controller{win: win, appBus: bus.New(nil), settings: st, certs: mgr, appCtx: ctx}

	var w leakWatch
	const n = 10
	for range n {
		var d *adw.Dialog
		var f *connectForm
		onMain(func() {
			dialog, form, saved := c.newConnectDialog(ui.ConnectRequest{Host: "127.0.0.1", Port: 1, Username: "a"})
			watchStruct(&w, form)
			watchStruct(&w, saved)
			w.object(dialog)
			for _, r := range saved.rows {
				w.object(r.row)
			}
			dialog.Present(win)
			d, f = dialog, form
		})
		time.Sleep(300 * time.Millisecond) // shown, its saved list pinging
		onMain(func() { c.connectFromForm(d, f) })
		d, f = nil, nil
		// The error dialog of the refused connection.
		shown := false
		for deadline := time.Now().Add(5 * time.Second); !shown && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			onMain(func() {
				if a := win.VisibleDialog(); a != nil {
					if _, isAlert := coreglib.BaseObject(a).Cast().(*adw.AlertDialog); isAlert {
						a.ForceClose()
						shown = true
					}
				}
			})
		}
		if !shown {
			t.Fatal("no error dialog")
		}
	}
	w.check(t, "connect dialog after connecting")
}

// enableWhenFilled still works after the Go side collected everything it
// could: the prompt stays open, the test keeps no reference to it.
func TestGTKEnableWhenFilledWorksAfterGC(t *testing.T) {
	needGTK(t)
	win := testWindow(t)
	onMain(func() {
		entry := adw.NewPasswordEntryRow()
		group := adw.NewPreferencesGroup()
		group.Add(entry)
		alert := adw.NewAlertDialog("Password", "")
		alert.SetExtraChild(group)
		alert.AddResponse("connect", "Connect")
		alert.SetResponseEnabled("connect", false)
		enableWhenFilled(entry, alert, "connect")
		alert.Present(win)
	})
	for range 12 {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		onMain(func() {})
	}
	var enabled bool
	onMain(func() {
		alert := coreglib.BaseObject(win.VisibleDialog()).Cast().(*adw.AlertDialog)
		group := coreglib.BaseObject(alert.ExtraChild()).Cast().(*adw.PreferencesGroup)
		var entry *adw.PasswordEntryRow
		var walk func(w gtk.Widgetter)
		walk = func(w gtk.Widgetter) {
			if e, ok := coreglib.BaseObject(w).Cast().(*adw.PasswordEntryRow); ok {
				entry = e
				return
			}
			for c := gtk.BaseWidget(w).FirstChild(); c != nil && entry == nil; c = gtk.BaseWidget(c).NextSibling() {
				walk(c)
			}
		}
		walk(group)
		if entry == nil {
			t.Error("entry not found")
			return
		}
		entry.SetText("secret")
		enabled = alert.ResponseEnabled("connect")
		alert.ForceClose()
	})
	if !enabled {
		t.Fatal("the response did not become enabled after GC")
	}
}
