package chat

import (
	"fmt"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
)

// settleFrames gives GTK time for a few frames (layout and the jump to
// the bottom on the next frame).
func settleFrames() { time.Sleep(150 * time.Millisecond); onMain(func() {}) }

// newestHidden is how far the newest row of c reaches below (positive) or
// starts above (negative) the visible part of the scrollback; 0 when it is
// fully shown.
func newestHidden(c *conversation) (px float32) {
	onMain(func() {
		row := c.hist.items[len(c.hist.items)-1].row
		b, ok := row.ComputeBounds(c.scroll)
		if !ok {
			px = 1e6
			return
		}
		if below := b.Y() + b.Height() - float32(gtk.BaseWidget(c.scroll).Height()); below > 0.5 {
			px = below
		} else if b.Y() < -0.5 {
			px = b.Y()
		}
	})
	return px
}

func logRow(i int) rowStyle {
	return rowStyle{kind: rowLog, at: time.Now(), content: fmt.Sprintf("user-%d joined the channel", i)}
}

// fill appends rows to c until it scrolls.
func fill(c *conversation) {
	for i := range 40 {
		onMain(func() { c.append(logRow(i)) })
	}
	settleFrames()
}

// While the view is at the bottom, every new row is fully shown, whatever
// its kind and however many come at once.
func TestGTKNewestRowIsShown(t *testing.T) {
	needGTK(t)
	v, _ := chatWindow(t)
	c := v.channel
	fill(c)
	steps := []struct {
		name string
		add  func(i int)
	}{
		{"a log line", func(i int) { c.append(logRow(i)) }},
		{"two log lines at once", func(i int) { c.append(logRow(i)); c.append(logRow(i + 1)) }},
		{"a message", func(int) {
			c.append(rowStyle{kind: rowMessage, sender: "anna", at: time.Now(), content: "hello there"})
		}},
		{"an error", func(int) {
			c.append(rowStyle{kind: rowError, at: time.Now(), content: "The channel is full."})
		}},
		{"a message and an error", func(int) {
			c.append(rowStyle{kind: rowMessage, sender: "me", at: time.Now(), content: "hi"})
			c.append(rowStyle{kind: rowError, at: time.Now(), content: "Refused by the server."})
		}},
	}
	for i, st := range steps {
		for n := range 3 {
			onMain(func() { st.add(100*i + n) })
			settleFrames()
			if px := newestHidden(c); px != 0 {
				t.Errorf("%s (%d): the newest row is %v px out of view", st.name, n+1, px)
			}
		}
	}
}

// A taller composer (several lines typed) makes the scrollback lower; the
// newest row stays in view.
func TestGTKNewestRowStaysShownWhenTheViewShrinks(t *testing.T) {
	needGTK(t)
	v, _ := chatWindow(t)
	c := v.channel
	fill(c)
	onMain(func() { c.composer.view.Buffer().SetText("one\ntwo\nthree\nfour\nfive") })
	settleFrames()
	if px := newestHidden(c); px != 0 {
		t.Errorf("after the composer grew, the newest row is %v px out of view", px)
	}
}

// Scrolled up, new rows leave the view where it is and offer
// the jump to the latest, which then shows the newest row.
func TestGTKScrolledUpStaysAndJumpShowsNewest(t *testing.T) {
	needGTK(t)
	v, _ := chatWindow(t)
	c := v.channel
	fill(c)
	onMain(func() { c.scroll.VAdjustment().SetValue(0) })
	settleFrames()
	onMain(func() { c.append(logRow(500)) })
	settleFrames()
	var value float64
	var revealed bool
	onMain(func() { value, revealed = c.scroll.VAdjustment().Value(), c.jump.RevealChild() })
	if value != 0 || !revealed {
		t.Errorf("scrolled up: view moved to %v, jump offered %v; want 0 and offered", value, revealed)
	}
	onMain(func() { coreglib.InternObject(c.jump.Child()).Emit("clicked") })
	settleFrames()
	if px := newestHidden(c); px != 0 {
		t.Errorf("after the jump, the newest row is %v px out of view", px)
	}
}

