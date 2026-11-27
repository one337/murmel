package channeltree

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"

	"github.com/one337/murmel/internal/bus"
)

// Space on a channel does nothing (joining takes Enter); on a user it
// opens the conversation.
func TestGTKSpaceOnChannelDoesNotJoin(t *testing.T) {
	needGTK(t)
	tree, b, _, _, keys := keyTree(t)
	joins := record[bus.ChannelJoinRequested](t, b)
	targets := record[bus.ChatTargetChanged](t, b)
	focusRow(t, tree, rowID{id: 3})
	for _, key := range []uint{gdk.KEY_space, gdk.KEY_KP_Space} {
		var handled bool
		onMain(func() { handled = press(keys, key, 0) })
		if !handled {
			t.Errorf("Space (%d) on a channel was left to GTK, which joins", key)
		}
	}
	if got := joins(0); len(got) != 0 {
		t.Errorf("Space on channel 3 joined: %+v", got)
	}
	if got := targets(0); len(got) != 0 {
		t.Errorf("Space on channel 3 changed the chat target: %+v", got)
	}

	user := rowID{user: true, id: usersOf(tree, 2)[0]}
	focusRow(t, tree, user)
	var handled bool
	onMain(func() { handled = press(keys, gdk.KEY_space, 0) })
	if got := targets(1); !handled || len(got) != 1 || got[0].Session != user.id {
		t.Errorf("Space on user %d: handled %v, sent %+v", user.id, handled, got)
	}
}
