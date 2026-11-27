package channeltree

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// In right-to-left layouts Left opens a channel and Right closes it.
func TestGTKLeftRightFollowTextDirection(t *testing.T) {
	needGTK(t)
	tree, _, _ := nestedTree(t)
	onMain(func() { tree.list.SetDirection(gtk.TextDirRTL) })
	k := keys{tree}
	for _, step := range []struct {
		key  uint
		open bool
		what string
	}{
		{gdk.KEY_Right, false, "Right on the open channel 6"},
		{gdk.KEY_Left, true, "Left on the closed channel 6"},
	} {
		focusRow(t, tree, rowID{id: 6})
		var handled bool
		onMain(func() { handled = press(k, step.key, 0) })
		onMain(func() {
			if open := tree.model.channels[6].expanded; !handled || open != step.open {
				t.Errorf("%s: handled %v, channel 6 open %v, want %v", step.what, handled, open, step.open)
			}
		})
		checkListMatchesModel(t, tree)
	}
}