// Private conversations scroll the same way.
func TestGTKNewestRowIsShownInPrivateChat(t *testing.T) {
	needGTK(t)
	v, _ := chatWindow(t)
	var c *conversation
	onMain(func() {
		v.openDM(convKey("u:7"), "anna")
		c = v.dms[convKey("u:7")]
	})
	settleFrames()
	fill(c)
	for i := range 3 {
		onMain(func() { c.append(logRow(900 + i)) })
		settleFrames()
		if px := newestHidden(c); px != 0 {
			t.Errorf("private chat, row %d: the newest row is %v px out of view", i+1, px)
		}
	}
}

// Rows that arrive while the window is not shown are in view once it is
// shown again; nothing has to run in the meantime.
func TestGTKRowsWhileHiddenAreShownLater(t *testing.T) {
	needGTK(t)
	v, _, win := chatWindowWith(t)
	c := v.channel
	fill(c)
	onMain(func() { win.SetVisible(false) })
	settleFrames()
	for i := range 5 {
		onMain(func() { c.append(logRow(700 + i)) })
	}
	settleFrames()
	onMain(func() { win.Present() })
	settleFrames()
	if px := newestHidden(c); px != 0 {
		t.Errorf("after showing the window again, the newest row is %v px out of view", px)
	}
}

// Scrolled up, every new line offers the jump to the latest: a log line,
// an error, a server message and a message from someone else. One's own
// message instead takes the view to the bottom and keeps it there.
func TestGTKScrolledUpEveryLineOffersJump(t *testing.T) {
	needGTK(t)
	v, b, _, _ := lockWindow(t, 600, perms(textAllowed), perms(textAllowed))
	c := v.channel
	for i := range 40 {
		b.Emit(bus.TextMessage{Sender: 7, ChannelIDs: []uint32{4}, Message: fmt.Sprintf("line %d", i)})
	}
	waitUntil(t, "the messages", func() bool { return len(c.hist.items) >= 40 })
	settleFrames()
	scrolledUp := func() {
		onMain(func() {
			c.scroll.VAdjustment().SetValue(0)
			c.jump.SetRevealChild(false)
		})
		settleFrames()
	}
	for _, step := range []struct {
		name string
		ev   bus.Event
	}{
		{"a log line", bus.UserUpdated{User: bus.UserInfo{Session: 7, Name: "anna", Channel: 4},
			Changed: bus.UserChangeChannel}},
		{"an error", bus.PermissionDenied{Reason: bus.DenyChannelFull}},
		{"a server message", bus.TextMessage{Sender: 0, ChannelIDs: []uint32{4}, Message: "notice"}},
		{"a message from someone else", bus.TextMessage{Sender: 7, ChannelIDs: []uint32{4}, Message: "hi"}},
	} {
		scrolledUp()
		n := len(c.hist.items)
		b.Emit(step.ev)
		waitUntil(t, step.name, func() bool { return len(c.hist.items) > n })
		settleFrames()
		var value float64
		var revealed bool
		onMain(func() { value, revealed = c.scroll.VAdjustment().Value(), c.jump.RevealChild() })
		if value != 0 || !revealed {
			t.Errorf("%s: view moved to %v, jump offered %v; want 0 and offered", step.name, value, revealed)
		}
	}

	scrolledUp()
	onMain(func() { c.jump.SetRevealChild(true) })
	onMain(func() {
		c.composer.buffer.SetText("my own")
		c.composer.Submit()
	})
	settleFrames()
	var revealed bool
	onMain(func() { revealed = c.jump.RevealChild() })
	if px := newestHidden(c); px != 0 || revealed {
		t.Errorf("own message: newest row %v px out of view, jump offered %v; want shown, not offered", px, revealed)
	}
	b.Emit(bus.UserUpdated{User: bus.UserInfo{Session: 7, Name: "anna", Channel: 5}, Changed: bus.UserChangeChannel})
	settleFrames()
	if px := newestHidden(c); px != 0 {
		t.Errorf("after one's own message the view stays at the bottom: newest row %v px out of view", px)
	}
	var tip string
	onMain(func() { tip = gtk.BaseWidget(c.jump.Child()).TooltipText() })
	if tip != "Scroll to the newest line" {
		t.Errorf("jump tooltip %q, want %q", tip, "Scroll to the newest line")
	}
}
