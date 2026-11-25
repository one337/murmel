package channeltree

import (
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/protocol/gumble"
)

// menuEntryShown reports whether the menu shows an entry labelled label.
func menuEntryShown(pop *gtk.PopoverMenu, label string) bool {
	var walk func(w gtk.Widgetter) bool
	walk = func(w gtk.Widgetter) bool {
		obj := coreglib.InternObject(w)
		if obj.TypeFromInstance().Name() == "GtkModelButton" &&
			obj.ObjectProperty("text") == label && gtk.BaseWidget(w).Visible() {
			return true
		}
		for c := gtk.BaseWidget(w).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
			if walk(c) {
				return true
			}
		}
		return false
	}
	return walk(pop)
}

// openNewUserMenu opens session's context menu and returns that menu (a
// menu closed before may still hang below the row).
func openNewUserMenu(t *testing.T, tree *Tree, session uint32) *gtk.PopoverMenu {
	t.Helper()
	var pop *gtk.PopoverMenu
	onMain(func() {
		tree.showUserMenu(session, 5, 5)
		if tree.menu != nil {
			pop = tree.menu.popover
		}
	})
	if pop == nil {
		t.Fatalf("no context menu for user %d", session)
	}
	return pop
}

// setSuppressed has the server (un)suppress session, a user in channel 1.
func setSuppressed(b *bus.Bus, session uint32, suppressed bool) {
	b.Emit(bus.UserUpdated{
		User:    bus.UserInfo{Session: session, Name: "user-20", Channel: 1, Suppressed: suppressed},
		Changed: bus.UserChangeAudio,
	})
	time.Sleep(2 * rebuildInterval)
}

// "Allow speaking" appears in a user's menu only while that user is
// suppressed, asks the controller to lift it, and goes away when the
// server lets the user speak while the menu is open.
func TestGTKAllowSpeakingWhileSuppressed(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	requests := record[bus.UserUnsuppressRequested](t, b)
	perms := int(gumble.PermissionEnter | gumble.PermissionMuteDeafen)
	b.Emit(bus.ChannelUpdated{Channel: bus.ChannelInfo{ID: 1, Name: "Channel 1", Permissions: &perms}})
	time.Sleep(2 * rebuildInterval)

	pop := openNewUserMenu(t, tree, 20)
	var shown bool
	onMain(func() { shown = menuEntryShown(pop, "Allow speaking") })
	if shown {
		t.Error("\"Allow speaking\" is shown for a user who may speak")
	}
	onMain(func() { pop.Popdown() })
	time.Sleep(2 * rebuildInterval)

	setSuppressed(b, 20, true)
	pop = openNewUserMenu(t, tree, 20)
	onMain(func() { shown = menuEntryShown(pop, "Allow speaking") })
	if !shown {
		t.Fatal("\"Allow speaking\" is missing for a suppressed user")
	}
	onMain(func() {
		pop.ActivateAction("ctx.unsuppress", nil)
		pop.Popdown()
	})
	if got := requests(1); len(got) != 1 || got[0].Session != 20 {
		t.Errorf("activating the entry requested %+v, want user 20", got)
	}
	time.Sleep(2 * rebuildInterval)

	pop = openNewUserMenu(t, tree, 20)
	setSuppressed(b, 20, false)
	onMain(func() { shown = menuEntryShown(pop, "Allow speaking") })
	if shown {
		t.Error("\"Allow speaking\" stays in the open menu after the user may speak again")
	}
	onMain(func() { pop.Popdown() })
}
