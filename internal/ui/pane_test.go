package ui

import (
	"slices"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
)

// paneWindow fills the window's slots with stand-ins: a list entry and a
// status button on the sidebar, a message field in the chat.
type paneWindow struct {
	app                *App
	list, status, chat *gtk.Button
}

func newPaneWindow(t *testing.T) (*paneWindow, *bus.Bus) {
	t.Helper()
	app, b := testApp(t)
	p := &paneWindow{app: app}
	onMain(func() {
		p.list = gtk.NewButtonWithLabel("list")
		p.status = gtk.NewButtonWithLabel("status")
		side := gtk.NewBox(gtk.OrientationVertical, 0)
		side.Append(p.list)
		side.Append(p.status)
		p.chat = gtk.NewButtonWithLabel("message field")
		w := app.window
		w.Sidebar().Set(side)
		w.Sidebar().SetFocus(p.list.GrabFocus)
		w.Chat().Set(p.chat)
		w.Chat().SetFocus(p.chat.GrabFocus)
		w.Present()
	})
	return p, b
}

// switchPane presses F6 (the window action) and names the focused stand-in.
func (p *paneWindow) switchPane() string {
	var name string
	onMain(func() {
		gtk.BaseWidget(p.app.window).ActivateAction("win.switch-pane", nil)
		name = p.focused()
	})
	return name
}

func (p *paneWindow) focused() string {
	f := p.app.window.Focus()
	for _, b := range []*gtk.Button{p.list, p.status, p.chat} {
		if f != nil && sameWidget(f, b) {
			return b.Label()
		}
	}
	return "elsewhere"
}

func connect(t *testing.T, p *paneWindow, b *bus.Bus) {
	t.Helper()
	b.Emit(bus.Connected{})
	waitMain(t, "the window to see the connection", func() bool { return p.app.window.connected })
}

// F6 and Shift+F6 move between the channel list and the message field;
// from anywhere outside the chat, e.g. the status area, F6 goes to the
// message field.
func TestGTKF6SwitchesPanes(t *testing.T) {
	needGTK(t)
	p, b := newPaneWindow(t)
	onMain(func() {
		accels := p.app.AccelsForAction("win.switch-pane")
		if !slices.Contains(accels, "F6") || !slices.Contains(accels, "<Shift>F6") {
			t.Errorf("switch-pane keys %v, want F6 and Shift+F6", accels)
		}
	})
	connect(t, p, b)
	onMain(func() { p.status.GrabFocus() })
	for i, want := range []string{"message field", "list", "message field", "list"} {
		if got := p.switchPane(); got != want {
			t.Errorf("F6 #%d: focus on %s, want %s", i+1, got, want)
		}
	}
}

// Without a connection or with a dialog open, F6 leaves the focus alone,
// and a narrow window keeps its page behind the dialog.
func TestGTKF6NeedsConnectionAndNoDialog(t *testing.T) {
	needGTK(t)
	p, b := newPaneWindow(t)
	onMain(func() { p.status.GrabFocus() })
	if got := p.switchPane(); got != "status" {
		t.Errorf("F6 while not connected: focus on %s, want status", got)
	}
	connect(t, p, b)
	var before gtk.Widgetter
	onMain(func() {
		p.app.window.setCollapsed(true)
		p.app.window.split.SetShowContent(false)
		openDialog(p.app)
	})
	waitMain(t, "the focus in the dialog", func() bool {
		before = p.app.window.Focus()
		return before != nil && p.focused() == "elsewhere"
	})
	p.switchPane()
	onMain(func() {
		if after := p.app.window.Focus(); after == nil || before == nil || !sameWidget(after, before) {
			t.Error("F6 with a dialog open moved the focus")
		}
		if p.app.window.split.ShowContent() {
			t.Error("F6 with a dialog open showed the chat page behind it")
		}
	})
}

// In a narrow window F6 shows the page it moves to.
func TestGTKF6InANarrowWindow(t *testing.T) {
	needGTK(t)
	p, b := newPaneWindow(t)
	connect(t, p, b)
	onMain(func() {
		p.app.window.setCollapsed(true)
		p.app.window.split.SetShowContent(false)
		p.list.GrabFocus()
	})
	for i, c := range []struct {
		focus string
		chat  bool
	}{{"message field", true}, {"list", false}} {
		got := p.switchPane()
		var shown bool
		onMain(func() { shown = p.app.window.split.ShowContent() })
		if got != c.focus || shown != c.chat {
			t.Errorf("F6 #%d: focus on %s, chat page shown %v; want %s, %v", i+1, got, shown, c.focus, c.chat)
		}
	}
}
