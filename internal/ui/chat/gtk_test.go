package chat

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/protocol/gumble"
)

// The GTK tests need GTK and a display. tools/verify.sh runs them under
// xvfb-run with MURMEL_GTK_TESTS=1 in a throwaway HOME; without it they
// are skipped, so a plain `go test` never opens a display.

var gtkReady bool

// GTK must run on the main thread: TestMain keeps it there and runs the
// GLib main loop while the tests run on other goroutines.
func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	if os.Getenv("MURMEL_GTK_TESTS") == "" || !gtk.InitCheck() {
		os.Exit(m.Run())
	}
	adw.Init()
	gtkReady = true
	bus.SetMainLoop(func(f func() bool) { glib.IdleAdd(f) })
	loop := glib.NewMainLoop(nil, false)
	code := 0
	go func() {
		code = m.Run()
		glib.IdleAdd(func() bool { loop.Quit(); return false })
	}()
	loop.Run()
	os.Exit(code)
}

// needGTK skips a test without GTK and a display, except in CI, where a
// GTK test that cannot run is a failure.
func needGTK(t *testing.T) {
	t.Helper()
	if gtkReady {
		return
	}
	if os.Getenv("CI") != "" {
		t.Fatal("GTK test cannot run in CI: GTK did not start (tools/verify.sh runs it under xvfb-run with MURMEL_GTK_TESTS=1)")
	}
	t.Skip("needs GTK and a display: run with MURMEL_GTK_TESTS=1 under xvfb-run (tools/verify.sh)")
}

// onMain runs f on the GTK main loop and waits for it.
func onMain(f func()) {
	done := make(chan struct{})
	glib.IdleAdd(func() bool { f(); close(done); return false })
	<-done
}

// chatWindow shows a chat view connected to a server with a channel
// "Quiet" and a user "anna" besides oneself.
func chatWindow(t *testing.T) (*View, *bus.Bus) {
	v, b, _ := chatWindowWith(t)
	return v, b
}

// chatWindowWith is chatWindow that also returns the window.
func chatWindowWith(t *testing.T) (*View, *bus.Bus, *gtk.Window) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := bus.New(nil)
	var v *View
	var win *gtk.Window
	onMain(func() {
		v = NewView(ctx, b, nil)
		win = gtk.NewWindow()
		win.SetDefaultSize(600, 500)
		win.SetChild(v.Root())
		win.Present()
	})
	t.Cleanup(func() { onMain(func() { win.Destroy() }) })
	b.Emit(bus.Connected{SelfSession: 1,
		Channels: []bus.ChannelInfo{{ID: 0, Name: "Root"}, {ID: 4, Name: "Quiet"}},
		Users:    []bus.UserInfo{{Session: 1, Name: "me"}, {Session: 7, Name: "anna", Channel: 4}},
	})
	waitUntil(t, "the connection", func() bool { return v.bus.State().SelfSession == 1 })
	return v, b, win
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	ok := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		onMain(func() { ok = cond() })
		if ok {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// errorRows lists the texts of the error rows in the conversation shown.
func errorRows(v *View) []string {
	var out []string
	var texts func(w gtk.Widgetter) []string
	texts = func(w gtk.Widgetter) []string {
		var l []string
		if label, ok := coreglib.InternObject(w).Cast().(*gtk.Label); ok && label.Text() != "" {
			l = append(l, label.Text())
		}
		for c := gtk.BaseWidget(w).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
			l = append(l, texts(c)...)
		}
		return l
	}
	for c := v.visible().list.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		row, ok := coreglib.InternObject(c).Cast().(*gtk.ListBoxRow)
		if !ok || row.Child() == nil || !gtk.BaseWidget(row.Child()).HasCSSClass("error") {
			continue
		}
		if l := texts(row.Child()); len(l) > 0 {
			out = append(out, l[len(l)-1]) // the time stamp comes first
		}
	}
	return out
}

// Refusals from the server show as error rows in the conversation shown,
// one each; one's own Enter (the password prompt answers it) shows none.
func TestGTKRefusalsShowInTheChat(t *testing.T) {
	needGTK(t)
	v, b := chatWindow(t)
	b.Emit(bus.PermissionDenied{Reason: bus.DenyPermission, Session: 1, ChannelID: 4,
		Permission: int(gumble.PermissionMuteDeafen)})
	b.Emit(bus.PermissionDenied{Reason: bus.DenyPermission, Session: 1, ChannelID: 4,
		Permission: int(gumble.PermissionEnter)})
	b.Emit(bus.PermissionDenied{Reason: bus.DenyText, Text: "Not now"})
	b.Emit(bus.PermissionDenied{Reason: bus.DenyChannelFull})
	want := []string{
		"You do not have the “Mute/Deafen” permission in Quiet.",
		"Refused by the server: Not now",
		"The channel is full.",
	}
	var got []string
	waitUntil(t, "the error rows", func() bool { got = errorRows(v); return len(got) >= len(want) })
	time.Sleep(100 * time.Millisecond)
	onMain(func() { got = errorRows(v) })
	if len(got) != len(want) {
		t.Fatalf("error rows %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("error row %d: %q, want %q", i, got[i], want[i])
		}
	}
}
