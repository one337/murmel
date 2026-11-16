package weakref

import (
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

type owner struct{ n int }

// collect runs garbage collections and the default main context until
// done reports true or a second passes.
func collect(done func() bool) bool {
	for range 100 {
		runtime.GC()
		for glib.MainContextDefault().Iteration(false) {
		}
		if done() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// Owned keeps the owner while the object exists and releases it, on the
// main loop, once the object is gone; the anchors do not pile up.
func TestOwnedLivesAsLongAsTheObject(t *testing.T) {
	const n = 100
	objs := make([]*gio.SimpleAction, n)
	owners := make([]weak.Pointer[owner], n)
	for i := range n {
		objs[i] = gio.NewSimpleAction("a", nil)
		owners[i] = Owned(objs[i], &owner{n: i})
	}
	if len(anchors) != n {
		t.Fatalf("%d anchors, want %d", len(anchors), n)
	}
	// While the objects exist, their owners stay.
	collect(func() bool { return false })
	for i, p := range owners {
		if p.Value() == nil {
			t.Fatalf("owner %d freed while its object exists", i)
		}
	}
	runtime.KeepAlive(objs)

	objs = nil
	if !collect(func() bool { return len(anchors) == 0 }) {
		t.Fatalf("%d anchors left after the objects were freed", len(anchors))
	}
	alive := 0
	collect(func() bool {
		alive = 0
		for _, p := range owners {
			if p.Value() != nil {
				alive++
			}
		}
		return alive == 0
	})
	if alive != 0 {
		t.Fatalf("%d owners still alive", alive)
	}
}

// A Ref reports the object only while it exists.
func TestRefGetAfterTheObjectIsGone(t *testing.T) {
	obj := gio.NewSimpleAction("a", nil)
	r := New(obj)
	if got, ok := r.Get(); !ok || got == nil {
		t.Fatal("Get failed while the object exists")
	}
	runtime.KeepAlive(obj)
	obj = nil
	gone := collect(func() bool { _, ok := r.Get(); return !ok })
	if !gone {
		t.Fatal("Get still returns the object after it was freed")
	}
	called := false
	r.Do(func(*gio.SimpleAction) { called = true })
	if called {
		t.Fatal("Do ran without the object")
	}
}
