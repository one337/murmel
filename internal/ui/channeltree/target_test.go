package channeltree

import (
	"testing"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
)

// clickRow sends a single click with the primary button to id's row.
func clickRow(t *testing.T, tree *Tree, id rowID) {
	t.Helper()
	clicked := false
	onMain(func() {
		ctrls := tree.rowFor(id).ObserveControllers()
		for i := uint(0); i < ctrls.NItems(); i++ {
			if g, ok := ctrls.Item(i).Cast().(*gtk.GestureClick); ok && g.Button() == gdk.BUTTON_PRIMARY {
				coreglib.BaseObject(g).Emit("released", 1, 5.0, 5.0)
				clicked = true
			}
		}
	})
	if !clicked {
		t.Fatalf("the row of %+v takes no click", id)
	}
}

// marked lists the rows shown as the chat target, and those that tell
// assistive technologies they are selected.
func marked(tree *Tree) (shown, told []rowID) {
	onMain(func() {
		for _, id := range tree.listOrder() {
			row := tree.rowFor(id)
			if row.StateFlags()&gtk.StateFlagSelected != 0 {
				shown = append(shown, id)
			}
			if gtk.TestAccessibleHasState(row, gtk.AccessibleStateSelected) {
				told = append(told, id)
			}
		}
	})
	return shown, told
}

// checkMarked fails unless exactly want's row is marked, to the eye and
// for assistive technologies.
func checkMarked(t *testing.T, tree *Tree, want rowID, what string) {
	t.Helper()
	shown, told := marked(tree)
	if len(shown) != 1 || shown[0] != want || len(told) != 1 || told[0] != want {
		t.Errorf("%s: rows shown as the chat target %+v, told as selected %+v, want only %+v", what, shown, told, want)
	}
}

// moveFocus moves the keyboard focus as Tab (forward) or Shift+Tab does.
func moveFocus(win *gtk.Window, forward bool) {
	dir := gtk.DirTabBackward
	if forward {
		dir = gtk.DirTabForward
	}
	coreglib.BaseObject(win).Emit("move-focus", dir)
}

// Tab and Shift+Tab into and through the list open no conversation and
// leave the chat target as it is.
func TestGTKTabbingOpensNoConversation(t *testing.T) {
	needGTK(t)
	tree, b, win, entry := focusTree(t, 5, 40)
	waitForFocus(t, tree, win, rowID{user: true, id: usersOf(tree, 2)[0]})
	picked := rowID{user: true, id: usersOf(tree, 3)[0]}
	clicked := record[bus.ChatTargetChanged](t, b)
	clickRow(t, tree, picked)
	clicked(1) // the bus delivers the click's event later
	onMain(func() { entry.GrabFocus() })

	targets := record[bus.ChatTargetChanged](t, b)
	var visited []rowID
	for _, forward := range []bool{false, false, false, false, true, true} {
		onMain(func() { moveFocus(win, forward) })
		if id, ok := focused(tree); ok {
			visited = append(visited, id)
		}
	}
	if len(visited) == 0 {
		t.Fatal("Tab and Shift+Tab never reached the list")
	}
	if got := targets(0); len(got) != 0 {
		t.Errorf("moving the focus over %+v changed the chat target: %+v", visited, got)
	}
	checkMarked(t, tree, picked, "after tabbing")
}

// Ctrl+Space (GTK: toggle the row) does nothing in the list.
func TestGTKCtrlSpaceDoesNothing(t *testing.T) {
	needGTK(t)
	tree, b, _, _, keys := keyTree(t)
	targets := record[bus.ChatTargetChanged](t, b)
	joins := record[bus.ChannelJoinRequested](t, b)
	for _, id := range []rowID{{user: true, id: usersOf(tree, 2)[0]}, {id: 3}} {
		focusRow(t, tree, id)
		for _, key := range []uint{gdk.KEY_space, gdk.KEY_KP_Space} {
			var handled bool
			onMain(func() { handled = press(keys, key, gdk.ControlMask) })
			if !handled {
				t.Errorf("Ctrl+Space (%d) on %+v was left to GTK", key, id)
			}
		}
	}
	if got := targets(0); len(got) != 0 {
		t.Errorf("Ctrl+Space changed the chat target: %+v", got)
	}
	if got := joins(0); len(got) != 0 {
		t.Errorf("Ctrl+Space joined: %+v", got)
	}
}

// A click on a channel or a user and Enter on a user mark that row as the
// chat target (only that row).
func TestGTKClickAndEnterMarkTheChatTarget(t *testing.T) {
	needGTK(t)
	tree, b, _, _, _ := keyTree(t)
	targets := record[bus.ChatTargetChanged](t, b)

	clickRow(t, tree, rowID{id: 3})
	if got := targets(1); len(got) != 1 || got[0].ChannelID != 3 {
		t.Fatalf("a click on channel 3 sent %+v", got)
	}
	checkMarked(t, tree, rowID{id: 3}, "click on channel 3")

	user := rowID{user: true, id: usersOf(tree, 2)[0]}
	clickRow(t, tree, user)
	checkMarked(t, tree, user, "click on a user")

	other := rowID{user: true, id: usersOf(tree, 4)[0]}
	focusRow(t, tree, other)
	onMain(func() { coreglib.BaseObject(tree.list).Emit("activate-cursor-row") })
	if got := targets(3); len(got) != 3 || got[2].Session != other.id {
		t.Fatalf("Enter on user %d sent %+v", other.id, got)
	}
	checkMarked(t, tree, other, "Enter on a user")
}
