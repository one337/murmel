package channeltree

import (
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
)

// find returns the first widget below w (w included) of type T.
func find[T any](w gtk.Widgetter) (T, bool) {
	if x, ok := coreglib.InternObject(w).Cast().(T); ok {
		return x, true
	}
	for c := gtk.BaseWidget(w).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		if x, ok := find[T](c); ok {
			return x, true
		}
	}
	var zero T
	return zero, false
}

// dragSource returns the drag gesture on a user row's handle.
func dragSource(t *testing.T, tree *Tree, session uint32) *gtk.DragSource {
	t.Helper()
	var src *gtk.DragSource
	onMain(func() {
		ctrls := tree.userRows[session].handle.ObserveControllers()
		for i := uint(0); i < ctrls.NItems(); i++ {
			if s, ok := ctrls.Item(i).Cast().(*gtk.DragSource); ok {
				src = s
			}
		}
	})
	if src == nil {
		t.Fatalf("user %d has no drag source on the handle", session)
	}
	return src
}

// leaveAndSettle has session leave and waits long enough for a rebuild
// that would run to have run.
func leaveAndSettle(b *bus.Bus, sessions ...uint32) {
	for _, s := range sessions {
		b.Emit(bus.UserRemoved{Session: s})
	}
	time.Sleep(4 * rebuildInterval)
}

// openUserMenu opens the context menu of session and returns it.
func openUserMenu(t *testing.T, tree *Tree, session uint32) *gtk.PopoverMenu {
	t.Helper()
	var pop *gtk.PopoverMenu
	onMain(func() {
		tree.showUserMenu(session, 5, 5)
		pop, _ = find[*gtk.PopoverMenu](tree.userRows[session].root)
	})
	if pop == nil {
		t.Fatalf("no context menu for user %d", session)
	}
	return pop
}

// While the mouse button is down on a drag handle, a user leaving does not
// rebuild the list (the row under the pointer stays); released without a
// drag, the list catches up at once.
func TestGTKRebuildWaitsWhileHandlePressed(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	src := dragSource(t, tree, 20)
	onMain(func() { coreglib.InternObject(src).Emit("begin", nil) })

	leaveAndSettle(b, 3)
	var old bool
	onMain(func() { _, old = tree.userRows[3] })
	if !old {
		t.Error("the list was rebuilt while the button was down on a drag handle")
	}
	var caughtUp bool
	onMain(func() {
		coreglib.InternObject(src).Emit("end", nil)
		_, still := tree.userRows[3]
		caughtUp = !still
	})
	if !caughtUp {
		t.Error("releasing the handle did not rebuild the list at once")
	}
	checkListMatchesModel(t, tree)
}

// While a user's context menu is open, another user leaving does not
// rebuild the list: the menu stays on screen and its volume slider still
// sets that user's volume. Closing the menu rebuilds at once.
func TestGTKRebuildWaitsWhileMenuOpen(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	volume := record[bus.UserLocalAudioRequested](t, b)
	pop := openUserMenu(t, tree, 20)
	time.Sleep(2 * rebuildInterval) // the menu is shown
	onMain(func() {
		if !pop.Mapped() {
			t.Fatal("the context menu is not on screen")
		}
	})

	leaveAndSettle(b, 3)
	var mapped, old bool
	onMain(func() {
		mapped = pop.Mapped()
		_, old = tree.userRows[3]
		scale, _ := find[*gtk.Scale](pop)
		scale.SetValue(50)
	})
	if !mapped || !old {
		t.Errorf("another user left while the menu was open: menu on screen %v, list not rebuilt %v; want both", mapped, old)
	}
	got := volume(1)
	if len(got) == 0 || got[len(got)-1].Session != 20 || got[len(got)-1].Volume != 0.5 {
		t.Errorf("the volume slider requested %+v, want 50 %% for user 20", got)
	}

	var caughtUp bool
	onMain(func() {
		pop.Popdown()
		_, still := tree.userRows[3]
		caughtUp = !still && tree.menuRefresh == nil
	})
	if !caughtUp {
		t.Error("closing the menu did not rebuild the list at once")
	}
	checkListMatchesModel(t, tree)
}

// The user whose menu is open leaves: the menu closes and the list is
// rebuilt.
func TestGTKMenuClosesWhenItsUserLeaves(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	pop := openUserMenu(t, tree, 20)
	leaveAndSettle(b, 20)
	onMain(func() {
		if pop.Visible() || tree.menuRefresh != nil {
			t.Error("the menu of the user who left is still open")
		}
		if _, ok := tree.userRows[20]; ok {
			t.Error("the user who left still has a row")
		}
	})
	checkListMatchesModel(t, tree)
}

// The channel whose menu is open is removed: the menu closes and the list
// is rebuilt.
func TestGTKMenuClosesWhenItsChannelGoes(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 6, 5) // channel 1 has no users
	var pop *gtk.PopoverMenu
	onMain(func() {
		tree.showChannelMenu(1, 5, 5)
		pop, _ = find[*gtk.PopoverMenu](tree.channelRows[1].root)
	})
	if pop == nil {
		t.Fatal("no context menu for channel 1")
	}
	b.Emit(bus.ChannelRemoved{ID: 1})
	time.Sleep(4 * rebuildInterval)
	onMain(func() {
		if pop.Visible() || tree.menuRefresh != nil {
			t.Error("the menu of the removed channel is still open")
		}
		if _, ok := tree.channelRows[1]; ok {
			t.Error("the removed channel still has a row")
		}
	})
	checkListMatchesModel(t, tree)
}

// A dialog opened from a user's menu (Kick…) still acts on that user after
// another user left and the list was rebuilt.
func TestGTKDialogFromMenuSurvivesRebuild(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	kicks := record[bus.UserKickRequested](t, b)
	all := int(^uint(0) >> 1)
	b.Emit(bus.ChannelUpdated{Channel: bus.ChannelInfo{ID: 0, Name: "Root", Permissions: &all}})
	time.Sleep(2 * rebuildInterval)

	pop := openUserMenu(t, tree, 20)
	// A click on the entry runs it and closes the menu.
	onMain(func() {
		pop.ActivateAction("ctx.kick", nil)
		pop.Popdown()
	})
	leaveAndSettle(b, 3)

	var alert *adw.AlertDialog
	onMain(func() {
		if _, ok := tree.userRows[3]; ok {
			t.Error("the list was not rebuilt after the menu closed")
		}
		toplevels := gtk.WindowGetToplevels()
		for i := uint(0); i < toplevels.NItems() && alert == nil; i++ {
			if w, ok := toplevels.Item(i).Cast().(gtk.Widgetter); ok {
				alert, _ = find[*adw.AlertDialog](w)
			}
		}
	})
	if alert == nil {
		t.Fatal("the kick dialog did not open")
	}
	onMain(func() { coreglib.InternObject(alert).Emit("response", "kick") })
	got := kicks(1)
	if len(got) != 1 || got[0].Session != 20 {
		t.Errorf("the kick dialog requested %+v, want a kick of user 20", got)
	}
	onMain(func() { alert.ForceClose() })
}
