package channeltree

import (
	"testing"
	"time"

	"io.github/murmel/internal/bus"
)

// userRowState returns what user session's row shows: whether the muted
// icon is visible, its tooltip, and the row's accessible description.
func userRowState(t *testing.T, tree *Tree, session uint32) (muted bool, tip, desc string) {
	t.Helper()
	onMain(func() {
		r, ok := tree.userRows[session]
		if !ok {
			t.Fatalf("user %d has no row", session)
		}
		muted = r.icons[iconMuted].Visible()
		tip = r.icons[iconMuted].TooltipText()
		desc = r.text.desc
	})
	return muted, tip, desc
}

// A user's state can change in the same update as their channel, e.g. the
// server suppresses a user it moves into a channel without Speak and lifts
// it again when they move on. The row, kept across the move, shows the
// new state.
func TestGTKMovedUserRowShowsNewState(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	// User 20 is in channel 1 (see connectedEvent).
	update := func(channel uint32, suppressed bool, changed bus.UserChanges) {
		b.Emit(bus.UserUpdated{
			User:    bus.UserInfo{Session: 20, Name: "user-20", Channel: channel, Suppressed: suppressed},
			Changed: changed,
		})
		time.Sleep(4 * rebuildInterval)
	}
	suppressedRow := func(when string) {
		t.Helper()
		muted, tip, desc := userRowState(t, tree, 20)
		if !muted || tip != "Not allowed to speak here" || desc != "not allowed to speak here" {
			t.Errorf("%s: muted icon %v, tooltip %q, description %q; want shown, %q, %q", when,
				muted, tip, desc, "Not allowed to speak here", "not allowed to speak here")
		}
	}
	freeRow := func(when string) {
		t.Helper()
		if muted, tip, desc := userRowState(t, tree, 20); muted || tip != "" || desc != "" {
			t.Errorf("%s: muted icon %v, tooltip %q, description %q; want none", when, muted, tip, desc)
		}
	}

	update(1, true, bus.UserChangeAudio)
	suppressedRow("suppressed in their channel")
	update(2, false, bus.UserChangeChannel|bus.UserChangeAudio)
	freeRow("moved into a channel where they may speak")
	update(2, false, bus.UserChangeAudio) // a fresh start for the next step
	update(3, true, bus.UserChangeChannel|bus.UserChangeAudio)
	suppressedRow("moved into a channel without Speak")
	checkListMatchesModel(t, tree)
}

// A user renamed in the same update as a move shows the new name.
func TestGTKMovedUserRowShowsNewName(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	b.Emit(bus.UserUpdated{
		User:    bus.UserInfo{Session: 20, Name: "renamed", Channel: 2},
		Changed: bus.UserChangeChannel | bus.UserChangeName,
	})
	time.Sleep(4 * rebuildInterval)
	var name, label string
	onMain(func() {
		r := tree.userRows[20]
		name = r.name.Text()
		label = r.text.name
	})
	if name != "renamed" || label != "renamed" {
		t.Errorf("row shows %q (accessible name %q), want %q", name, label, "renamed")
	}
	checkListMatchesModel(t, tree)
}
