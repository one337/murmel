package ui

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
)

// A password problem stays 10 seconds, or until dismissed while a dialog
// covers the window.
func TestGTKPasswordProblemToastTimeout(t *testing.T) {
	needGTK(t)
	onMain(func() {
		if got := passwordProblemToast("x", false).Timeout(); got != 10 {
			t.Errorf("without a dialog: timeout %d", got)
		}
		if got := passwordProblemToast("x", true).Timeout(); got != 0 {
			t.Errorf("with a dialog: timeout %d", got)
		}
	})
}

// The window shows a reported password problem as a toast.
func TestGTKPasswordProblemIsShown(t *testing.T) {
	needGTK(t)
	app, b := testApp(t)
	const text = "Password not saved: test"
	b.Emit(bus.PasswordProblem{Text: text})
	waitMain(t, "the toast", func() bool { return hasLabel(app.window.toasts, text) })
}

// hasLabel reports whether a label below w shows text.
func hasLabel(w gtk.Widgetter, text string) bool {
	if l, ok := w.(*gtk.Label); ok && l.Label() == text {
		return true
	}
	for child := gtk.BaseWidget(w).FirstChild(); child != nil; child = gtk.BaseWidget(child).NextSibling() {
		if hasLabel(child, text) {
			return true
		}
	}
	return false
}
