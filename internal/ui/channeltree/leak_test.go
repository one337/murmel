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

// Rebuilding the tree frees the rows it replaced, on the Go side (the row
// structs) and in GTK (the row widgets are finalized).
func TestGTKRebuildFreesOldRows(t *testing.T) {
	needGTK(t)
	const channels, users, rebuilds = 30, 100, 20

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
	built := false
	for deadline := time.Now().Add(5 * time.Second); !built && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		onMain(func() { built = len(tree.userRows) == users && len(tree.channelRows) == channels+1 })
	}
	if !built {
		t.Fatal("the tree was not built")
	}

	// Watch the first generation of rows: weakly on the Go side, by GObject
	// finalization in GTK.
	var structs []weak.Pointer[userRowWidgets]
	var channelStructs []weak.Pointer[channelRowWidgets]
	var finalized atomic.Int32
	total := 0
	onMain(func() {
		for _, r := range tree.userRows {
			structs = append(structs, weak.Make(r))
			coreglib.WeakRefObject(r.root, func() { finalized.Add(1) })
			total++
		}
		for _, r := range tree.channelRows {
			channelStructs = append(channelStructs, weak.Make(r))
			coreglib.WeakRefObject(r.root, func() { finalized.Add(1) })
			total++
		}
	})

	for range rebuilds {
		onMain(func() { tree.rebuild() })
	}
	onMain(func() {
		if len(tree.userRows) != users {
			t.Fatalf("after the rebuilds the tree shows %d users, want %d", len(tree.userRows), users)
		}
	})

	// Only a small remainder may still wait for the next collection.
	const tolerance = 2
	var aliveGo, aliveGTK int
	for range 3 {
		collect()
		aliveGo = 0
		for _, p := range structs {
			if p.Value() != nil {
				aliveGo++
			}
		}
		for _, p := range channelStructs {
			if p.Value() != nil {
				aliveGo++
			}
		}
		aliveGTK = total - int(finalized.Load())
		if aliveGo <= tolerance && aliveGTK <= tolerance {
			return
		}
	}
	t.Fatalf("after %d rebuilds %d of %d old rows are still held in Go and %d are not finalized in GTK (tolerance %d)",
		rebuilds, aliveGo, total, aliveGTK, tolerance)
}
