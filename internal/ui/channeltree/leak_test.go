package channeltree

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"weak"

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

// onMain runs f on the GTK main loop and waits for it.
func onMain(f func()) {
	done := make(chan struct{})
	glib.IdleAdd(func() bool { f(); close(done); return false })
	<-done
}

// collect runs several garbage collections with main-loop turns in
// between: gotk4 releases nested widgets one level per collection, and
// releases its references on the main loop.
func collect() {
	for range 12 {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		onMain(func() {})
	}
}

// connectedEvent is a server with channels channels below the root and
// users users spread over them; the local user is session 1.
func connectedEvent(channels, users int) bus.Connected {
	ev := bus.Connected{SelfSession: 1}
	ev.Channels = append(ev.Channels, bus.ChannelInfo{ID: 0, Name: "Root"})
	for i := 1; i <= channels; i++ {
		ev.Channels = append(ev.Channels, bus.ChannelInfo{ID: uint32(i), Name: fmt.Sprintf("Channel %d", i)})
	}
	for s := 1; s <= users; s++ {
		ev.Users = append(ev.Users, bus.UserInfo{
			Session: uint32(s), Name: fmt.Sprintf("user-%d", s), Channel: uint32(s%channels + 1),
		})
	}
	return ev
}

// buildLeakTree shows a tree of channels and users in a window.
func buildLeakTree(t *testing.T, channels, users int) (*Tree, *bus.Bus) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := bus.New(nil)
	var tree *Tree
	var win *gtk.Window
	onMain(func() {
		win = gtk.NewWindow()
		tree = NewTree(ctx, b, win)
		win.SetChild(tree.Root())
	})
	t.Cleanup(func() { onMain(func() { win.Destroy() }) })
	b.Emit(connectedEvent(channels, users))
	waitBuilt(t, tree, channels, users)
	return tree, b
}

func waitBuilt(t *testing.T, tree *Tree, channels, users int) {
	t.Helper()
	built := false
	for deadline := time.Now().Add(5 * time.Second); !built && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		onMain(func() {
			built = len(tree.userRows) == users && len(tree.channelRows) == channels+1 && !tree.rebuildPending && !tree.rebuildScheduled
		})
	}
	if !built {
		t.Fatal("the tree was not built")
	}
}

// rowWatch watches rows weakly on the Go side and by GObject finalization
// in GTK.
type rowWatch struct {
	users     []weak.Pointer[userRowWidgets]
	channels  []weak.Pointer[channelRowWidgets]
	finalized atomic.Int32
	total     int
}

// watchAll watches every row the tree has now (main loop).
func (w *rowWatch) watchAll(tree *Tree) {
	for _, r := range tree.userRows {
		w.users = append(w.users, weak.Make(r))
		coreglib.WeakRefObject(r.root, func() { w.finalized.Add(1) })
		w.total++
	}
	for _, r := range tree.channelRows {
		w.channels = append(w.channels, weak.Make(r))
		coreglib.WeakRefObject(r.root, func() { w.finalized.Add(1) })
		w.total++
	}
}

// alive counts the watched rows still held in Go and not finalized in GTK.
func (w *rowWatch) alive() (inGo, inGTK int) {
	for _, p := range w.users {
		if p.Value() != nil {
			inGo++
		}
	}
	for _, p := range w.channels {
		if p.Value() != nil {
			inGo++
		}
	}
	return inGo, w.total - int(w.finalized.Load())
}

// Rows that go are freed, on the Go side (the row structs) and in GTK (the
// row widgets are finalized): here every row, replaced by reconnecting 20
// times.
func TestGTKReplacedRowsAreFreed(t *testing.T) {
	needGTK(t)
	const channels, users, reconnects = 30, 100, 20
	tree, b := buildLeakTree(t, channels, users)

	var w rowWatch
	onMain(func() { w.watchAll(tree) })
	for range reconnects {
		b.Emit(bus.Disconnected{})
		b.Emit(connectedEvent(channels, users))
		waitBuilt(t, tree, channels, users)
	}

	// Only a small remainder may still wait for the next collection.
	const tolerance = 2
	var aliveGo, aliveGTK int
	for range 3 {
		collect()
		aliveGo, aliveGTK = w.alive()
		if aliveGo <= tolerance && aliveGTK <= tolerance {
			return
		}
	}
	t.Fatalf("after %d reconnects %d of %d old rows are still held in Go and %d are not finalized in GTK (tolerance %d)",
		reconnects, aliveGo, w.total, aliveGTK, tolerance)
}

// After disconnecting, the tree runs garbage collections for the old rows
// (see release) and stops by itself, having freed at least some of them;
// nothing in the test runs a collection until then. Under load some rows
// stay reachable in Go for a while, so the rest need only be freed by
// later collections.
func TestGTKDisconnectReleasesTheOldRows(t *testing.T) {
	needGTK(t)
	const channels, users = 30, 100
	tree, b := buildLeakTree(t, channels, users)

	var w rowWatch
	var gen int
	onMain(func() {
		w.watchAll(tree)
		gen = tree.releaseGen
	})
	b.Emit(bus.Disconnected{})
	limit := time.Duration(releaseMaxRounds*releaseStepMs)*time.Millisecond + 5*time.Second
	stopped := false
	for deadline := time.Now().Add(limit); !stopped && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		onMain(func() { stopped = tree.releaseGen != gen && tree.releaseRound == 0 })
	}
	if !stopped {
		t.Fatalf("the release did not stop within %v", limit)
	}
	_, inGTK := w.alive()
	t.Logf("the release freed %d of %d rows", w.total-inGTK, w.total)
	if inGTK == w.total {
		t.Errorf("the release stopped without freeing any of the %d rows", w.total)
	}

	for deadline := time.Now().Add(10 * time.Second); inGTK > 0 && time.Now().Before(deadline); {
		collect()
		_, inGTK = w.alive()
	}
	if inGTK > 0 {
		t.Errorf("%d of %d rows are not finalized after further collections", inGTK, w.total)
	}
}

// A row that stays held does not keep the release running: it stops once
// no row was freed for a while, long before its upper limit.
func TestGTKReleaseGivesUpOnHeldRows(t *testing.T) {
	needGTK(t)
	const channels, users = 5, 20
	tree, b := buildLeakTree(t, channels, users)

	var w rowWatch
	var held *userRowWidgets
	var gen int
	onMain(func() {
		w.watchAll(tree)
		held = tree.userRows[3]
		gen = tree.releaseGen
	})
	start := time.Now()
	b.Emit(bus.Disconnected{})
	stopped := false
	for deadline := time.Now().Add(10 * time.Second); !stopped && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		onMain(func() { stopped = tree.releaseGen != gen && tree.releaseRound == 0 })
	}
	took := time.Since(start)
	if limit := time.Duration(releaseMaxRounds*releaseStepMs) * time.Millisecond / 2; !stopped || took > limit {
		t.Fatalf("with a row held the release stopped %v after disconnecting (stopped %v), want well within %v", took, stopped, limit)
	}
	if _, inGTK := w.alive(); inGTK == 0 {
		t.Error("the held row was finalized")
	}

	runtime.KeepAlive(held) // held until here
	inGTK := w.total
	for deadline := time.Now().Add(10 * time.Second); inGTK > 0 && time.Now().Before(deadline); {
		collect()
		_, inGTK = w.alive()
	}
	if inGTK > 0 {
		t.Errorf("%d of %d rows are not finalized after letting go", inGTK, w.total)
	}
}
