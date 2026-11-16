package prefs

import (
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

// Opening and closing Preferences 50 times frees every dialog; the
// certificate list of the Identity page goes with its dialog.
func TestPreferencesAreFreed(t *testing.T) {
	if !gtkReady {
		t.Skip("needs GTK and a display: run with MURMEL_GTK_TESTS=1 under xvfb-run (tools/verify.sh)")
	}
	mgr, err := certs.NewManager(certs.WithDataDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Generate("qa", ""); err != nil {
		t.Fatal(err)
	}
	st := settings.NewMemory()
	b := bus.New(nil)
	win := testWindow(t)

	var w leakWatch
	for range cycles {
		var d, dc *adw.PreferencesDialog
		onMain(func() {
			pw := New(st, mgr, nil, b, nil)
			w.object(pw.PreferencesDialog)
			pw.Present(win)
			d = pw.PreferencesDialog
		})
		onMain(func() { d.ForceClose() })
		// The Identity page's certificate list, in a dialog of its own.
		onMain(func() {
			pw := adw.NewPreferencesDialog()
			page, c := identityPage(st, mgr, pw)
			pw.Add(page)
			watchStruct(&w, c)
			w.object(pw)
			pw.Present(win)
			dc = pw
		})
		onMain(func() { dc.ForceClose() })
		d, dc = nil, nil
	}
	w.check(t, "preferences")
}

// collect runs garbage collections with main-loop turns in between.
func collect() {
	for range 12 {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		onMain(func() {})
	}
}

// onNotify still reaches its row after the Go side collected everything
// it could: the row lives on in the window, the test keeps no reference.
func TestOnNotifyWorksAfterGC(t *testing.T) {
	if !gtkReady {
		t.Skip("needs GTK and a display: run with MURMEL_GTK_TESTS=1 under xvfb-run (tools/verify.sh)")
	}
	var win *adw.Window
	var got atomic.Int32
	onMain(func() {
		win = adw.NewWindow()
		row := adw.NewSwitchRow()
		onNotify(row, "active", func(row *adw.SwitchRow) {
			if row.Active() {
				got.Add(1)
			}
		})
		list := gtk.NewListBox()
		list.Append(row)
		win.SetContent(list)
		win.Present()
	})
	t.Cleanup(func() { onMain(func() { win.Destroy() }) })
	collect()
	onMain(func() {
		list := coreglib.InternObject(win.Content()).Cast().(*gtk.ListBox)
		r := coreglib.InternObject(list.RowAtIndex(0)).Cast().(*adw.SwitchRow)
		r.SetActive(true)
	})
	if got.Load() != 1 {
		t.Fatalf("the notify callback ran %d times after GC, want 1", got.Load())
	}
}
