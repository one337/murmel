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
// they are freed.
const (
	releaseStepMs    = 50
	releaseMaxRounds = 40
)

// release frees the given widgets of replaced rows (the most deeply nested
// one the tree holds of each row) in steps, see above. It stops when they
// are freed, after releaseMaxRounds at the latest.
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
	if t.releaseRound > 0 {
		return // already running
	}
	t.releaseRound = 1
	go runtime.GC()
	glib.TimeoutAdd(releaseStepMs, func() bool {
		if t.releasing <= 0 || t.releaseRound >= releaseMaxRounds {
			t.releasing, t.releaseRound = 0, 0
			t.releaseGen++
			return false
		}
		t.releaseRound++
		go runtime.GC()
		return true
	})
}
