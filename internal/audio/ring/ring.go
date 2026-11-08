// Package ring provides a fixed-frame-size single-producer single-consumer
// (SPSC) lock-free ring buffer for real-time audio handoff.
//
// One ring per direction: the malgo capture callback produces into a ring
// while the pipeline capture stage consumes it; the pipeline sink produces
// into a second ring while the malgo playback callback consumes it. All
// storage is pre-allocated at construction; the hot path is memcpy plus
// atomic word reads/writes only — no locks, no allocations, no syscalls.
//
// Overflow policy is split by producer type. Push is for real-time
// producers (device callbacks): it may drop frames but never waits —
// on a full ring it reclaims the oldest un-read slot (drop-oldest),
// unless the consumer is mid-copy of exactly that slot, in which case
// the new frame is dropped instead. TryPush is for lossless producers
// (tests, file streams): it returns false on a full ring and never
// destroys un-read frames, so a retry-until-success loop transfers
// every frame.
package ring

import (
	"sync/atomic"
)

// Ring is a fixed-frame-size SPSC ring. Construct with New. A Ring must
// not be copied after construction.
type Ring struct {
	slots [][]byte        // pre-allocated frame storage, len == n
	seq   []atomic.Int64  // per-slot state machine (see below)
	head  atomic.Uint64   // next frame generation to read
	tail  atomic.Uint64   // next frame generation to write
	drop  atomic.Uint64   // frames lost (stolen oldest + rejected newest)
	n     uint64          // number of slots (generations wrap mod n)
}

// Per-slot state, keyed by generation g (a slot j serves generations
// g ≡ j mod n). Sequence values:
//
//	2*g        EMPTY for gen g     — producer may write frame g into slot j
//	2*g+1      FULL for gen g      — consumer may claim, producer may steal
//	2*(g+n)+1  CLAIMED gen g       — consumer is copying; protected from steal
//
// After the consumer finishes its copy it stores 2*(g+n), releasing the
// slot for generation g+n. When the producer steals (drop-oldest) it
// CASes FULL → 2*(g+n), releasing the slot without a read.
//
// Go's atomics are sequentially consistent, which gives the needed
// happens-before edges between slot data writes and the seq publication
// store, so the -race detector sees no torn sharing.

// New returns a ring holding numFrames frames of frameBytes bytes each.
// Panics on non-positive dimensions — callers fix ring sizes at compile
// time; this is a programmer-error guard, not a runtime path.
func New(frameBytes, numFrames int) *Ring {
	if frameBytes <= 0 || numFrames <= 0 {
		panic("ring: frameBytes and numFrames must be positive")
	}
	r := &Ring{n: uint64(numFrames)}
	r.slots = make([][]byte, numFrames)
	for i := range r.slots {
		r.slots[i] = make([]byte, frameBytes)
	}
	r.seq = make([]atomic.Int64, numFrames)
	for i := range r.seq {
		r.seq[i].Store(int64(2 * i)) // EMPTY for generation i
	}
	return r
}

// Push copies src (exactly one frame, len == frameBytes) into the next
// free slot and reports whether the frame was accepted. On overflow the
// oldest unread frame is dropped to make room (drop-oldest); if that
// oldest slot is being copied out by the consumer at that instant, the
// pushed frame is dropped instead. Push never blocks and never allocates.
func (r *Ring) Push(src []byte) bool {
	if len(src) != len(r.slots[0]) {
		r.drop.Add(1)
		return false
	}
	for {
		t := r.tail.Load()
		j := int(t % r.n)
		if r.seq[j].Load() == int64(2*t) { // EMPTY for my generation
			copy(r.slots[j], src)
			r.seq[j].Store(int64(2*t + 1)) // FULL
			r.tail.Store(t + 1)
			return true
		}
		// Slot j is not writable: the ring is (logically) full. Steal the
		// oldest FULL slot. When tail-head == n the stolen slot IS slot j,
		// so exactly one successful steal guarantees the retry writes.
		h := r.head.Load()
		jh := int(h % r.n)
		if r.seq[jh].CompareAndSwap(int64(2*h+1), int64(2*(h+r.n))) {
			r.head.Add(1)
			r.drop.Add(1)
			continue // retry the write; it must succeed now
		}
		// Consumer is claiming/copying the oldest slot right now: drop
		// the new frame rather than wait (real-time discipline).
		r.drop.Add(1)
		return false
	}
}

// TryPush copies src into the next free slot if one is available and
// reports whether the frame was accepted. Unlike Push it never drops
// un-read frames: on a full ring (or a size mismatch, which counts as
// a drop) it returns false, so a lossless producer can wait and retry
// until the consumer frees a slot. Never blocks, never allocates.
func (r *Ring) TryPush(src []byte) bool {
	if len(src) != len(r.slots[0]) {
		r.drop.Add(1)
		return false
	}
	t := r.tail.Load()
	j := int(t % r.n)
	if r.seq[j].Load() != int64(2*t) { // not EMPTY for my generation
		return false
	}
	copy(r.slots[j], src)
	r.seq[j].Store(int64(2*t + 1)) // FULL
	r.tail.Store(t + 1)
	return true
}

// Pop copies one frame into dst (len == frameBytes) and reports whether a
// frame was available. Pop never blocks and never allocates; a false
// return may be spurious only while a concurrent Push steals that exact
// slot, in which case a later Pop sees the replacement frame.
func (r *Ring) Pop(dst []byte) bool {
	if len(dst) != len(r.slots[0]) {
		return false
	}
	h := r.head.Load()
	j := int(h % r.n)
	// Claim: FULL → CLAIMED makes the slot invisible to a concurrent
	// steal, so the copy below cannot race a Push overwrite.
	if !r.seq[j].CompareAndSwap(int64(2*h+1), int64(2*(h+r.n)+1)) {
		return false
	}
	copy(dst, r.slots[j])
	r.seq[j].Store(int64(2 * (h + r.n))) // release for generation h+n
	r.head.Add(1)
	return true
}

// Len returns the number of frames currently available. It may lag a
// concurrent Pop by one frame for the duration of the copy; diagnostics
// only — never use it for synchronization.
func (r *Ring) Len() int {
	t := r.tail.Load()
	h := r.head.Load()
	if t < h {
		return 0
	}
	return int(t - h)
}

// Drops returns the number of frames lost to overflow (stolen oldest plus
// rejected newest) since construction.
func (r *Ring) Drops() uint64 { return r.drop.Load() }
