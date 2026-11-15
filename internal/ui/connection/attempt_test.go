package connection

import (
	"context"
	"testing"
	"time"

	"io.github/murmel/internal/bus"
)

func newAttemptController(t *testing.T) *Controller {
	t.Helper()
	b := bus.New(nil)
	t.Cleanup(b.Close)
	return &Controller{appBus: b, appCtx: context.Background()}
}

// recordStarts collects the order of Disconnected and ConnectionStarting
// on the app bus; done receives it once ConnectionStarting arrived.
func recordStarts(t *testing.T, b *bus.Bus) (done chan []string) {
	t.Helper()
	done = make(chan []string, 1)
	var seen []string
	t.Cleanup(bus.Subscribe(b, func(bus.Disconnected) { seen = append(seen, "disconnected") }))
	t.Cleanup(bus.Subscribe(b, func(bus.ConnectionStarting) {
		seen = append(seen, "starting")
		done <- seen
	}))
	return done
}

func waitSeen(t *testing.T, done chan []string) []string {
	t.Helper()
	select {
	case seen := <-done:
		return seen
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ConnectionStarting")
		return nil
	}
}

func TestSwitchAttemptClearsShownConnectionFirst(t *testing.T) {
	withMainLoop(t, func() {
		c := newAttemptController(t)
		c.mu.Lock()
		old, _ := c.beginAttemptLocked()
		c.mu.Unlock()
		c.passFor(old)(bus.Connected{}) // the UI shows the old server

		done := recordStarts(t, c.appBus)
		next := c.switchAttempt(&dialParams{addr: "b.example:64738"})

		seen := waitSeen(t, done)
		if len(seen) != 2 || seen[0] != "disconnected" || seen[1] != "starting" {
			t.Fatalf("events = %v, want [disconnected starting]", seen)
		}
		if c.current(old) || !c.current(next) {
			t.Fatal("the old attempt is still current")
		}
		select {
		case <-old.ctx.Done():
		default:
			t.Fatal("the old attempt's context is not cancelled (its prompts stay open)")
		}
	})
}

func TestSwitchAttemptWithoutShownConnection(t *testing.T) {
	withMainLoop(t, func() {
		c := newAttemptController(t)
		c.mu.Lock()
		old, _ := c.beginAttemptLocked() // still dialing: nothing shown
		c.mu.Unlock()
		pass := c.passFor(old)
		pass(bus.Connected{})
		pass(bus.Disconnected{}) // dropped again: the UI already knows

		done := recordStarts(t, c.appBus)
		c.switchAttempt(&dialParams{})
		if seen := waitSeen(t, done); len(seen) != 1 || seen[0] != "starting" {
			t.Fatalf("events = %v, want [starting]", seen)
		}
	})
}

func TestSupersededAttemptForwardsNothing(t *testing.T) {
	withMainLoop(t, func() {
		c := newAttemptController(t)
		c.mu.Lock()
		old, _ := c.beginAttemptLocked()
		c.mu.Unlock()
		src := bus.New(nil)
		t.Cleanup(src.Close)
		stop := forwardBus(src, c.appBus, c.passFor(old), nil)
		t.Cleanup(stop)

		got := make(chan string, 4)
		t.Cleanup(bus.Subscribe(c.appBus, func(ev bus.UserAdded) { got <- ev.User.Name }))
		src.Emit(bus.UserAdded{User: bus.UserInfo{Session: 1, Name: "before"}})
		select {
		case name := <-got:
			if name != "before" {
				t.Fatalf("forwarded %q", name)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the current attempt's event was not forwarded")
		}

		c.switchAttempt(&dialParams{})
		src.Emit(bus.Connected{})
		src.Emit(bus.UserAdded{User: bus.UserInfo{Session: 2, Name: "late"}})
		select {
		case name := <-got:
			t.Fatalf("superseded attempt forwarded %q", name)
		case <-time.After(300 * time.Millisecond):
		}
		if c.shownGen != 0 {
			t.Fatal("a superseded Connected marked its connection as shown")
		}
	})
}

func TestDisconnectSupersedesRunningDial(t *testing.T) {
	withMainLoop(t, func() {
		c := newAttemptController(t)
		c.mu.Lock()
		dialing, _ := c.beginAttemptLocked()
		c.mu.Unlock()

		c.onDisconnectRequested(bus.DisconnectRequested{})
		if c.current(dialing) {
			t.Fatal("a dial still running stays current after leaving the server")
		}
		select {
		case <-dialing.ctx.Done():
		default:
			t.Fatal("its prompts are not closed")
		}
	})
}

func TestShownConnectionFollowsTheAttempt(t *testing.T) {
	withMainLoop(t, func() {
		c := newAttemptController(t)
		if _, _, _, ok := c.shownConnection(); ok {
			t.Fatal("a connection is shown before any dial")
		}
		a := c.switchAttempt(&dialParams{addr: "Voice.Example.org:64739", username: "simon"})
		if _, _, _, ok := c.shownConnection(); ok {
			t.Fatal("a connection is shown while still dialing")
		}
		c.passFor(a)(bus.Connected{})
		host, port, user, ok := c.shownConnection()
		if !ok || host != "Voice.Example.org" || port != 64739 || user != "simon" {
			t.Fatalf("shownConnection = %q %d %q %v", host, port, user, ok)
		}
		c.switchAttempt(&dialParams{addr: "other.example:64738", username: "simon"})
		if _, _, _, ok := c.shownConnection(); ok {
			t.Fatal("the replaced connection is still shown")
		}
	})
}
