package admin

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

// Opening and closing the channel editor 50 times frees every editor:
// the dialog, the editor struct and its description and ACL pages.
func TestChannelEditorIsFreed(t *testing.T) {
	if !gtkReady {
		t.Skip("needs GTK and a display: run with MURMEL_GTK_TESTS=1 under xvfb-run (tools/verify.sh)")
	}
	win := testWindow(t)
	b := bus.New(nil)
	var w leakWatch
	for range cycles {
		var d *adw.Dialog
		onMain(func() {
			e, dialog := newChannelEditor(b, bus.ChannelInfo{ID: 1, Name: "Lobby", Description: "<b>Hi</b>"}, "")
			e.acl = &bus.ACLFetched{
				ChannelID: 1,
				Groups:    []bus.ACLGroupInfo{{Name: "team", InheritUsers: true, Inheritable: true}},
				Rules:     []bus.ACLRuleInfo{{AppliesCurrent: true, GroupName: "all"}},
			}
			e.aclLoaded()
			watchStruct(&w, e)
			watchStruct(&w, e.desc)
			watchStruct(&w, e.groups)
			watchStruct(&w, e.perms)
			w.object(dialog)
			dialog.Present(win)
			d = dialog
		})
		onMain(func() { d.ForceClose() })
		d = nil
	}
	w.check(t, "channel editor")
}

// An alert dialog (Kick) is freed after closing, 50 times.
func TestAlertDialogIsFreed(t *testing.T) {
	if !gtkReady {
		t.Skip("needs GTK and a display: run with MURMEL_GTK_TESTS=1 under xvfb-run (tools/verify.sh)")
	}
	win := testWindow(t)
	b := bus.New(nil)
	var w leakWatch
	for range cycles {
		onMain(func() {
			ShowKick(win, b, 5, "someone")
			if d := win.VisibleDialog(); d != nil {
				w.object(d)
				d.ForceClose()
			}
		})
	}
	if w.objects != cycles {
		t.Fatalf("%d of %d kick dialogs were shown", w.objects, cycles)
	}
	w.check(t, "kick dialog")
}
