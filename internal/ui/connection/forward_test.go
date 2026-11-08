package connection

import (
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"io.github/murmel/internal/bus"
)

// withMainLoop runs f with a GLib main loop spinning on a separate
// goroutine so idle-dispatched bus handlers actually execute.
func withMainLoop(t *testing.T, f func()) {
	t.Helper()
	loop := glib.NewMainLoop(nil, false)
	go loop.Run()
	t.Cleanup(loop.Quit)
	f()
}

func waitFor[T any](t *testing.T, got chan T) {
	t.Helper()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded event")
	}
}

func TestForwardBusDeliversEvents(t *testing.T) {
	withMainLoop(t, func() {
		src, dst := bus.New(nil), bus.New(nil)
		t.Cleanup(dst.Close)
		t.Cleanup(src.Close)

		got := make(chan struct{})
		unsub := bus.Subscribe(dst, func(ev bus.UserAdded) {
			got <- struct{}{}
		})
		t.Cleanup(unsub)

		stop := forwardBus(src, dst, nil)
		t.Cleanup(stop)

		src.Emit(bus.UserAdded{User: bus.UserInfo{Session: 7, Name: "peer"}})
		waitFor(t, got)
	})
}

func TestForwardBusOrder(t *testing.T) {
	withMainLoop(t, func() {
		src, dst := bus.New(nil), bus.New(nil)
		t.Cleanup(dst.Close)
		t.Cleanup(src.Close)

		got := make(chan []string, 1)
		var seen []string
		unsub := bus.Subscribe(dst, func(ev bus.Connected) {
			seen = append(seen, "connected")
		})
		t.Cleanup(unsub)
		unsub2 := bus.Subscribe(dst, func(ev bus.UserAdded) {
			seen = append(seen, "user")
		})
		t.Cleanup(unsub2)
		unsub3 := bus.Subscribe(dst, func(ev bus.Disconnected) {
			seen = append(seen, "disconnected")
			got <- seen
		})
		t.Cleanup(unsub3)

		stop := forwardBus(src, dst, nil)
		t.Cleanup(stop)

		src.Emit(bus.Connected{})
		src.Emit(bus.UserAdded{})
		src.Emit(bus.Disconnected{})

		waitFor(t, got)
		want := []string{"connected", "user", "disconnected"}
		if len(seen) != len(want) {
			t.Fatalf("seen = %v, want %v", seen, want)
		}
		for i := range want {
			if seen[i] != want[i] {
				t.Fatalf("seen = %v, want %v", seen, want)
			}
		}
	})
}

func TestForwardBusDisconnectedCallback(t *testing.T) {
	withMainLoop(t, func() {
		src, dst := bus.New(nil), bus.New(nil)
		t.Cleanup(dst.Close)
		t.Cleanup(src.Close)

		got := make(chan struct{}, 1)
		stopDone := make(chan struct{})
		var stopFwd func()
		stopFwd = forwardBus(src, dst, func() {
			got <- struct{}{}
			// stopFwd must not be called synchronously from within the handler:
			// the handler holds the subscription mutex, and stopFwd → kill()
			// also needs that mutex → deadlock. Dispatch to a goroutine so
			// kill() blocks correctly until the handler's run() frame exits.
			go func() {
				stopFwd()
				close(stopDone)
			}()
		})
		t.Cleanup(func() { stopFwd() })

		src.Emit(bus.Disconnected{})
		waitFor(t, got)
		waitFor(t, stopDone) // wait for stopFwd to complete before testing absence

		// After the callback unsubscribed, no further forwarding happens.
		delivered := make(chan struct{})
		unsub := bus.Subscribe(dst, func(ev bus.UserAdded) { delivered <- struct{}{} })
		t.Cleanup(unsub)
		src.Emit(bus.UserAdded{})
		select {
		case <-delivered:
			t.Fatal("event forwarded after stop")
		case <-time.After(200 * time.Millisecond):
		}
	})
}

func TestForwardBusStopDropsSubsequent(t *testing.T) {
	withMainLoop(t, func() {
		src, dst := bus.New(nil), bus.New(nil)
		t.Cleanup(dst.Close)
		t.Cleanup(src.Close)

		stop := forwardBus(src, dst, nil)
		stop()

		delivered := make(chan struct{})
		unsub := bus.Subscribe(dst, func(ev bus.TextMessage) { delivered <- struct{}{} })
		t.Cleanup(unsub)
		src.Emit(bus.TextMessage{Message: "late"})
		select {
		case <-delivered:
			t.Fatal("event forwarded after stop")
		case <-time.After(200 * time.Millisecond):
		}
	})
}
