package channeltree

import (
	"runtime"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// gotk4 frees a widget only after Go's garbage collector found its Go side
// unreachable, and a row's nested widgets one level per collection. The
// collector runs as Go's own heap grows, which rows hardly add to: the
// rows replaced on connecting or disconnecting (hundreds on a big server)
// would hold their memory until then and be freed all at once, blocking
// the main loop. release runs collections for them in small steps until
// they are freed. Under load some rows stay reachable in Go for a while
// although GTK no longer holds them; release then gives up once no row
// was freed for releaseStallRounds, and the collector frees them later.
const (
	releaseStepMs      = 50
	releaseStallRounds = 40  // 2 s without another row freed
	releaseMaxRounds   = 400 // 20 s at most
)

// release frees the given widgets of replaced rows (the most deeply nested
// one the tree holds of each row) in steps, see above. It stops when they
// are freed, when none was freed for releaseStallRounds, and after
// releaseMaxRounds at the latest.
func (t *Tree) release(leaves []gtk.Widgetter) {
	if len(leaves) == 0 {
		return
	}
	gen := t.releaseGen
	for _, w := range leaves {
		t.releasing++
		coreglib.WeakRefObject(w, func() {
			// The notify may run while GTK finalizes the widget: only
			// count, on the main loop.
			glib.IdleAdd(func() bool {
				if t.releaseGen == gen {
					t.releasing--
				}
				return false
			})
		})
	}
	t.releaseLeft, t.releaseIdle = t.releasing, 0
	if t.releaseRound > 0 {
		return // already running
	}
	t.releaseRound = 1
	go runtime.GC()
	glib.TimeoutAdd(releaseStepMs, func() bool {
		if t.releasing < t.releaseLeft {
			t.releaseLeft, t.releaseIdle = t.releasing, 0
		} else {
			t.releaseIdle++
		}
		if t.releasing <= 0 || t.releaseIdle >= releaseStallRounds || t.releaseRound >= releaseMaxRounds {
			t.releasing, t.releaseRound = 0, 0
			t.releaseLeft, t.releaseIdle = 0, 0
			t.releaseGen++
			return false
		}
		t.releaseRound++
		go runtime.GC()
		return true
	})
}
