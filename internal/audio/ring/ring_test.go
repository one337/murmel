package ring

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
)

func frameBytes() int { return 960 } // 480 samples * s16, one malgo period

func makeFrame(v byte, size int) []byte {
	f := make([]byte, size)
	for i := range f {
		f[i] = v
	}
	return f
}

func TestRingFIFO(t *testing.T) {
	r := New(frameBytes(), 4)
	for i := 0; i < 3; i++ {
		if !r.Push(makeFrame(byte(i), frameBytes())) {
			t.Fatalf("push %d rejected on non-full ring", i)
		}
	}
	if r.Len() != 3 {
		t.Fatalf("Len = %d, want 3", r.Len())
	}
	var dst []byte = make([]byte, frameBytes())
	for i := 0; i < 3; i++ {
		if !r.Pop(dst) {
			t.Fatalf("pop %d failed", i)
		}
		if !bytes.Equal(dst, makeFrame(byte(i), frameBytes())) {
			t.Fatalf("frame %d corrupted: first=%d", i, dst[0])
		}
	}
	if r.Pop(dst) {
		t.Fatal("pop on empty ring succeeded")
	}
	if r.Len() != 0 || r.Drops() != 0 {
		t.Fatalf("Len=%d Drops=%d after drain, want 0 0", r.Len(), r.Drops())
	}
}

func TestRingWraparound(t *testing.T) {
	r := New(frameBytes(), 4)
	var dst []byte = make([]byte, frameBytes())
	// 10 push/pop cycles through a 4-slot ring: every slot survives many
	// generation wraps with data intact.
	for gen := 0; gen < 10; gen++ {
		if !r.Push(makeFrame(byte(gen), frameBytes())) {
			t.Fatalf("gen %d: push rejected", gen)
		}
		if !r.Pop(dst) {
			t.Fatalf("gen %d: pop failed", gen)
		}
		if !bytes.Equal(dst, makeFrame(byte(gen), frameBytes())) {
			t.Fatalf("gen %d: data corruption across wrap", gen)
		}
	}
	if r.Drops() != 0 {
		t.Fatalf("unexpected drops: %d", r.Drops())
	}
}

func TestRingOverflowDropsOldest(t *testing.T) {
	r := New(frameBytes(), 4)
	// Fill 4, then push 2 more without consuming: frames 0 and 1 must
	// be dropped, leaving 2,3,4,5.
	for i := 0; i < 6; i++ {
		if !r.Push(makeFrame(byte(i), frameBytes())) {
			t.Fatalf("push %d rejected; drop-oldest must always make room", i)
		}
	}
	if got := r.Len(); got != 4 {
		t.Fatalf("Len = %d, want 4", got)
	}
	if got := r.Drops(); got != 2 {
		t.Fatalf("Drops = %d, want 2", got)
	}
	var dst []byte = make([]byte, frameBytes())
	for i := 2; i < 6; i++ {
		if !r.Pop(dst) {
			t.Fatalf("pop %d failed", i)
		}
		if dst[0] != byte(i) {
			t.Fatalf("got frame %d, want %d (oldest must be dropped)", dst[0], i)
		}
	}
	if r.Pop(dst) {
		t.Fatal("ring not empty after draining surviving frames")
	}
}

func TestRingSizeMismatchRejected(t *testing.T) {
	r := New(frameBytes(), 2)
	if r.Push(make([]byte, frameBytes()-1)) {
		t.Fatal("short push accepted")
	}
	if r.Drops() != 1 {
		t.Fatalf("Drops = %d, want 1 (rejected frame counted)", r.Drops())
	}
	if r.Push(make([]byte, frameBytes()+1)) {
		t.Fatal("long push accepted")
	}
	if r.Pop(make([]byte, frameBytes()-1)) {
		t.Fatal("short pop accepted")
	}
}

