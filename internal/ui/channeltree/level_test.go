package channeltree

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Assistive technologies know the list as a tree and every row as one of
// its items with a level (screen readers read levels only on tree items).
func TestGTKTreeRowsTellTheirLevel(t *testing.T) {
	needGTK(t)
	tree, _, _, _ := focusTree(t, 5, 40)
	onMain(func() {
		if got := tree.list.AccessibleRole(); got != gtk.AccessibleRoleTree {
			t.Errorf("list role %v, want tree", got)
		}
		rows := 0
		for child := tree.list.FirstChild(); child != nil; child = gtk.BaseWidget(child).NextSibling() {
			row, ok := child.(*gtk.ListBoxRow)
			if !ok {
				continue
			}
			rows++
			id, _ := tree.rowIDOf(row)
			if got := row.AccessibleRole(); got != gtk.AccessibleRoleTreeItem {
				t.Errorf("%+v: role %v, want tree item", id, got)
			}
			if !gtk.TestAccessibleHasProperty(row, gtk.AccessiblePropertyLevel) {
				t.Errorf("%+v: no level", id)
			}
		}
		if want := len(tree.channelRows) + len(tree.userRows); rows != want {
			t.Errorf("%d rows checked, want %d", rows, want)
		}
	})
}
