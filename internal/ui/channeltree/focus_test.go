package channeltree

import (
	"context"
	"sync"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// focusTree is batchTree with a text entry below the list, to have a
// focus target outside it.
func focusTree(t *testing.T, channels, users int) (*Tree, *bus.Bus, *gtk.Window, *gtk.Entry) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := bus.New(nil)
	var tree *Tree
	var win *gtk.Window
	var entry *gtk.Entry
	onMain(func() {
		win = gtk.NewWindow()
		win.SetDefaultSize(320, 600)
		tree = NewTree(ctx, b, win)
		entry = gtk.NewEntry()
		box := gtk.NewBox(gtk.OrientationVertical, 0)
		box.Append(tree.Root())
		box.Append(entry)
		win.SetChild(box)
		win.Present()
	})
	t.Cleanup(func() { onMain(func() { win.Destroy() }) })
	b.Emit(connectedEvent(channels, users))
	waitFor(t, "the tree to be built", func() bool {
		return len(tree.userRows) == users && len(tree.channelRows) == channels+1
	})
	time.Sleep(3 * rebuildInterval)
	return tree, b, win, entry
}

// focused names the row that has the keyboard focus (ok false if none).
func focused(tree *Tree) (id rowID, ok bool) {
	onMain(func() {
		root := tree.list.Root()
		if root == nil {
			return
		}
		if row := focusedRow(root.Focus(), tree.list); row != nil {
			id, ok = tree.rowIDOf(row)
		}
	})
	return id, ok
}

// focusRow gives the row of id the keyboard focus.
func focusRow(t *testing.T, tree *Tree, id rowID) {
	t.Helper()
	onMain(func() { tree.rowFor(id).GrabFocus() })
	if got, ok := focused(tree); !ok || got != id {
		t.Fatalf("could not focus %+v (focus on %+v, %v)", id, got, ok)
	}
}

// usersOf returns the users shown in channel ch, in list order.
func usersOf(tree *Tree, ch uint32) []uint32 {
	var out []uint32
	onMain(func() {
		for _, e := range tree.model.walk() {
			if e.kind != entryChannel && tree.model.users[e.id].Channel == ch {
				out = append(out, e.id)
			}
		}
	})
	return out
}

// removeAndWait has sessions leave in one pass and waits for the rebuild.
func removeAndWait(t *testing.T, tree *Tree, b *bus.Bus, sessions ...uint32) {
	t.Helper()
	var evs []bus.Event
	for _, s := range sessions {
		evs = append(evs, bus.UserRemoved{Session: s})
	}
	inOnePass(b, evs, func() {})
	waitFor(t, "the rebuild", func() bool {
		for _, s := range sessions {
			if _, ok := tree.userRows[s]; ok {
				return false
			}
		}
		return !tree.rebuildPending && !tree.rebuildScheduled
	})
	time.Sleep(3 * rebuildInterval) // let layout and any scrolling settle
}

// record collects the events of type T the bus delivers from now on. The
// returned func waits until at least want of them arrived (2 s at most;
// the bus delivers from its own goroutine) and a little longer for any
// further ones, then returns them all.
func record[T bus.Event](t *testing.T, b *bus.Bus) func(want int) []T {
	var mu sync.Mutex
	var got []T
	stop := bus.Subscribe(b, func(ev T) { mu.Lock(); got = append(got, ev); mu.Unlock() })
	t.Cleanup(stop)
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(got) }
	return func(want int) []T {
		for deadline := time.Now().Add(2 * time.Second); count() < want && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		}
		time.Sleep(100 * time.Millisecond)
		onMain(func() {})
		mu.Lock()
		defer mu.Unlock()
		return append([]T(nil), got...)
	}
}

// Another user leaves (the rows shift): the focused user keeps the focus.
func TestGTKRebuildKeepsFocusOnTheSameUser(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	users := usersOf(tree, 3)
	keep := rowID{user: true, id: users[len(users)-1]}
	focusRow(t, tree, keep)

	// Users of channels above, and one just above in the same channel.
	removeAndWait(t, tree, b, usersOf(tree, 1)[0], usersOf(tree, 2)[0], users[0])

	if got, ok := focused(tree); !ok || got != keep {
		t.Errorf("focus on %+v (%v), want the same user %+v", got, ok, keep)
	}
}

// The focused user leaves: the focus goes to the next user in the same
// channel; when the channel has no user left, to the channel. Enter then
// joins that channel.
func TestGTKFocusedUserLeavesFocusStaysInChannel(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	joins := record[bus.ChannelJoinRequested](t, b)

	const ch = 4
	users := usersOf(tree, ch)
	if len(users) < 3 {
		t.Fatalf("channel %d has %d users, want 3 or more", ch, len(users))
	}
	focusRow(t, tree, rowID{user: true, id: users[0]})
	removeAndWait(t, tree, b, users[0])
	if got, ok := focused(tree); !ok || got != (rowID{user: true, id: users[1]}) {
		t.Fatalf("focus on %+v (%v), want the next user %d in channel %d", got, ok, users[1], ch)
	}

	// The last user in the channel leaves: the channel gets the focus.
	last := users[len(users)-1]
	focusRow(t, tree, rowID{user: true, id: last})
	removeAndWait(t, tree, b, users[1:]...)
	if got, ok := focused(tree); !ok || got != (rowID{id: ch}) {
		t.Fatalf("focus on %+v (%v), want channel %d", got, ok, ch)
	}

	// Enter (the list's activate-cursor-row) joins the focused channel.
	onMain(func() { coreglib.InternObject(tree.list).Emit("activate-cursor-row") })
	got := joins(1)
	if len(got) != 1 || got[0].ChannelID != ch {
		t.Errorf("Enter after the rebuild requested %+v, want to join channel %d", got, ch)
	}
}

