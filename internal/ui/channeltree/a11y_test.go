package channeltree

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
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
//
// The row holds its count only while it has the input focus, which needs
// an active window; under Xvfb without a window manager the window may
// lose it for a moment. A pass in which that happened proves nothing and
// is repeated (logged).
func TestGTKUnreadWaitsForTheFocusToLeave(t *testing.T) {
	needGTK(t)
	tree, b, win, entry := focusTree(t, 5, 40)
	id := rowID{user: true, id: 20}
	var lost atomic.Bool
	onMain(func() {
		c := gtk.NewEventControllerFocus()
		c.ConnectLeave(func() { lost.Store(true) })
		tree.rowFor(id).AddController(c)
		coreglib.InternObject(win).NotifyProperty("is-active", func() {
			if !win.IsActive() {
				lost.Store(true)
			}
		})
	})

	const passes = 3
	n := 0
	for pass := 1; ; pass++ {
		focusRow(t, tree, id)
		waitForFocus(t, tree, win, id)
		lost.Store(false)
		var before string
		onMain(func() { before = tree.userRows[20].text.desc })
		n = pass + 1
		channel := fmt.Sprintf("8 users, you are here, %d unread messages", n+1)
		b.Emit(bus.UnreadChanged{Session: 20, Count: n})
		b.Emit(bus.UnreadChanged{Session: 0, Count: n + 1})
		// Both events are handled once the row without the focus shows its count.
		waitFor(t, "the own channel's count", func() bool {
			return tree.channelRows[2].text.desc == channel
		})
		if lost.Load() {
			if pass == passes {
				t.Fatalf("the window or the row lost the focus in all %d passes", passes)
			}
			t.Logf("pass %d: the window or the row lost the focus meanwhile; repeated", pass)
			continue
		}
		onMain(func() {
			if got := tree.userRows[20].text.desc; got != before {
				t.Errorf("focused row described as %q while it has the focus, want %q as before", got, before)
			}
		})
		break
	}

	onMain(func() { entry.GrabFocus() })
	want := fmt.Sprintf("%d unread messages", n)
	waitFor(t, "the unread count after the focus left", func() bool {
		return tree.userRows[20].text.desc == want
	})
}

// waitForFocus waits until the window is active and the row has the input
// focus. Without a window manager the X focus goes to the window shown last,
// which may be another test program's on the same display; every 2 s without
// the focus the window is shown and the row focused again (logged).
func waitForFocus(t *testing.T, tree *Tree, win *gtk.Window, id rowID) {
	t.Helper()
	start := time.Now()
	again := start.Add(2 * time.Second)
	var active, focus bool
	for deadline := start.Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		onMain(func() { active, focus = win.IsActive(), tree.rowFor(id).HasFocus() })
		if active && focus {
			return
		}
		if time.Now().After(again) {
			t.Logf("no input focus after %v (window active %v, row focused %v); window shown again",
				time.Since(start).Round(time.Millisecond), active, focus)
			onMain(func() {
				win.Present()
				tree.rowFor(id).GrabFocus()
			})
			again = time.Now().Add(2 * time.Second)
		}
	}
	t.Fatalf("timed out waiting for the row to have the input focus (window active %v, row focused %v)", active, focus)
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
		if !gtk.TestAccessibleHasProperty(scale, gtk.AccessiblePropertyValueText) {
			t.Error("the volume slider has no value text")
		}
		pop.Popdown()
	})
}
