package pipeline

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Fixed voice format: mono 48 kHz PCM16, 480-sample frames = 10 ms,
// 100 frames/s (matches internal/audio/opus constants).
const (
	SampleRate      = 48000
	FrameSize       = 480
	FrameDuration   = 10 * time.Millisecond
	FramesPerSecond = 100
)

// Frame is one 10 ms voice frame plus its latency trace. The payload is a
// fixed array so frames can be pooled and recycled with zero allocation;
// PCM returns the payload as a slice without copying.
type Frame struct {
	Seq  uint64
	Data [FrameSize]int16

	CapturedAt    time.Time // source read completed (capture stage)
	EncodeReadyAt time.Time // handed to the encode callback
	EncodedAt     time.Time // encode callback returned
	DecodedAt     time.Time // decode callback returned
	PlayedAt      time.Time // delivered to the sink
}

// PCM returns the frame payload. The slice aliases the frame; it is valid
// until the frame is recycled to its pool.
func (f *Frame) PCM() []int16 { return f.Data[:] }

// framePool recycles pre-allocated frames LIFO (cache-friendly, and a
// Get right after a Put deterministically returns the same frame). Get
// blocks only if every pooled frame is simultaneously parked in a bounded
// queue — impossible when pool capacity exceeds total queue capacity,
// which New guarantees.
type framePool struct {
	mu   sync.Mutex
	free []*Frame
}

func newFramePool(capacity int) *framePool {
	p := &framePool{free: make([]*Frame, 0, capacity)}
	for i := 0; i < capacity; i++ {
		p.free = append(p.free, &Frame{})
	}
	return p
}

func (p *framePool) Get() *Frame {
	p.mu.Lock()
	n := len(p.free) - 1
	f := p.free[n]
	p.free[n] = nil
	p.free = p.free[:n]
	p.mu.Unlock()
	return f
}

func (p *framePool) Put(f *Frame) {
	p.mu.Lock()
	p.free = append(p.free, f)
	p.mu.Unlock()
}

// queueRelay is the default FrameRelay: a bounded drop-oldest queue of
// pooled frames. When full, Push discards the oldest queued frame
// (recycling it) so producers never block and the newest audio survives.
// ready is signalled (cap 1) on every push; closedCh broadcasts Close.
type queueRelay struct {
	ch        chan *Frame
	ready     chan struct{}
	closedCh  chan struct{}
	pool      *framePool
	dropped   atomic.Uint64
	closeOnce sync.Once
}

func newQueueRelay(depth int, pool *framePool) *queueRelay {
	return &queueRelay{
		ch:       make(chan *Frame, depth),
		ready:    make(chan struct{}, 1),
		closedCh: make(chan struct{}),
		pool:     pool,
	}
}

func (q *queueRelay) Push(f *Frame) {
	select {
	case q.ch <- f:
	default:
		select {
		case old := <-q.ch:
			q.pool.Put(old)
			q.dropped.Add(1)
		default:
		}
		select {
		case q.ch <- f:
		default:
			q.pool.Put(f)
			q.dropped.Add(1)
		}
	}
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

func (q *queueRelay) Pull() (*Frame, bool) {
	select {
	case f := <-q.ch:
		return f, true
	default:
		return nil, false
	}
}

// WaitReady blocks until a frame is available, the relay closes (final
// drain still allowed), or ctx ends.
func (q *queueRelay) WaitReady(ctx context.Context) bool {
	for {
		if len(q.ch) > 0 {
			return true
		}
		select {
		case <-q.ready:
		case <-q.closedCh:
			if len(q.ch) > 0 {
				return true
			}
			return false
		case <-ctx.Done():
			return false
		}
	}
}

func (q *queueRelay) Closed() bool {
	select {
	case <-q.closedCh:
		return true
	default:
		return false
	}
}

func (q *queueRelay) Close() { q.closeOnce.Do(func() { close(q.closedCh) }) }

func (q *queueRelay) Drops() uint64 { return q.dropped.Load() }

func (q *queueRelay) Len() int { return len(q.ch) }

// FrameRelay is the seam between the decode stage and the playback sink
// where the jitter buffer plugs in. Push must be non-blocking;
// consumers block in WaitReady.
type FrameRelay interface {
	Push(*Frame)
	Pull() (*Frame, bool)
	WaitReady(ctx context.Context) bool
	Closed() bool
	Close()
	Drops() uint64
}
