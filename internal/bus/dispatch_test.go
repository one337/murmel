package bus

import (
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// These tests drive the real glib.IdleAdd path (New constructs the bus with
// glib.IdleAdd as the idleAdd hook). They iterate the default main context
// from the test goroutine, which is what makes idle sources — and therefore
// handlers — run on "the main loop" here.

const (
	floodGoroutines = 10
	floodPerGoroutine = 1000
	floodTotal        = floodGoroutines * floodPerGoroutine
	p99Limit          = time.Millisecond
)

func iterateMainContext(t *testing.T, until func() bool) {
	t.Helper()
	ctx := glib.MainContextDefault()
	deadline := time.After(30 * time.Second)
	for !until() {
		if ctx.Pending() {
			ctx.Iteration(false)
			continue
		}
		select {
		case <-deadline:
			t.Fatal("main-context dispatch deadline exceeded")
		case <-time.After(time.Millisecond):
		}
	}
}

// drainMainContext runs every source that is already due, then returns; used
// to prove that NO further handler calls happen after unsubscribe.
func drainMainContext(t *testing.T) {
	t.Helper()
	ctx := glib.MainContextDefault()
	deadline := time.After(30 * time.Second)
	for ctx.Pending() {
		select {
		case <-deadline:
			t.Fatal("main-context drain deadline exceeded")
		default:
		}
		ctx.Iteration(false)
	}
}

func TestDispatchFlood(t *testing.T) {
	b := New(nil)
	defer b.Close()

	var delivered atomic.Int64
	var onMainLoop atomic.Bool
	handlerOnMain := false
	unsub := Subscribe(b, func(UserUpdated) {
		delivered.Add(1)
		if !handlerOnMain {
			handlerOnMain = glib.MainContextDefault().IsOwner()
			onMainLoop.Store(handlerOnMain)
		}
	})
	defer unsub()

	var emitDurations []time.Duration
	var mu sync.Mutex
	var wg sync.WaitGroup
	for g := 0; g < floodGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]time.Duration, 0, floodPerGoroutine)
			for i := 0; i < floodPerGoroutine; i++ {
				start := time.Now()
				b.Emit(UserUpdated{User: UserInfo{Session: uint32(i)}})
				local = append(local, time.Since(start))
			}
			mu.Lock()
			emitDurations = append(emitDurations, local...)
			mu.Unlock()
		}()
	}

	emitted := make(chan struct{})
	go func() { wg.Wait(); close(emitted) }()
	iterateMainContext(t, func() bool {
		select {
		case <-emitted:
		default:
			return false
		}
		return delivered.Load()+int64(b.Dropped()) == floodTotal
	})

	if got := delivered.Load() + int64(b.Dropped()); got != floodTotal {
		t.Fatalf("delivered+dropped = %d, want %d (events vanished)", got, floodTotal)
	}
	if dropped := b.Dropped(); dropped > 0 {
		t.Logf("backpressure engaged: %d dropped, %d delivered (accounting holds)", dropped, delivered.Load())
	}

	if len(emitDurations) != floodTotal {
		t.Fatalf("recorded %d emit timings, want %d", len(emitDurations), floodTotal)
	}
	sorted := append([]time.Duration(nil), emitDurations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p99 := sorted[int(0.99*float64(len(sorted)-1))]
	if p99 >= p99Limit {
		t.Fatalf("p99 Emit send-block = %v, want < %v (bus must never stall the network loop)", p99, p99Limit)
	}
	t.Logf("p99 send-block = %v, max = %v, delivered = %d, dropped = %d", p99, sorted[len(sorted)-1], delivered.Load(), b.Dropped())

	if !onMainLoop.Load() {
		t.Fatal("handler did not run while the iterating thread owned the default main context — dispatch is not main-loop bound")
	}
}


func TestUnsubscribe(t *testing.T) {
	b := New(nil)
	defer b.Close()

	var calls atomic.Int64
	unsub := Subscribe(b, func(TextMessage) { calls.Add(1) })

	for i := 0; i < 5; i++ {
		b.Emit(TextMessage{Message: "before"})
	}
	iterateMainContext(t, func() bool { return calls.Load() == 5 })
	if calls.Load() != 5 {
		t.Fatalf("calls = %d, want 5", calls.Load())
	}

	unsub()
	if SubscribeCount[TextMessage](b) != 0 {
		t.Fatalf("live handlers after unsubscribe = %d, want 0 (leak)", SubscribeCount[TextMessage](b))
	}

	for i := 0; i < 5; i++ {
		b.Emit(TextMessage{Message: "after"})
	}
	b.Emit(ChannelRemoved{ID: 1})
	drainMainContext(t)
	time.Sleep(100 * time.Millisecond)
	drainMainContext(t)
	if calls.Load() != 5 {
		t.Fatalf("handler called after unsubscribe: %d calls", calls.Load())
	}
}
