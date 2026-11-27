package channeltree

import (
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
)

// The list hands its keys to Tree.onKey before handling them itself
// (the tests below call onKey directly: emitting key-pressed from Go
// loses the handler's answer).
func TestGTKListKeysAreCaptured(t *testing.T) {
	needGTK(t)
	tree, _, _, _ := focusTree(t, 2, 4)
	onMain(func() {
		ctrls := tree.list.ObserveControllers()
		for i := uint(0); i < ctrls.NItems(); i++ {
			if k, ok := ctrls.Item(i).Cast().(*gtk.EventControllerKey); ok && k.PropagationPhase() == gtk.PhaseCapture {
				return
			}
		}
		t.Error("the list has no key controller in the capture phase")
	})
}

// keys stands for the list's key controller: it passes key presses to
// the tree.
type keys struct{ tree *Tree }

func press(k keys, keyval uint, state gdk.ModifierType) bool {
	return k.tree.onKey(keyval, 0, state)
}

// keyTree is a focus tree whose focus is on a user of channel 2 (not the
// local user), with the focus ring shown as after keyboard use, and with
// all permissions (so that every menu has entries).
func keyTree(t *testing.T) (*Tree, *bus.Bus, *gtk.Window, *gtk.Entry, keys) {
	t.Helper()
	tree, b, win, entry := focusTree(t, 5, 40)
	all := int(^uint(0) >> 1)
	for _, c := range connectedEvent(5, 0).Channels {
		c.Permissions = &all
		b.Emit(bus.ChannelUpdated{Channel: c})
	}
	time.Sleep(2 * rebuildInterval)
	focusRow(t, tree, rowID{user: true, id: usersOf(tree, 2)[1]})
	onMain(func() { win.SetFocusVisible(true) })
	return tree, b, win, entry, keys{tree}
}

// The arrow keys (and Page Down, End) move the cursor over the users
// without selecting them: no conversation opens and the focus stays in
// the list.
func TestGTKArrowKeysMoveOnlyTheCursor(t *testing.T) {
	needGTK(t)
	tree, b, _, _, keys := keyTree(t)
	targets := record[bus.ChatTargetChanged](t, b)
	order := listOrder(tree)

	for i, key := range []uint{gdk.KEY_Down, gdk.KEY_Down, gdk.KEY_Down, gdk.KEY_Up, gdk.KEY_KP_Down} {
		before, _ := focused(tree)
		var handled bool
		onMain(func() { handled = press(keys, key, 0) })
		after, ok := focused(tree)
		if !handled || !ok || after == before {
			t.Fatalf("key %d: handled %v, focus moved from %+v to %+v (in the list %v)", i, handled, before, after, ok)
		}
		if shown, _ := marked(tree); len(shown) != 0 {
			t.Errorf("key %d marked %+v as the chat target", i, shown)
		}
	}
	for _, key := range []uint{gdk.KEY_Page_Down, gdk.KEY_End, gdk.KEY_Home} {
		onMain(func() { press(keys, key, 0) })
		if _, ok := focused(tree); !ok {
			t.Errorf("key %d took the focus out of the list", key)
		}
	}
	if got, _ := focused(tree); got != order[0] {
		t.Errorf("Home put the cursor on %+v, want the first row %+v", got, order[0])
	}
	if got := targets(0); len(got) != 0 {
		t.Errorf("moving the cursor changed the chat target: %+v", got)
	}
}

// With Shift the arrow keys also only move the cursor; with Ctrl or Alt
// they are left to GTK.
func TestGTKArrowKeysWithModifiers(t *testing.T) {
	needGTK(t)
	_, b, _, _, keys := keyTree(t)
	targets := record[bus.ChatTargetChanged](t, b)
	onMain(func() {
		if !press(keys, gdk.KEY_Down, gdk.ShiftMask) {
			t.Error("Shift+Down was not handled")
		}
		if press(keys, gdk.KEY_Down, gdk.ControlMask) || press(keys, gdk.KEY_Down, gdk.AltMask) {
			t.Error("Ctrl+Down or Alt+Down was taken from GTK")
		}
	})
	if got := targets(0); len(got) != 0 {
		t.Errorf("Shift+Down changed the chat target: %+v", got)
	}
}

