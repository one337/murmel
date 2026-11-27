package channeltree

import (
	"context"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
)

// Tab and Shift+Tab leave the list from any row in one step.
func TestGTKTabLeavesTheList(t *testing.T) {
	needGTK(t)
	tree, _, win, _ := focusTree(t, 5, 40)
	waitForFocus(t, tree, win, rowID{user: true, id: usersOf(tree, 2)[0]})
	for _, forward := range []bool{true, false} {
		focusRow(t, tree, rowID{user: true, id: usersOf(tree, 3)[0]})
		onMain(func() { moveFocus(win, forward) })
		if id, ok := focused(tree); ok {
			t.Errorf("Tab (forward %v) moved to row %+v instead of leaving the list", forward, id)
		}
	}
}

// Tab and Shift+Tab from outside enter the list on the row that last had
// the focus, also when its row was replaced meanwhile.
func TestGTKTabReturnsToTheLastRow(t *testing.T) {
	needGTK(t)
	tree, b, win, entry := focusTree(t, 5, 40)
	waitForFocus(t, tree, win, rowID{user: true, id: usersOf(tree, 2)[0]})
	last := rowID{user: true, id: usersOf(tree, 4)[1]}
	for _, forward := range []bool{true, false} {
		focusRow(t, tree, last)
		onMain(func() { entry.GrabFocus() })
		onMain(func() { moveFocus(win, forward) })
		if got, ok := focused(tree); !ok || got != last {
			t.Errorf("Tab (forward %v) entered on %+v (%v), want %+v", forward, got, ok, last)
		}
	}

	// The focus is outside while another user leaves and the row of the
	// last focused user is replaced (the user leaves and comes back).
	focusRow(t, tree, last)
	onMain(func() { entry.GrabFocus() })
	removeAndWait(t, tree, b, last.id)
	b.Emit(bus.UserAdded{User: bus.UserInfo{Session: last.id, Name: "back", Channel: 4}})
	waitFor(t, "the user to be back", func() bool {
		_, ok := tree.userRows[last.id]
		return ok && !tree.rebuildPending && !tree.rebuildScheduled
	})
	time.Sleep(3 * rebuildInterval)
	onMain(func() { moveFocus(win, false) })
	if got, ok := focused(tree); !ok || got != last {
		t.Errorf("after the rebuild Shift+Tab entered on %+v (%v), want %+v", got, ok, last)
	}
}

// Before any row had the focus, Tab and Shift+Tab enter on one's own row.
func TestGTKTabEntersOnOwnRowFirst(t *testing.T) {
	needGTK(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := bus.New(nil)
	var tree *Tree
	var win *gtk.Window
	onMain(func() {
		win = gtk.NewWindow()
		win.SetDefaultSize(320, 600)
		tree = NewTree(ctx, b, win)
		entry := gtk.NewEntry()
		box := gtk.NewBox(gtk.OrientationVertical, 0)
		box.Append(entry) // first: it has the focus when the window opens
		box.Append(tree.Root())
		win.SetChild(box)
		entry.GrabFocus()
		win.Present()
	})
	t.Cleanup(func() { onMain(func() { win.Destroy() }) })
	b.Emit(connectedEvent(5, 40))
	waitFor(t, "the tree to be built", func() bool { return len(tree.userRows) == 40 })
	time.Sleep(3 * rebuildInterval)
	if id, ok := focused(tree); ok {
		t.Fatalf("the list has the focus (%+v) before any Tab", id)
	}
	onMain(func() { moveFocus(win, true) })
	if got, ok := focused(tree); !ok || got != (rowID{user: true, id: 1}) {
		t.Errorf("Tab entered on %+v (%v), want one's own row (session 1)", got, ok)
	}
}

// When the row that last had the focus is gone, Tab enters on the chat
// target.
func TestGTKTabEntersOnTargetWhenLastRowWent(t *testing.T) {
	needGTK(t)
	tree, b, win, entry := focusTree(t, 5, 40)
	waitForFocus(t, tree, win, rowID{user: true, id: usersOf(tree, 2)[0]})
	target := rowID{user: true, id: usersOf(tree, 4)[0]}
	focusRow(t, tree, target)
	clickRow(t, tree, target)
	gone := rowID{user: true, id: usersOf(tree, 3)[0]}
	focusRow(t, tree, gone)
	onMain(func() { entry.GrabFocus() })
	removeAndWait(t, tree, b, gone.id)
	onMain(func() { moveFocus(win, false) })
	if got, ok := focused(tree); !ok || got != target {
		t.Errorf("Shift+Tab entered on %+v (%v), want the chat target %+v", got, ok, target)
	}
}

// FocusEntry (F6) enters the list where Tab would: on the row that last
// had the focus; with the focus already in the list it stays there.
func TestGTKFocusEntryEntersLikeTab(t *testing.T) {
	needGTK(t)
	tree, _, win, entry := focusTree(t, 5, 40)
	waitForFocus(t, tree, win, rowID{user: true, id: usersOf(tree, 2)[0]})
	last := rowID{user: true, id: usersOf(tree, 4)[1]}
	focusRow(t, tree, last)
	onMain(func() { entry.GrabFocus() })
	var ok bool
	onMain(func() { ok = tree.FocusEntry() })
	if got, in := focused(tree); !ok || !in || got != last {
		t.Errorf("FocusEntry from outside: %v, focus on %+v (%v), want %+v", ok, got, in, last)
	}
	other := rowID{id: 3}
	focusRow(t, tree, other)
	onMain(func() { ok = tree.FocusEntry() })
	if got, in := focused(tree); !ok || !in || got != other {
		t.Errorf("FocusEntry from inside: %v, focus on %+v (%v), want %+v", ok, got, in, other)
	}
}

// Without rows (not connected) FocusEntry does nothing.
func TestGTKFocusEntryWithoutRows(t *testing.T) {
	needGTK(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var win *gtk.Window
	var ok bool
	onMain(func() {
		win = gtk.NewWindow()
		tree := NewTree(ctx, bus.New(nil), win)
		win.SetChild(tree.Root())
		win.Present()
		ok = tree.FocusEntry()
	})
	t.Cleanup(func() { onMain(func() { win.Destroy() }) })
	if ok {
		t.Error("FocusEntry reported the focus in an empty list")
	}
}
