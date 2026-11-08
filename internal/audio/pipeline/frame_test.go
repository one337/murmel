package pipeline

import (
	"context"
	"testing"
	"time"
)

func TestFramePoolRecycles(t *testing.T) {
	pool := newFramePool(4)
	f1 := pool.Get()
	f1.Data[0] = 42
	pool.Put(f1)
	f2 := pool.Get()
	if f2 != f1 {
		t.Fatalf("pool did not recycle: got %p want %p", f2, f1)
	}
	if f2.Data[0] != 42 {
		t.Fatalf("recycled frame data not preserved")
	}
}

func TestQueueRelayDropOldest(t *testing.T) {
	pool := newFramePool(16)
	q := newQueueRelay(2, pool)
	a, b, c := pool.Get(), pool.Get(), pool.Get()
	a.Seq, b.Seq, c.Seq = 1, 2, 3

	q.Push(a)
	q.Push(b)
	q.Push(c)

	if q.Len() != 2 {
		t.Fatalf("queue len = %d, want 2", q.Len())
	}
	if q.Drops() != 1 {
		t.Fatalf("drops = %d, want 1", q.Drops())
	}
	first, ok := q.Pull()
	if !ok || first.Seq != 2 {
		t.Fatalf("oldest frame not dropped: first seq = %d", first.Seq)
	}
	second, ok := q.Pull()
	if !ok || second.Seq != 3 {
		t.Fatalf("newest frame lost: second seq = %d", second.Seq)
	}
	if _, ok := q.Pull(); ok {
		t.Fatal("queue should be empty")
	}
}

func TestQueueRelayNeverBlocksWhenFull(t *testing.T) {
	pool := newFramePool(64)
	q := newQueueRelay(2, pool)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			q.Push(pool.Get())
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Push blocked on full queue")
	}
	if q.Drops() == 0 {
		t.Fatal("expected drops under sustained overflow")
	}
}

func TestQueueRelayWaitReadySignalling(t *testing.T) {
	pool := newFramePool(4)
	q := newQueueRelay(2, pool)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan bool, 1)
	go func() { got <- q.WaitReady(ctx) }()

	select {
	case <-got:
		t.Fatal("WaitReady returned before push")
	case <-time.After(50 * time.Millisecond):
	}
	q.Push(pool.Get())
	if v := <-got; !v {
		t.Fatal("WaitReady did not wake on push")
	}
	if _, ok := q.Pull(); !ok {
		t.Fatal("signalled frame missing")
	}

	q.Close()
	if q.WaitReady(ctx) {
		t.Fatal("WaitReady on closed empty queue should return false")
	}
	q.Push(pool.Get()) // push after close must not panic
	if !q.Closed() {
		t.Fatal("Closed flag lost")
	}
}

func TestFrameConstants(t *testing.T) {
	if FrameSize != 480 || SampleRate != 48000 {
		t.Fatalf("frame format drifted: %d samples @ %d Hz", FrameSize, SampleRate)
	}
	if FrameDuration != 10*time.Millisecond {
		t.Fatalf("frame duration = %v", FrameDuration)
	}
}
