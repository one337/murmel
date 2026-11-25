package connection

import (
	"context"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/ui/weakref"
)

// attempt is one connection attempt: a dial (the first one or a reconnect)
// together with the prompts it shows. Connecting anew or leaving the server
// supersedes it: its ctx ends, which closes its open prompts, and whatever
// it still reports (events, success, errors) is dropped.
type attempt struct {
	gen uint64
	ctx context.Context
}

// current reports whether a is still the running attempt. Safe from any
// goroutine.
func (c *Controller) current(a attempt) bool { return c.gen.Load() == a.gen }

// beginAttemptLocked supersedes the running attempt and starts a new one.
// shown reports whether the user interface shows the superseded attempt's
// connection and so still has to be told Disconnected. c.mu must be held;
// main loop only.
func (c *Controller) beginAttemptLocked() (a attempt, shown bool) {
	shown = c.endAttemptLocked()
	ctx, cancel := context.WithCancel(c.appCtx)
	c.attemptCancel = cancel
	return attempt{gen: c.gen.Load(), ctx: ctx}, shown
}

// endAttemptLocked supersedes the running attempt without starting another
// and reports whether the user interface shows its connection (its
// Connected was forwarded, its Disconnected not). c.mu must be held; main
// loop only.
func (c *Controller) endAttemptLocked() (shown bool) {
	shown = c.shownGen != 0 && c.shownGen == c.gen.Load()
	c.shownGen = 0
	if c.attemptCancel != nil {
		c.attemptCancel()
		c.attemptCancel = nil
	}
	c.gen.Add(1)
	return shown
}

// passFor lets only the events of attempt a through to the app bus and
// notes whether the user interface shows its connection. It runs on the
// main loop (forwardBus).
func (c *Controller) passFor(a attempt) func(bus.Event) bool {
	return func(ev bus.Event) bool {
		if !c.current(a) {
			return false
		}
		switch ev.(type) {
		case bus.Connected:
			c.shownGen = a.gen
		case bus.Disconnected:
			c.shownGen = 0
		}
		return true
	}
}

// closeWhenSuperseded closes d, a prompt of attempt a, once a is
// superseded. Main loop only.
func closeWhenSuperseded(a attempt, d *adw.Dialog) {
	closed := false
	self := weakref.New(d)
	stop := context.AfterFunc(a.ctx, func() {
		glib.IdleAdd(func() {
			if !closed {
				self.Do(func(d *adw.Dialog) { d.ForceClose() })
			}
		})
	})
	d.ConnectClosed(func() {
		closed = true
		stop()
	})
}
