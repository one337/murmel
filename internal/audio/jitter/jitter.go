// Package jitter implements the playback-side de-jitter buffer for
// received voice frames: the absorbing layer between order-independent
// packet arrival (UDP transport, upstream gumble TODO handlers.go:116
// "use in jitter buffer") and the fixed 10 ms playback cadence (
// pipeline integration).
//
// The component is pure: decoded frames enter via Consume keyed by the
// per-sender monotonic sequence number (int64, as in the voice framing),
// playback frames leave via Next. There is no transport or audio-device
// coupling and no timing inside — the caller's real-time pacing (one
// Next per 10 ms) provides the clock. One Buffer serves one sender.
//
// Design (simple, deterministic — no adaptive estimation):
//
//   - Frames are held in a map keyed by sequence number; the buffered
//     span is bounded by the catch-up depth, so storage is O(catchUp).
//   - The drain position nextSeq only ever moves forward. Frames older
//     than it are late and dropped; re-transmits of buffered frames are
//     duplicates and dropped. Reorders within the catch-up span are
//     reassembled in order.
//   - Interior gaps (a missing nextSeq while newer frames are buffered)
//     are concealed by the caller-supplied Concealer (the Opus decoder's
//     PLC: Decode(nil, 480)) — one concealment frame per missing seq.
//     An empty buffer reports starvation instead of synthesizing audio.
//   - Burst catch-up: when a frame's span (seq - nextSeq + 1) exceeds
//     catchUpDepth (3× target), the drain position jumps to
//     newest-target+1. Buffered frames below the jump are burst drops;
//     never-arrived sequence numbers in between are skipped without
//     concealment. This keeps added latency bounded by the catch-up
//     depth and returns the buffer toward the target depth in the same
//     drain window, trading the excess frames (drop-oldest, matching
//     the pipeline's overflow policy) for latency.
//   - Starved restart: a frame landing past the catch-up span while the
//     buffer is empty (a long idle gap) resumes playback at that frame;
//     the idle span is skipped without concealment — PLC is for lost
//     packets inside a stream, not for silence.
//
// The target depth is the steady-state buffering level the caller's
// pacing maintains (default 3 frames = 30 ms; configurable 2–8). All
// accounting is exposed via Metrics for the UI latency display.
package jitter

import "fmt"

// FrameSize is the fixed voice frame length in samples: 480 samples of
// mono 48 kHz PCM16 = 10 ms, matching internal/audio/pipeline.
const FrameSize = 480

// Target depth bounds, in frames. The default of 3 frames = 30 ms of
// de-jitter margin; the configurable range is 2–8 (20–80 ms).
const (
	DefaultTargetDepth = 3
	MinTargetDepth     = 2
	MaxTargetDepth     = 8

	// catchUpMultiplier fixes the burst threshold at target × 3: spans
	// beyond it trigger the catch-up jump down to the target depth.
	catchUpMultiplier = 3
)

// Concealer synthesizes one concealment frame for a lost packet. The
// Opus decoder satisfies it via Decode(nil, FrameSize) (PLC); keeping
// it an interface leaves decoder internals untouched.
type Concealer interface {
	Conceal() []int16
}

// ConcealerFunc adapts a plain function to Concealer.
type ConcealerFunc func() []int16

// Conceal implements Concealer.
func (f ConcealerFunc) Conceal() []int16 { return f() }

// Metrics is a point-in-time snapshot of the buffer's accounting.
type Metrics struct {
	Depth      int    // frames currently buffered
	Filled     uint64 // frames accepted into the buffer
	Delivered  uint64 // real frames returned by Next
	PLC        uint64 // concealment frames returned by Next
	Duplicates uint64 // consumes dropped as re-transmits of buffered frames
	Late       uint64 // consumes dropped as older than the drain position
	Burst      uint64 // buffered frames discarded by catch-up jumps
	Skipped    uint64 // never-arrived sequence numbers jumped over by catch-up
}

// Drops returns the total dropped frames: Duplicates + Late + Burst.
func (m Metrics) Drops() uint64 { return m.Duplicates + m.Late + m.Burst }

// Outputs returns the total frames returned by Next: Delivered + PLC.
func (m Metrics) Outputs() uint64 { return m.Delivered + m.PLC }

// Buffer is the playback-side jitter buffer for one sender's stream.
// It is not safe for concurrent use; the voice engine owns it on the
// playback goroutine (Consume arrives via the decode callback hand-off).
type Buffer struct {
	target  int
	catchUp int
	plc     Concealer
	frames  map[int64][]int16
	nextSeq int64
	started bool

	filled    uint64
	delivered uint64
	plcCount  uint64
	dups      uint64
	late      uint64
	burst     uint64
	skipped   uint64
}