// Enter on a user opens the conversation, also on the user who is
// selected already (the cursor went away and came back).
func TestGTKEnterOnUserOpensConversation(t *testing.T) {
	needGTK(t)
	tree, b, _, _, keys := keyTree(t)
	targets := record[bus.ChatTargetChanged](t, b)
	onMain(func() { press(keys, gdk.KEY_Down, 0) })
	user, _ := focused(tree)
	if !user.user {
		t.Fatalf("the cursor is on %+v, not on a user", user)
	}

	onMain(func() { coreglib.BaseObject(tree.list).Emit("activate-cursor-row") })
	got := targets(1)
	if len(got) == 0 || got[len(got)-1].Session != user.id {
		t.Fatalf("Enter on user %d sent %+v", user.id, got)
	}

	// The user is selected now; the cursor goes elsewhere and back.
	other := rowID{user: true, id: usersOf(tree, 3)[0]}
	focusRow(t, tree, other)
	focusRow(t, tree, user)
	n := len(targets(0))
	onMain(func() { coreglib.BaseObject(tree.list).Emit("activate-cursor-row") })
	got = targets(n + 1)
	if len(got) <= n || got[len(got)-1].Session != user.id {
		t.Errorf("Enter on the selected user %d sent %+v", user.id, got[n:])
	}
}

// Enter on a channel joins it, as before.
func TestGTKEnterOnChannelJoins(t *testing.T) {
	needGTK(t)
	tree, b, _, _, _ := keyTree(t)
	joins := record[bus.ChannelJoinRequested](t, b)
	focusRow(t, tree, rowID{id: 3})
	onMain(func() { coreglib.BaseObject(tree.list).Emit("activate-cursor-row") })
	if got := joins(1); len(got) != 1 || got[0].ChannelID != 3 {
		t.Errorf("Enter on channel 3 sent %+v", got)
	}
}

// The mouse works as before: a click on a user row opens the
// conversation, also on the user who is the chat target already.
func TestGTKMouseStillOpensConversation(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	targets := record[bus.ChatTargetChanged](t, b)
	user := usersOf(tree, 2)[0]
	clickRow(t, tree, rowID{user: true, id: user})
	if got := targets(1); len(got) != 1 || got[0].Session != user {
		t.Fatalf("clicking user %d sent %+v", user, got)
	}
	clickRow(t, tree, rowID{user: true, id: user})
	if got := targets(2); len(got) != 2 || got[1].Session != user {
		t.Errorf("clicking the user %d again sent %+v", user, got)
	}
}

// shownMenu returns the context menu shown on id's row.
func shownMenu(t *testing.T, tree *Tree, id rowID) *gtk.PopoverMenu {
	t.Helper()
	var pop *gtk.PopoverMenu
	onMain(func() {
		if row := tree.rowFor(id); row != nil {
			pop, _ = find[*gtk.PopoverMenu](row)
		}
	})
	if pop == nil {
		t.Fatalf("no context menu on %+v", id)
	}
	return pop
}

