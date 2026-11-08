package pipeline

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pktPipe is an in-memory stand-in for the UDP voice path: encoded
// packets flow send→receive; Close closes the stream so the decode side
// sees EOF.
type pktPipe struct {
	mu   sync.Mutex
	ch   chan []byte
	loss func(n int) bool // deterministic loss: return true to drop
	n    int
}

func newPktPipe() *pktPipe { return &pktPipe{ch: make(chan []byte, 64)} }

func (p *pktPipe) WritePacket(pkt []byte) error {
	p.mu.Lock()
	p.n++
	drop := p.loss != nil && p.loss(p.n)
	p.mu.Unlock()
	if drop {
		return nil
	}
	cp := make([]byte, len(pkt))
	copy(cp, pkt)
	p.ch <- cp
	return nil
}

func (p *pktPipe) ReadPacket() ([]byte, error) {
	pkt, ok := <-p.ch
	if !ok {
		return nil, io.EOF
	}
	return pkt, nil
}

func (p *pktPipe) Close() error { close(p.ch); return nil }

// trivialCodec "encodes" a frame to its first sample so round-trip
// identity can be asserted without Opus.
func trivialEncoder() EncodeFunc {
	return func(pcm []int16) ([]byte, error) {
		b := make([]byte, 2)
		b[0] = byte(pcm[0])
		b[1] = byte(pcm[0] >> 8)
		return b, nil
	}
}

func trivialDecoder() DecodeFunc {
	return func(pkt []byte) ([]int16, error) {
		out := make([]int16, FrameSize)
		if len(pkt) == 2 {
			v := int16(pkt[0]) | int16(pkt[1])<<8
			for i := range out {
				out[i] = v
			}
		}
		return out, nil
	}
}

// memSource yields n frames of a constant sample value, optionally paced
// to model the 100 pps device cadence (free-running bursts overflow the
// bounded queues before the consumer goroutines wake, which the drop
// policy handles but tests asserting full delivery want to avoid).
type memSource struct {
	val   int16
	n     int
	pace  time.Duration
	buf   [FrameSize]int16
	ended bool
}

func (m *memSource) Read() ([]int16, error) {
	if m.n <= 0 {
		return nil, io.EOF
	}
	m.n--
	if m.pace > 0 {
		time.Sleep(m.pace)
	}
	for i := range m.buf {
		m.buf[i] = m.val
	}
	return m.buf[:], nil
}

func paced(val int16, n int) *memSource {
	return &memSource{val: val, n: n, pace: 100 * time.Microsecond}
}

func TestPipelineFiniteFlow(t *testing.T) {
	const n = 100
	pipe := newPktPipe()
	sink := NewRecordingSink()
	p := New(Config{
		Source:    paced(1234, n),
		Encode:    trivialEncoder(),
		PacketOut: pipe,
		PacketIn:  pipe,
		Decode:    trivialDecoder(),
		Sink:      sink,
	})
	p.Start(context.Background())
	p.Wait()

	st := p.Snapshot()
	if st.Captured != n || st.Encoded != n || st.PacketsOut != n {
		t.Fatalf("send side: %+v", st)
	}
	if st.PacketsIn != n || st.Decoded != n || st.Played != n {
		t.Fatalf("recv side: %+v", st)
	}
	if st.DroppedFrames != 0 {
		t.Fatalf("unexpected drops: %d", st.DroppedFrames)
	}
	first := sink.Frames()[0]
	if first[0] != 1234 {
		t.Fatalf("payload corrupted: %d", first[0])
	}
}

type sinkFunc func(frame []int16) error

func (f sinkFunc) Write(frame []int16) error { return f(frame) }

func TestPipelineSlowSinkDropsOldest(t *testing.T) {
	const n = 50
	release := make(chan struct{})
	var writtenMu sync.Mutex
	var written []int16

	p := New(Config{
		Source:   &memSource{val: 0, n: 0},
		PacketIn: &packetListSource{},
		Decode:   func(pkt []byte) ([]int16, error) { return make([]int16, FrameSize), nil },
		Sink: sinkFunc(func(frame []int16) error {
			<-release
			writtenMu.Lock()
			written = append(written, frame[0])
			writtenMu.Unlock()
			return nil
		}),
		QueueDepth: 4,
	})

	// push n frames straight into the decode→sink relay
	go func() {
		for i := 0; i < n; i++ {
			f := p.pool.Get()
			for j := range f.Data {
				f.Data[j] = int16(i)
			}
			p.preSink.Push(f)
		}
		p.preSink.Close()
	}()

	time.Sleep(50 * time.Millisecond) // let the queue overflow
	close(release)
	p.Start(context.Background())
	p.Wait()

	st := p.Snapshot()
	if st.DroppedFrames == 0 {
		t.Fatal("no drops despite blocked sink")
	}
	writtenMu.Lock()
	defer writtenMu.Unlock()
	if len(written)+int(st.DroppedFrames) != n {
		t.Fatalf("frames not conserved: written %d + dropped %d != %d", len(written), st.DroppedFrames, n)
	}
	if len(written) > 4+1 {
		t.Fatalf("sink wrote %d frames through a depth-4 queue", len(written))
	}
	// drop-oldest: the surviving frames must be the newest
	if int(written[len(written)-1]) != n-1 {
		t.Fatalf("newest frame lost: last written = %d, want %d", written[len(written)-1], n-1)
	}
}