// New returns a Buffer with the given target depth (2–8 frames) and
// Concealer for loss fill-in.
func New(targetDepth int, plc Concealer) (*Buffer, error) {
	if targetDepth < MinTargetDepth || targetDepth > MaxTargetDepth {
		return nil, fmt.Errorf("jitter: target depth %d outside [%d, %d]",
			targetDepth, MinTargetDepth, MaxTargetDepth)
	}
	if plc == nil {
		return nil, fmt.Errorf("jitter: nil Concealer")
	}
	return &Buffer{
		target:  targetDepth,
		catchUp: catchUpMultiplier * targetDepth,
		plc:     plc,
		frames:  make(map[int64][]int16, catchUpMultiplier*MaxTargetDepth),
	}, nil
}

// TargetDepth returns the configured target buffer depth in frames.
func (b *Buffer) TargetDepth() int { return b.target }

// CatchUpDepth returns the burst threshold in frames: spans beyond it
// trigger the catch-up jump back to the target depth.
func (b *Buffer) CatchUpDepth() int { return b.catchUp }

// Consume hands one decoded frame to the buffer under its sequence
// number. The frame is copied; the caller may reuse its slice. Frames
// are dropped silently (counted in Metrics) when stale, duplicated, or
// displaced by a catch-up jump — Consume never blocks or fails.
func (b *Buffer) Consume(seq int64, frame []int16) {
	if !b.started {
		b.started = true
		b.nextSeq = seq
		b.store(seq, frame)
		return
	}
	if seq < b.nextSeq {
		b.late++
		return
	}
	if _, dup := b.frames[seq]; dup {
		b.dups++
		return
	}
	if seq-b.nextSeq+1 > int64(b.catchUp) {
		if len(b.frames) == 0 {
			// Starved restart: the buffer drained during an idle
			// gap, so there is nothing to catch up from — resume
			// playback at the new frame instead of concealing the
			// idle period as packet loss. Like a catch-up jump's
			// never-arrived span, the idle seqs count as skipped.
			b.skipped += uint64(seq - b.nextSeq)
			b.nextSeq = seq
		} else {
			b.jumpTo(seq)
		}
	}
	b.store(seq, frame)
}

// store copies frame into the buffer. seq is guaranteed within
// [nextSeq, nextSeq+catchUp) by the callers.
func (b *Buffer) store(seq int64, frame []int16) {
	cp := make([]int16, len(frame))
	copy(cp, frame)
	b.frames[seq] = cp
	b.filled++
}

// jumpTo performs burst catch-up: the drain position moves to
// newest-target+1, buffered frames below it are dropped (burst), and
// the never-arrived sequence numbers in between are counted as skipped.
func (b *Buffer) jumpTo(newest int64) {
	targetSeq := newest - int64(b.target) + 1
	dropped := 0
	for seq := range b.frames {
		if seq < targetSeq {
			delete(b.frames, seq)
			dropped++
		}
	}
	b.burst += uint64(dropped)
	b.skipped += uint64(targetSeq-b.nextSeq) - uint64(dropped)
	b.nextSeq = targetSeq
}

// Next returns the next playback frame at the fixed 10 ms cadence. It
// succeeds exactly when the buffer is non-empty: a buffered frame for
// the drain position is returned as-is, a missing drain position with
// newer frames buffered is concealed by the Concealer, and an empty
// buffer reports starvation (false, nil) — the caller pauses rather
// than the buffer inventing audio. Returned slices are owned by the
// caller; the buffer keeps no reference to them.
func (b *Buffer) Next() ([]int16, bool) {
	if !b.started {
		return nil, false
	}
	if f, ok := b.frames[b.nextSeq]; ok {
		delete(b.frames, b.nextSeq)
		b.nextSeq++
		b.delivered++
		return f, true
	}
	if len(b.frames) == 0 {
		return nil, false
	}
	b.nextSeq++
	b.plcCount++
	return b.plc.Conceal(), true
}

// Depth returns the number of frames currently buffered.
func (b *Buffer) Depth() int { return len(b.frames) }

// Metrics returns a snapshot of the buffer's counters.
func (b *Buffer) Metrics() Metrics {
	return Metrics{
		Depth:      len(b.frames),
		Filled:     b.filled,
		Delivered:  b.delivered,
		PLC:        b.plcCount,
		Duplicates: b.dups,
		Late:       b.late,
		Burst:      b.burst,
		Skipped:    b.skipped,
	}
}

// Reset returns the buffer to a fresh state: no frames, no drain
// position, all counters cleared. The next Consume starts a new stream
// regardless of its sequence number (e.g. a new talk spurt).
func (b *Buffer) Reset() {
	b.frames = make(map[int64][]int16, catchUpMultiplier*MaxTargetDepth)
	b.nextSeq = 0
	b.started = false
	b.filled, b.delivered, b.plcCount = 0, 0, 0
	b.dups, b.late, b.burst, b.skipped = 0, 0, 0, 0
}
