package pipeline

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Tap observes a captured frame synchronously on the capture goroutine
// (VAD, level meter). Taps must not block or allocate.
type Tap interface {
	Process(f *Frame)
}

// TapFunc adapts a function to Tap.
type TapFunc func(f *Frame)

func (t TapFunc) Process(f *Frame) { t(f) }

// EncodeFunc encodes one 480-sample frame to a packet. The returned slice
// is handed to PacketSink and then discarded; it must not alias pooled
// memory. (internal/audio/opus.Encoder.Encode adapts directly.)
type EncodeFunc func(pcm []int16) ([]byte, error)

// DecodeFunc decodes one packet to one 480-sample frame. An empty packet
// must produce a concealment frame (PLC). (opus.Decoder.Decode adapts
// directly.)
type DecodeFunc func(pkt []byte) ([]int16, error)

// PacketSource yields encoded packets for the decode stage; io.EOF ends
// the stream.
type PacketSource interface {
	ReadPacket() ([]byte, error)
}

// PacketSink receives encoded packets from the encode stage.
type PacketSink interface {
	WritePacket(pkt []byte) error
}

// Config wires a pipeline. Source+Encode+PacketOut form the send side;
// PacketIn+Decode+Sink the receive side. Both sides always run; tests
// that only need one side pass a noop for the other.
type Config struct {
	Source    Source
	Taps      []Tap
	Encode    EncodeFunc
	PacketOut PacketSink

	PacketIn PacketSource
	Decode   DecodeFunc
	Sink     Sink

	// PreSink is the decode→sink seam. Nil → internal drop-oldest queue.
	// The jitter buffer (T10/T20) plugs in here.
	PreSink FrameRelay

	// QueueDepth is the depth of each bounded inter-stage queue in
	// frames. Default 8 (80 ms).
	QueueDepth int
}

// Stats is a snapshot of pipeline counters, all maintained atomically on
// the hot path (no locks).
type Stats struct {
	Captured      uint64
	Encoded       uint64
	PacketsOut    uint64
	PacketsIn     uint64
	Decoded       uint64
	Played        uint64
	DroppedFrames uint64
	EncodeErrors  uint64
	DecodeErrors  uint64
	SinkErrors    uint64

	CaptureToEncodeReadyNS uint64 // total across encoded frames
	MaxCaptureToEncodeNS   uint64
	EncodeNS               uint64 // total encode-callback time
	MaxEncodeNS            uint64
}

type counters struct {
	captured, encoded, packetsOut, packetsIn, decoded, played uint64
	encodeErr, decodeErr, sinkErr                             uint64
	encTotal, encMax, c2eTotal, c2eMax                        uint64
}

// Pipeline is the staged voice graph described in the package doc: each
// stage runs on its own goroutine; every bounded queue drops oldest on
// overflow.
type Pipeline struct {
	cfg     Config
	pool    *framePool
	encQ    *queueRelay
	preSink FrameRelay
	c       counters
	wg      sync.WaitGroup
	seq     atomic.Uint64
}

func New(cfg Config) *Pipeline {
	depth := cfg.QueueDepth
	if depth == 0 {
		depth = 8
	}
	pool := newFramePool(4 * depth)
	p := &Pipeline{
		cfg:     cfg,
		pool:    pool,
		encQ:    newQueueRelay(depth, pool),
		preSink: cfg.PreSink,
	}
	if p.preSink == nil {
		p.preSink = newQueueRelay(depth, pool)
	}
	return p
}

// Start launches the stage goroutines. Cancel ctx to stop; finite sources
// end their side via io.EOF. Start must be called once.
func (p *Pipeline) Start(ctx context.Context) {
	p.wg.Add(4)
	go func() { defer p.wg.Done(); p.captureLoop(ctx) }()
	go func() { defer p.wg.Done(); p.encodeLoop(ctx) }()
	go func() { defer p.wg.Done(); p.decodeLoop(ctx) }()
	go func() { defer p.wg.Done(); p.sinkLoop(ctx) }()
}

// Wait blocks until all stages have exited (ctx cancellation or EOF).
func (p *Pipeline) Wait() { p.wg.Wait() }

// PreSinkRelay exposes the decode→sink seam for tests and the future
// jitter-buffer install.
func (p *Pipeline) PreSinkRelay() FrameRelay { return p.preSink }