// The focus far down the list and the scroll position stay where they
// were.
func TestGTKRebuildKeepsFocusAndScrollFarDown(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 30, 300)
	var keep rowID
	onMain(func() {
		walk := tree.model.walk()
		e := walk[len(walk)*9/10]
		keep = rowID{user: e.kind != entryChannel, id: e.id}
	})
	focusRow(t, tree, keep)
	time.Sleep(3 * rebuildInterval) // the list scrolls to the row
	var before, after float64
	onMain(func() { before = tree.root.VAdjustment().Value() })
	if before < 1000 {
		t.Fatalf("focusing a row far down did not scroll (at %.0f)", before)
	}

	var gone []uint32
	for s := uint32(2); len(gone) < 20; s++ {
		if (rowID{user: true, id: s}) != keep {
			gone = append(gone, s)
		}
	}
	removeAndWait(t, tree, b, gone...)
	onMain(func() { after = tree.root.VAdjustment().Value() })
	if after != before {
		t.Errorf("the scroll position moved from %.0f to %.0f", before, after)
	}
	if got, ok := focused(tree); !ok || got != keep {
		t.Errorf("focus on %+v (%v), want %+v", got, ok, keep)
	}
}

// An arrow key handled right after a rebuild, before the new rows are laid
// out (a key pressed while the main loop was busy rebuilding), moves the
// focus to the next row without scrolling to the top.
func TestGTKArrowKeyRightAfterRebuildKeepsScroll(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 30, 300)
	var keep, next rowID
	onMain(func() {
		walk := tree.model.walk()
		i := len(walk) * 9 / 10
		for walk[i].kind != entryChannel {
			i++
		}
		keep = rowID{id: walk[i].id}
		next = rowID{user: walk[i+1].kind != entryChannel, id: walk[i+1].id}
	})
	focusRow(t, tree, keep)
	time.Sleep(3 * rebuildInterval)
	var before, after float64
	onMain(func() { before = tree.root.VAdjustment().Value() })

	inOnePass(b, []bus.Event{bus.UserRemoved{Session: 2}}, func() {
		tree.flushRebuild()
		coreglib.InternObject(tree.list).Emit("move-cursor", gtk.MovementDisplayLines, 1, false, false)
	})
	time.Sleep(3 * rebuildInterval)
	var pageSize float64
	onMain(func() {
		adj := tree.root.VAdjustment()
		after, pageSize = adj.Value(), adj.PageSize()
	})
	if got, ok := focused(tree); !ok || got != next {
		t.Errorf("focus on %+v (%v) after the arrow key, want the next row %+v", got, ok, next)
	}
	if after < before-pageSize || after > before+pageSize {
		t.Errorf("the arrow key scrolled from %.0f to %.0f, more than a page", before, after)
	}
}

// With the focus outside the list, a rebuild leaves it there.
func TestGTKRebuildLeavesFocusOutsideTheList(t *testing.T) {
	needGTK(t)
	tree, b, win, entry := focusTree(t, 5, 40)
	onMain(func() { entry.GrabFocus() })
	removeAndWait(t, tree, b, 3, 4, 5)
	onMain(func() {
		focus := win.Focus()
		if focus == nil || !gtk.BaseWidget(focus).IsAncestor(entry) && coreglib.InternObject(focus).Native() != coreglib.InternObject(entry).Native() {
			t.Errorf("the focus moved from the entry to %T", focus)
		}
	})
	if id, ok := focused(tree); ok {
		t.Errorf("the list took the focus (row %+v)", id)
	}
}

// The selected row stays selected, and restoring it is no new chat
// target.
func TestGTKRebuildKeepsSelection(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	for _, sel := range []rowID{{user: true, id: 20}, {id: 3}} {
		targets := record[bus.ChatTargetChanged](t, b)
		onMain(func() { tree.list.SelectRow(tree.rowFor(sel)) })
		// Selecting is a new chat target: the test sees that event.
		if len(targets(1)) == 0 {
			t.Fatal("selecting a row did not change the chat target")
		}
		removeAndWait(t, tree, b, usersOf(tree, 1)[0])
		var got rowID
		var ok bool
		onMain(func() {
			if row := tree.list.SelectedRow(); row != nil {
				got, ok = tree.rowIDOf(row)
			}
		})
		if !ok || got != sel {
			t.Errorf("selected %+v (%v) after the rebuild, want %+v", got, ok, sel)
		}
		if ev := targets(1); len(ev) != 1 {
			t.Errorf("restoring the selection of %+v changed the chat target: %+v", sel, ev[1:])
		}
	}
}

// Restoring the focus shows the focus ring only if it was shown.
func TestGTKRebuildKeepsFocusRingVisibility(t *testing.T) {
	needGTK(t)
	tree, b, win, _ := focusTree(t, 5, 40)
	users := usersOf(tree, 2)
	for i, visible := range []bool{false, true} {
		focusRow(t, tree, rowID{user: true, id: users[i+1]})
		onMain(func() { win.SetFocusVisible(visible) })
		removeAndWait(t, tree, b, users[i+1]) // the focused row goes
		var got bool
		onMain(func() { got = win.FocusVisible() })
		if got != visible {
			t.Errorf("focus ring shown %v after the rebuild, want %v as before", got, visible)
		}
		if _, ok := focused(tree); !ok {
			t.Error("no row has the focus after the rebuild")
		}
	}
}
