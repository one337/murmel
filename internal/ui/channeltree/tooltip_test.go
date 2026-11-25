package channeltree

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
)

// Each status icon of a user row says which state it stands for, and
// follows the state; its look and the row's role for assistive
// technologies stay as they are.
func TestGTKStatusIconsNameTheirState(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	var base bus.UserInfo
	onMain(func() { base = tree.model.users[20] })
	for _, c := range []struct {
		name                         string
		set                          func(*bus.UserInfo)
		muted, deaf, prio, recording string
	}{
		{"self muted", func(u *bus.UserInfo) { u.SelfMuted = true }, "Muted themselves", "", "", ""},
		{"server muted", func(u *bus.UserInfo) { u.Muted = true }, "Muted on the server", "", "", ""},
		{"both muted", func(u *bus.UserInfo) { u.SelfMuted, u.Muted = true, true }, "Muted themselves, muted on the server", "", "", ""},
		{"suppressed", func(u *bus.UserInfo) { u.Suppressed = true }, "Not allowed to speak here", "", "", ""},
		{"self deafened", func(u *bus.UserInfo) { u.SelfDeafened, u.SelfMuted = true, true }, "Muted themselves", "Deafened themselves", "", ""},
		{"server deafened", func(u *bus.UserInfo) { u.Deafened, u.Muted = true, true }, "Muted on the server", "Deafened on the server", "", ""},
		{"priority, recording", func(u *bus.UserInfo) { u.PrioritySpeaker, u.Recording = true, true }, "", "", "Priority speaker", "Recording"},
		{"nothing", func(*bus.UserInfo) {}, "", "", "", ""},
	} {
		u := base
		c.set(&u)
		b.Emit(bus.UserUpdated{User: u, Changed: bus.UserChangeAudio | bus.UserChangePrioritySpeaker | bus.UserChangeRecording})
		tip := func(icon string) string { return tree.userRows[20].icons[icon].TooltipText() }
		waitFor(t, c.name+": the icons' tooltips", func() bool {
			return tip(iconMuted) == c.muted && tip(iconDeafened) == c.deaf &&
				tip(iconPrioritySpeaker) == c.prio && tip(iconRecording) == c.recording
		})
		onMain(func() {
			row := tree.userRows[20].root
			if got := row.TooltipText(); got != "" {
				t.Errorf("%s: user row has the tooltip %q", c.name, got)
			}
			if got := gtk.BaseWidget(row.Child()).AccessibleRole(); got != gtk.AccessibleRoleNone {
				t.Errorf("%s: user row content has the role %v, want none", c.name, got)
			}
		})
	}
}
