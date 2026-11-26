package gfileio

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// WindowOf finds the toplevel window of w, the parent for a file dialog
// (nil when w is not shown).
func WindowOf(w gtk.Widgetter) *gtk.Window {
	if w == nil {
		return nil
	}
	root := gtk.BaseWidget(w).Root()
	if root == nil {
		return nil
	}
	switch win := coreglib.BaseObject(root).Cast().(type) {
	case *adw.ApplicationWindow:
		return &win.ApplicationWindow.Window
	case *adw.Window:
		return &win.Window
	case *gtk.ApplicationWindow:
		return &win.Window
	case *gtk.Window:
		return win
	}
	return nil
}