type packetListSource struct {
	pkts [][]byte
}

func (s *packetListSource) ReadPacket() ([]byte, error) {
	if len(s.pkts) == 0 {
		return nil, io.EOF
	}
	pkt := s.pkts[0]
	s.pkts = s.pkts[1:]
	return pkt, nil
}

type nopPacketSink struct{}

func (nopPacketSink) WritePacket(pkt []byte) error { return nil }

func TestPipelineTapFlow(t *testing.T) {
	var mu sync.Mutex
	seen := 0
	tap := TapFunc(func(f *Frame) {
		mu.Lock()
		seen++
		mu.Unlock()
	})
	const n = 40
	pipe := newPktPipe()
	p := New(Config{
		Source:    paced(9, n),
		Taps:      []Tap{tap},
		Encode:    trivialEncoder(),
		PacketOut: pipe,
		PacketIn:  pipe,
		Decode:    trivialDecoder(),
		Sink:      NewRecordingSink(),
	})
	p.Start(context.Background())
	p.Wait()
	mu.Lock()
	defer mu.Unlock()
	if seen != n {
		t.Fatalf("tap saw %d frames, want %d", seen, n)
	}
}

func TestPipelineCustomPreSinkSeam(t *testing.T) {
	relay := newSpyRelay()
	p := New(Config{
		Source:   &memSource{val: 0, n: 0},
		PreSink:  relay,
		PacketIn: &packetListSource{pkts: [][]byte{{1, 2}, {3, 4}}},
		Decode:   func(pkt []byte) ([]int16, error) { return make([]int16, FrameSize), nil },
		Sink:     sinkFunc(func([]int16) error { return nil }),
	})
	if p.PreSinkRelay() != relay {
		t.Fatal("custom relay not installed")
	}
	p.Start(context.Background())
	p.Wait()
	if relay.pushes.Load() != 2 {
		t.Fatalf("relay pushes = %d, want 2", relay.pushes.Load())
	}
	if !relay.closed.Load() {
		t.Fatal("relay never closed")
	}
}

type spyRelay struct {
	pushes atomic.Int64
	closed atomic.Bool
	ch     chan *Frame
}

func newSpyRelay() *spyRelay { return &spyRelay{ch: make(chan *Frame, 16)} }

func (s *spyRelay) Push(f *Frame) { s.pushes.Add(1); s.ch <- f }
func (s *spyRelay) Pull() (*Frame, bool) {
	select {
	case f := <-s.ch:
		return f, true
	default:
		return nil, false
	}
}

func (s *spyRelay) WaitReady(ctx context.Context) bool {
	for {
		if len(s.ch) > 0 {
			return true
		}
		if s.closed.Load() {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Millisecond):
		}
	}
}

func (s *spyRelay) Closed() bool  { return s.closed.Load() }
func (s *spyRelay) Close()        { s.closed.Store(true) }
func (s *spyRelay) Drops() uint64 { return 0 }

func TestPipelineLatencyInstrumentation(t *testing.T) {
	const n = 100
	pipe := newPktPipe()
	p := New(Config{
		Source:    paced(5, n),
		Encode:    trivialEncoder(),
		PacketOut: pipe,
		PacketIn:  pipe,
		Decode:    trivialDecoder(),
		Sink:      NewRecordingSink(),
	})
	p.Start(context.Background())
	p.Wait()
	st := p.Snapshot()
	if st.Encoded != n {
		t.Fatalf("encoded %d", st.Encoded)
	}
	avg := st.CaptureToEncodeReadyNS / st.Encoded
	if avg > uint64(2*time.Millisecond) {
		t.Fatalf("avg capture→encode-ready = %v > 2 ms budget", time.Duration(avg))
	}
	if st.MaxCaptureToEncodeNS > uint64(50*time.Millisecond) {
		t.Fatalf("max capture→encode-ready = %v (scheduling anomaly beyond 50 ms)",
			time.Duration(st.MaxCaptureToEncodeNS))
	}
	if st.EncodeNS == 0 {
		t.Fatal("encode time not instrumented")
	}
	t.Logf("capture→encode-ready: avg %v, max %v; encode: avg %v, max %v",
		time.Duration(avg), time.Duration(st.MaxCaptureToEncodeNS),
		time.Duration(st.EncodeNS/st.Encoded), time.Duration(st.MaxEncodeNS))
}

func TestPipelineCtxStop(t *testing.T) {
	p := New(Config{
		Source:   NewSinSource(440, 5000), // infinite
		Encode:   trivialEncoder(),
		PacketIn: blockPacketSource{},
		Decode:   func([]byte) ([]int16, error) { return make([]int16, FrameSize), nil },
		Sink:     NewRecordingSink(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)
	time.Sleep(30 * time.Millisecond)
	cancel()
	done := make(chan struct{})
	go func() { p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline did not stop on ctx cancel")
	}
}

// blockPacketSource stalls ReadPacket briefly, long enough to prove ctx
// cancellation wins over a blocking read.
type blockPacketSource struct{}

func (blockPacketSource) ReadPacket() ([]byte, error) {
	time.Sleep(300 * time.Millisecond)
	return nil, errors.New("blocked")
}