func (p *Pipeline) Snapshot() Stats {
	return Stats{
		Captured:               atomic.LoadUint64(&p.c.captured),
		Encoded:                atomic.LoadUint64(&p.c.encoded),
		PacketsOut:             atomic.LoadUint64(&p.c.packetsOut),
		PacketsIn:              atomic.LoadUint64(&p.c.packetsIn),
		Decoded:                atomic.LoadUint64(&p.c.decoded),
		Played:                 atomic.LoadUint64(&p.c.played),
		DroppedFrames:          p.encQ.Drops() + p.preSink.Drops(),
		EncodeErrors:           atomic.LoadUint64(&p.c.encodeErr),
		DecodeErrors:           atomic.LoadUint64(&p.c.decodeErr),
		SinkErrors:             atomic.LoadUint64(&p.c.sinkErr),
		CaptureToEncodeReadyNS: atomic.LoadUint64(&p.c.c2eTotal),
		MaxCaptureToEncodeNS:   atomic.LoadUint64(&p.c.c2eMax),
		EncodeNS:               atomic.LoadUint64(&p.c.encTotal),
		MaxEncodeNS:            atomic.LoadUint64(&p.c.encMax),
	}
}

func (p *Pipeline) captureLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			p.encQ.Close()
			return
		default:
		}
		pcm, err := p.cfg.Source.Read()
		if err != nil {
			p.encQ.Close()
			return
		}
		f := p.pool.Get()
		copy(f.Data[:], pcm)
		f.Seq = p.seq.Add(1)
		f.CapturedAt = time.Now()
		for _, t := range p.cfg.Taps {
			t.Process(f)
		}
		p.encQ.Push(f)
		atomic.AddUint64(&p.c.captured, 1)
	}
}

func (p *Pipeline) encodeLoop(ctx context.Context) {
	defer p.closePacketOut()
	encode := p.cfg.Encode
	for {
		if !p.encQ.WaitReady(ctx) {
			return
		}
		for {
			f, ok := p.encQ.Pull()
			if !ok {
				break
			}
			f.EncodeReadyAt = time.Now()
			if encode != nil {
				pkt, err := encode(f.PCM())
				f.EncodedAt = time.Now()
				if err != nil {
					atomic.AddUint64(&p.c.encodeErr, 1)
				} else {
					atomic.AddUint64(&p.c.encoded, 1)
					p.recordEncodeLatency(f)
					if p.cfg.PacketOut != nil {
						if werr := p.cfg.PacketOut.WritePacket(pkt); werr != nil {
							atomic.AddUint64(&p.c.sinkErr, 1)
						} else {
							atomic.AddUint64(&p.c.packetsOut, 1)
						}
					}
				}
			}
			p.pool.Put(f)
		}
	}
}

// closePacketOut propagates end-of-stream: a PacketSink that is also an
// io.Closer (e.g. an in-memory network mock) gets closed once every
// encoded frame has been handed over, so the receive side can see EOF.
func (p *Pipeline) closePacketOut() {
	if c, ok := p.cfg.PacketOut.(io.Closer); ok {
		c.Close()
	}
}

func (p *Pipeline) decodeLoop(ctx context.Context) {
	decode := p.cfg.Decode
	for {
		select {
		case <-ctx.Done():
			p.preSink.Close()
			return
		default:
		}
		pkt, err := p.cfg.PacketIn.ReadPacket()
		if err != nil {
			p.preSink.Close()
			return
		}
		atomic.AddUint64(&p.c.packetsIn, 1)
		pcm, derr := decode(pkt)
		if derr != nil {
			atomic.AddUint64(&p.c.decodeErr, 1)
			continue
		}
		f := p.pool.Get()
		copy(f.Data[:], pcm)
		f.Seq = p.seq.Add(1)
		f.DecodedAt = time.Now()
		atomic.AddUint64(&p.c.decoded, 1)
		p.preSink.Push(f)
	}
}

func (p *Pipeline) sinkLoop(ctx context.Context) {
	for {
		if !p.preSink.WaitReady(ctx) {
			return
		}
		for {
			f, ok := p.preSink.Pull()
			if !ok {
				break
			}
			f.PlayedAt = time.Now()
			if err := p.cfg.Sink.Write(f.PCM()); err != nil {
				atomic.AddUint64(&p.c.sinkErr, 1)
			} else {
				atomic.AddUint64(&p.c.played, 1)
			}
			p.pool.Put(f)
		}
	}
}

func (p *Pipeline) recordEncodeLatency(f *Frame) {
	ns := uint64(f.EncodedAt.Sub(f.EncodeReadyAt))
	atomic.AddUint64(&p.c.encTotal, ns)
	maxAtomic(&p.c.encMax, ns)
	ns = uint64(f.EncodeReadyAt.Sub(f.CapturedAt))
	atomic.AddUint64(&p.c.c2eTotal, ns)
	maxAtomic(&p.c.c2eMax, ns)
}

func maxAtomic(addr *uint64, v uint64) {
	for {
		old := atomic.LoadUint64(addr)
		if v <= old || atomic.CompareAndSwapUint64(addr, old, v) {
			return
		}
	}
}