// The Menu key and Shift+F10 open the focused row's menu, pointing at the
// whole row. Closed, the focus is back on the row, also when the list was
// rebuilt meanwhile (another user left while the menu was open).
func TestGTKMenuKeysOpenTheRowMenu(t *testing.T) {
	needGTK(t)
	for _, c := range []struct {
		name  string
		key   uint
		state gdk.ModifierType
		row   func(tree *Tree) rowID
	}{
		{"Menu on a user", gdk.KEY_Menu, 0, func(tree *Tree) rowID { return rowID{user: true, id: usersOf(tree, 3)[1]} }},
		{"Shift+F10 on a user", gdk.KEY_F10, gdk.ShiftMask, func(tree *Tree) rowID { return rowID{user: true, id: usersOf(tree, 3)[1]} }},
		{"Menu on a channel", gdk.KEY_Menu, 0, func(*Tree) rowID { return rowID{id: 3} }},
		{"Shift+F10 on a channel", gdk.KEY_F10, gdk.ShiftMask, func(*Tree) rowID { return rowID{id: 3} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			tree, b, _, _, keys := keyTree(t)
			id := c.row(tree)
			focusRow(t, tree, id)
			time.Sleep(2 * rebuildInterval) // the list scrolls to the row
			var handled bool
			onMain(func() { handled = press(keys, c.key, c.state) })
			if !handled {
				t.Fatal("the key was not handled")
			}
			pop := shownMenu(t, tree, id)
			time.Sleep(2 * rebuildInterval)
			onMain(func() {
				rect, ok := pop.PointingTo()
				row := tree.rowFor(id)
				if !pop.Mapped() || !ok || rect.X() != 0 || rect.Y() != 0 || rect.Width() != row.Width() || rect.Height() != row.Height() {
					t.Errorf("menu on screen %v, pointing at %v %d,%d %dx%d, want the whole row %dx%d",
						pop.Mapped(), ok, rect.X(), rect.Y(), rect.Width(), rect.Height(), row.Width(), row.Height())
				}
			})

			leaveAndSettle(b, usersOf(tree, 1)[0])
			onMain(func() { pop.Popdown() })
			time.Sleep(2 * rebuildInterval)
			if got, ok := focused(tree); !ok || got != id {
				t.Errorf("after the menu closed the focus is on %+v (%v), want %+v", got, ok, id)
			}
			checkListMatchesModel(t, tree)
		})
	}
}

// An entry that moves the focus elsewhere (the composer for "Send
// message", a dialog for "Kick…") keeps it there: the focus does not go
// back to the row.
func TestGTKMenuEntryKeepsTheFocusItMoved(t *testing.T) {
	needGTK(t)
	tree, _, _, entry, keys := keyTree(t)
	id, _ := focused(tree)
	onMain(func() { press(keys, gdk.KEY_Menu, 0) })
	pop := shownMenu(t, tree, id)
	time.Sleep(2 * rebuildInterval)
	onMain(func() {
		pop.Popdown()
		entry.GrabFocus()
	})
	time.Sleep(2 * rebuildInterval)
	var inEntry bool
	onMain(func() {
		if f := tree.list.Root().Focus(); f != nil {
			inEntry = coreglib.BaseObject(f).Native() == coreglib.BaseObject(entry).Native() || gtk.BaseWidget(f).IsAncestor(entry)
		}
	})
	if row, ok := focused(tree); ok || !inEntry {
		t.Errorf("the focus is on row %+v (%v), in the entry %v; want it in the entry", row, ok, inEntry)
	}
}

// In the user menu, Up and Down on the volume slider go to the entries
// above and below it; the value does not change.
func TestGTKVolumeSliderUpDownLeaveIt(t *testing.T) {
	needGTK(t)
	tree, _, _, _, keys := keyTree(t)
	id, _ := focused(tree)
	onMain(func() { press(keys, gdk.KEY_Menu, 0) })
	pop := shownMenu(t, tree, id)
	time.Sleep(2 * rebuildInterval)
	var scale *gtk.Scale
	onMain(func() { scale, _ = find[*gtk.Scale](pop) })
	if scale == nil {
		t.Fatal("no volume slider in the menu")
	}
	for _, key := range []uint{gdk.KEY_Down, gdk.KEY_Up} {
		var handled, still bool
		var value float64
		onMain(func() {
			scale.GrabFocus()
			handled = sliderKey(scale, key, 0)
			still = scale.HasFocus()
			value = scale.Value()
		})
		if !handled || still || value != 100 {
			t.Errorf("key %d on the slider: handled %v, slider still focused %v, value %.0f; want the focus on another entry and 100", key, handled, still, value)
		}
	}
	onMain(func() { pop.Popdown() })
}

// Assistive technologies learn that channel and user rows have a menu.
func TestGTKRowsAnnounceTheirMenu(t *testing.T) {
	needGTK(t)
	tree, _, _, _ := focusTree(t, 5, 40)
	onMain(func() {
		for _, id := range []rowID{{id: 3}, {user: true, id: 20}} {
			if !gtk.TestAccessibleHasProperty(tree.rowFor(id), gtk.AccessiblePropertyHasPopup) {
				t.Errorf("%+v does not announce a menu", id)
			}
		}
	})
}
