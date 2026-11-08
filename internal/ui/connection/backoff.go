package connection

import "time"

// Reconnect schedule: exponential from 1s doubling to a 60s cap
// (1, 2, 4, 8, 16, 32, 60, 60, …), reset on a successful connection.
const (
	reconnectInitial = 1 * time.Second
	reconnectMax     = 60 * time.Second
	// maxShift caps the shift count; 1s << 30 already far exceeds the
	// 60s cap, so larger attempts saturate instead of overflowing.
	maxShift = 30
)

// backoff produces the auto-reconnect delay sequence. It is pure state;
// the controller owns the timers, the countdown banner, and cancellation.
type backoff struct {
	attempt int
}

// next returns the next delay and advances the sequence.
func (b *backoff) next() time.Duration {
	var d time.Duration
	if b.attempt >= maxShift {
		d = reconnectMax
	} else {
		d = reconnectInitial << uint(b.attempt)
	}
	if d > reconnectMax || d < reconnectInitial {
		d = reconnectMax
	}
	b.attempt++
	return d
}

// reset restarts the sequence at the initial delay (called when a
// connection attempt succeeds or a fresh manual connect begins).
func (b *backoff) reset() { b.attempt = 0 }
