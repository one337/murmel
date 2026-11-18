package channeltree

import (
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// Rows carry their name and state for assistive technologies; their
// content (avatar, icons, badge, count, chevron) is hidden from them.
func TestGTKRowsNameThemselvesAndHideTheirContent(t *testing.T) {
	needGTK(t)
	tree, _, _, _ := focusTree(t, 5, 40)
	onMain(func() {
		for _, c := range []struct {
			id   rowID
			name string
		}{{rowID{id: 3}, "Channel 3"}, {rowID{user: true, id: 20}, "user-20"}} {
			row := tree.rowFor(c.id)
			for _, p := range []gtk.AccessibleProperty{gtk.AccessiblePropertyLabel, gtk.AccessiblePropertyDescription} {
				if !gtk.TestAccessibleHasProperty(row, p) {
					t.Errorf("%+v: no accessible property %v", c.id, p)
				}
			}
			if got := gtk.BaseWidget(row.Child()).AccessibleRole(); got != gtk.AccessibleRoleNone {
				t.Errorf("%+v: content role %v, want none", c.id, got)
			}
		}
		// On the row, GTK would read the tooltip out as its description
		// where that is empty; on the content it is still shown.
		row := tree.rowFor(rowID{id: 3})
		if got := row.TooltipText(); got != "" {
			t.Errorf("channel row has the tooltip %q", got)
		}
		if got := gtk.BaseWidget(row.Child()).TooltipText(); got != "Double-click to join" {
			t.Errorf("channel row content has the tooltip %q", got)
		}
		if got := tree.channelRows[3].text.name; got != "Channel 3" {
			t.Errorf("channel row name %q", got)
		}
		if got := tree.userRows[20].text.name; got != "user-20" {
			t.Errorf("user row name %q", got)
		}
		if got := tree.channelRows[2].text.desc; got != "8 users, you are here" {
			t.Errorf("own channel described as %q", got)
		}
	})
}

// The chevron is for the mouse: no Tab stop (Left and Right open and
// close a channel from the keyboard).
func TestGTKChevronIsNoTabStop(t *testing.T) {
	needGTK(t)
	tree, _, _, _ := focusTree(t, 5, 40)
	onMain(func() {
		r := tree.channelRows[3]
		if r.chevron.CanFocus() || !r.chevron.CanTarget() {
			t.Errorf("chevron of a channel with users: can focus %v, can target %v; want false, true", r.chevron.CanFocus(), r.chevron.CanTarget())
		}
	})
}

// Speaking leaves the description alone (screen readers would read it
// again on every word); a state change updates it.
func TestGTKSpeakingKeepsTheDescription(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	focusRow(t, tree, rowID{user: true, id: 20})
	b.Emit(bus.VoiceActivity{Session: 20, Active: true})
	time.Sleep(2 * rebuildInterval)
	onMain(func() {
		if got := tree.userRows[20].text.desc; got != "" {
			t.Errorf("speaking user described as %q, want nothing", got)
		}
	})
	var u bus.UserInfo
	onMain(func() { u = tree.model.users[20] })
	u.Muted = true
	b.Emit(bus.UserUpdated{User: u, Changed: bus.UserChangeAudio})
	waitFor(t, "the muted state in the description", func() bool {
		return tree.userRows[20].text.desc == "muted on the server"
	})
}

// New messages do not change the focused row's description; it follows
// when the focus leaves. Rows without the focus follow at once.
func TestGTKUnreadWaitsForTheFocusToLeave(t *testing.T) {
	needGTK(t)
	tree, b, _, entry := focusTree(t, 5, 40)
	focusRow(t, tree, rowID{user: true, id: 20})
	b.Emit(bus.UnreadChanged{Session: 20, Count: 2})
	b.Emit(bus.UnreadChanged{Session: 0, Count: 3})
	time.Sleep(2 * rebuildInterval)
	onMain(func() {
		if got := tree.userRows[20].text.desc; got != "" {
			t.Errorf("focused row described as %q while it has the focus", got)
		}
		if got := tree.channelRows[2].text.desc; got != "8 users, you are here, 3 unread messages" {
			t.Errorf("own channel (no focus) described as %q", got)
		}
	})
	onMain(func() { entry.GrabFocus() })
	waitFor(t, "the unread count after the focus left", func() bool {
		return tree.userRows[20].text.desc == "2 unread messages"
	})
}

// The volume slider in the user menu is named for assistive technologies.
func TestGTKVolumeSliderIsLabelled(t *testing.T) {
	needGTK(t)
	tree, _, _, _, keys := keyTree(t)
	id, _ := focused(tree)
	onMain(func() { press(keys, gdk.KEY_Menu, 0) })
	pop := shownMenu(t, tree, id)
	onMain(func() {
		scale, ok := find[*gtk.Scale](pop)
		if !ok {
			t.Error("no volume slider in the menu")
			return
		}
		if !gtk.TestAccessibleHasProperty(scale, gtk.AccessiblePropertyLabel) {
			t.Error("the volume slider has no accessible name")
		}
		pop.Popdown()
	})
}