func TestRingTryPushNeverDrops(t *testing.T) {
	r := New(frameBytes(), 2)
	for i := 0; i < 2; i++ {
		if !r.TryPush(makeFrame(byte(i), frameBytes())) {
			t.Fatalf("try-push %d rejected on non-full ring", i)
		}
	}
	if r.TryPush(makeFrame('x', frameBytes())) {
		t.Fatal("try-push on full ring accepted; must not steal un-read frames")
	}
	if got := r.Drops(); got != 0 {
		t.Fatalf("Drops = %d, want 0 (full-ring rejection is not a drop)", got)
	}
	if r.TryPush(make([]byte, frameBytes()-1)) {
		t.Fatal("short try-push accepted")
	}
	if got := r.Drops(); got != 1 {
		t.Fatalf("Drops = %d, want 1 (size mismatch counted)", got)
	}
	var dst []byte = make([]byte, frameBytes())
	if !r.Pop(dst) || dst[0] != 0 {
		t.Fatal("frame 0 lost despite lossless policy")
	}
	if !r.TryPush(makeFrame('y', frameBytes())) {
		t.Fatal("try-push rejected after consumer freed a slot")
	}
}

func TestRingNewValidatesArgs(t *testing.T) {
	for _, tc := range [][2]int{{0, 4}, {960, 0}, {-1, -1}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("New(%d, %d) did not panic", tc[0], tc[1])
				}
			}()
			New(tc[0], tc[1])
		}()
	}
}

func TestRingConcurrentSPSC(t *testing.T) {
	const (
		slots    = 8
		produced = 20000
	)
	r := New(frameBytes(), slots)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		src := make([]byte, frameBytes())
		for i := 0; i < produced; i++ {
			v := byte(i)
			for j := range src {
				src[j] = v
			}
			// Lossless transfer: TryPush never drops un-read frames, so
			// every retried frame eventually lands and the consumer's
			// 20000-frame demand is exactly satisfiable.
			for !r.TryPush(src) {
			}
		}
	}()
	go func() {
		defer wg.Done()
		dst := make([]byte, frameBytes())
		for popped := 0; popped < produced; popped++ {
			for !r.Pop(dst) {
			}
			if !bytes.Equal(dst, makeFrame(dst[0], frameBytes())) {
				t.Errorf("frame %d torn: not uniform %d", popped, dst[0])
				return
			}
		}
	}()
	wg.Wait()

	if r.Len() != 0 {
		t.Fatalf("Len = %d after both sides done, want 0", r.Len())
	}
	if r.Drops() != 0 {
		t.Fatalf("Drops = %d; producer retries must deliver every frame", r.Drops())
	}
}

func TestRingDropOldestUnderFlood(t *testing.T) {
	// Consumer slower than producer on purpose: pushes must never fail
	// while the ring has unread frames (drop-oldest absorbs overflow),
	// and every popped frame must be internally consistent.
	const (
		slots    = 4
		produced = 10000
	)
	r := New(frameBytes(), slots)
	var wg sync.WaitGroup
	var done atomic.Bool
	wg.Add(2)

	go func() {
		defer wg.Done()
		src := make([]byte, frameBytes())
		for i := 0; i < produced; i++ {
			v := byte(i)
			for j := range src {
				src[j] = v
			}
			r.Push(src)
		}
		done.Store(true)
	}()
	go func() {
		defer wg.Done()
		dst := make([]byte, frameBytes())
		for {
			if r.Pop(dst) {
				if !bytes.Equal(dst, makeFrame(dst[0], frameBytes())) {
					t.Errorf("torn frame: %x..%x", dst[:4], dst[len(dst)-4:])
					return
				}
				continue
			}
			if done.Load() && r.Len() == 0 {
				return
			}
		}
	}()
	wg.Wait()

	if got := r.Drops(); got >= produced {
		t.Fatalf("Drops = %d impossibly high (all frames lost)", got)
	}
}

func TestRingSequentialFloodDrops(t *testing.T) {
	// Single-threaded flood (consumer absent): every push succeeds, ring
	// always holds the NEWEST n frames.
	r := New(frameBytes(), 4)
	for i := 0; i < 100; i++ {
		if !r.Push(makeFrame(byte(i), frameBytes())) {
			t.Fatalf("push %d rejected without consumer", i)
		}
	}
	if got := r.Drops(); got != 96 {
		t.Fatalf("Drops = %d, want 96", got)
	}
	var dst []byte = make([]byte, frameBytes())
	for want := byte(96); want < 100; want++ {
		if !r.Pop(dst) || dst[0] != want {
			t.Fatalf("surviving frames must be newest; got %d want %d", dst[0], want)
		}
	}
}
