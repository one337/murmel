package dragicon

import (
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The GTK test needs a display. tools/verify.sh runs it under xvfb-run
// with MURMEL_GTK_TESTS=1 and G_DEBUG=fatal-criticals; without them it is
// skipped.

var gtkReady bool

// GTK must run on the main thread: TestMain keeps it there and runs the
// GLib main loop while the tests run on other goroutines.
func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	if os.Getenv("MURMEL_GTK_TESTS") == "" || !gtk.InitCheck() {
		os.Exit(m.Run())
	}
	gtkReady = true
	loop := glib.NewMainLoop(nil, false)
	code := 0
	go func() {
		code = m.Run()
		glib.IdleAdd(func() bool { loop.Quit(); return false })
	}()
	loop.Run()
	os.Exit(code)
}

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

// collectUntil runs garbage collections with main-loop turns in between
// until done reports true, for at most 10 s.
func collectUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		onMain(func() {})
		if done() {
			return
		}
	}
	t.Fatalf("%s was not finalized", what)
}

// On Wayland, finalizing a drag destroys its surface. A drag icon that Go
// still holds at that moment and releases later was taken down without a
// frame clock (critical, fatal under G_DEBUG=fatal-criticals). The test
// releases the drag first and the icon after it, the order that went
// wrong, and checks that End took the icon down before.
func TestGTKIconIsDownBeforeItsDragGoes(t *testing.T) {
	needGTK(t)
	// Dropped: GTK hides the drag surface at once. Cancelled: it flies
	// back first (X11), and End waits for that.
	for _, dropped := range []bool{true, false} {
		iconGoesWithDrag(t, dropped)
	}
}

func iconGoesWithDrag(t *testing.T, dropped bool) {
	t.Helper()
	var win *gtk.Window
	onMain(func() {
		win = gtk.NewWindow()
		win.SetDefaultSize(200, 200)
		win.Present()
	})
	t.Cleanup(func() { onMain(func() { win.Destroy() }) })
	for deadline := time.Now().Add(5 * time.Second); ; {
		var mapped bool
		onMain(func() { mapped = win.Mapped() })
		if mapped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("window not mapped")
		}
		time.Sleep(20 * time.Millisecond)
	}

	var dragGone, iconGone atomic.Bool
	var held *gtk.DragIcon // Go's own reference to the icon, released last
	var icon *Icon
	onMain(func() {
		surface := gtk.BaseWidget(win).Native().Surface()
		pointer := gdk.BaseSeat(gtk.BaseWidget(win).Display().DefaultSeat()).Pointer()
		drag := gdk.DragBegin(surface, pointer,
			gdk.NewContentProviderForValue(coreglib.NewValue("row")), gdk.ActionMove, 0, 0)
		if drag == nil {
			return
		}
		coreglib.WeakRefObject(drag, func() { dragGone.Store(true) })
		icon = Set(drag, gtk.NewLabel("row"))
		held = icon.icon
		coreglib.WeakRefObject(held, func() { iconGone.Store(true) })
		gdk.BaseDrag(drag).DropDone(dropped)
		icon.End()
	})
	if icon == nil {
		t.Fatal("gdk.DragBegin started no drag")
	}
	icon = nil

	// The drag goes first: its surface is destroyed while Go still
	// holds the icon.
	collectUntil(t, "the drag", dragGone.Load)
	runtime.KeepAlive(held)
	held = nil
	collectUntil(t, "the drag icon", iconGone.Load)
}

func TestNilIconEnds(t *testing.T) {
	var icon *Icon
	icon.End()
	(&Icon{}).End()
}
