package admin

import (
	"slices"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// pickerList is the list of users inside the picker's popover.
func pickerList(btn *gtk.MenuButton) *gtk.ListBox {
	var walk func(w gtk.Widgetter) *gtk.ListBox
	walk = func(w gtk.Widgetter) *gtk.ListBox {
		if l, ok := w.(*gtk.ListBox); ok {
			return l
		}
		for c := gtk.BaseWidget(w).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
			if l := walk(c); l != nil {
				return l
			}
		}
		return nil
	}
	return walk(btn.Popover().Child())
}

// Users whose names differ only in case, or are the same, are listed in
// the same order every time the picker opens: by name ignoring case, then
// by name, then by user ID.
func TestGTKUserPickerOrderIsFixed(t *testing.T) {
	needGTK(t)
	win := testWindow(t)
	users := map[uint32]string{9: "anna", 4: "Anna", 7: "ANNA", 3: "bob", 2: "bob", 5: "Carl", 8: "Bob"}
	want := []uint32{7, 4, 9, 8, 2, 3, 5}

	var btn *gtk.MenuButton
	var picked []uint32
	onMain(func() {
		btn = userPicker("list-add-symbolic", "Add", func() map[uint32]string { return users }, nil,
			func(id uint32, _ string) { picked = append(picked, id) })
		win.SetContent(btn)
	})
	for cycle := range 20 {
		picked = nil
		for i := range want {
			onMain(func() {
				btn.Popup()
				if row := pickerList(btn).RowAtIndex(i); row != nil {
					row.Activate()
				}
			})
		}
		if !slices.Equal(picked, want) {
			t.Fatalf("cycle %d: rows pick users %v, want %v", cycle, picked, want)
		}
	}
}
