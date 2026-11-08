package connection

import (
	"testing"
	"time"
)

func TestBackoffSequence(t *testing.T) {
	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		60 * time.Second, // 64 capped
		60 * time.Second,
	}
	var b backoff
	for i, w := range want {
		if got := b.next(); got != w {
			t.Fatalf("next #%d = %v, want %v", i, got, w)
		}
	}
}

func TestBackoffCapNeverExceeded(t *testing.T) {
	var b backoff
	for i := 0; i < 200; i++ {
		d := b.next()
		if d > reconnectMax || d < reconnectInitial {
			t.Fatalf("attempt %d produced out-of-range delay %v", i, d)
		}
	}
}

func TestBackoffReset(t *testing.T) {
	var b backoff
	b.next()
	b.next()
	b.next()
	b.reset()
	if got := b.next(); got != reconnectInitial {
		t.Fatalf("after reset next = %v, want %v", got, reconnectInitial)
	}
}
