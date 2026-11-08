package jitter

import (
	"math"
	"testing"
)

// Test frame payloads are self-describing so property tests can classify
// every output as a real frame (and recover its seq) or a concealment
// frame purely by content.
const (
	realMarker int16 = 0x2A2A
	plcMarker  int16 = 0x504C
)

// dataFrame builds the deterministic payload for the real frame with
// sequence number seq: samples 0/1 carry the low 32 bits of seq, sample 2
// carries the real marker, the rest a seq-dependent pattern. Valid for
// 0 ≤ seq < 2^32 (frameSeq inverts it).
func dataFrame(seq int64) []int16 {
	f := make([]int16, FrameSize)
	f[0] = int16(uint16(seq & 0xFFFF))
	f[1] = int16(uint16((seq >> 16) & 0xFFFF))
	f[2] = realMarker
	for i := 3; i < FrameSize; i++ {
		f[i] = int16(uint16(seq)) ^ int16(i*7)
	}
	return f
}

// frameSeq recovers the seq embedded by dataFrame.
func frameSeq(f []int16) int64 {
	return int64(uint16(f[0])) | int64(uint16(f[1]))<<16
}

// plcStub is the fixed concealment frame the test Concealer returns;
// distinct from every dataFrame by its marker sample.
func plcStub() []int16 {
	f := make([]int16, FrameSize)
	f[0], f[1] = 'P', 'L'
	f[2] = plcMarker
	for i := 3; i < FrameSize; i++ {
		f[i] = -1
	}
	return f
}

func testConcealer() Concealer {
	return ConcealerFunc(func() []int16 { return plcStub() })
}

func framesEqual(a, b []int16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// drainAll Nexts until starvation and returns the delivered frames.
func drainAll(t *testing.T, b *Buffer) [][]int16 {
	t.Helper()
	var out [][]int16
	for {
		f, ok := b.Next()
		if !ok {
			if f != nil {
				t.Fatalf("Next returned nil frame with ok=false")
			}
			return out
		}
		out = append(out, f)
	}
}

func mustNew(t *testing.T, target int) *Buffer {
	t.Helper()
	b, err := New(target, testConcealer())
	if err != nil {
		t.Fatalf("New(%d): %v", target, err)
	}
	return b
}

func TestNewValidation(t *testing.T) {
	if _, err := New(MinTargetDepth-1, testConcealer()); err == nil {
		t.Fatalf("target %d accepted, want rejection", MinTargetDepth-1)
	}
	if _, err := New(MaxTargetDepth+1, testConcealer()); err == nil {
		t.Fatalf("target %d accepted, want rejection", MaxTargetDepth+1)
	}
	if _, err := New(DefaultTargetDepth, nil); err == nil {
		t.Fatalf("nil Concealer accepted, want rejection")
	}
	for depth := MinTargetDepth; depth <= MaxTargetDepth; depth++ {
		b, err := New(depth, testConcealer())
		if err != nil {
			t.Fatalf("New(%d): %v", depth, err)
		}
		if b.TargetDepth() != depth {
			t.Fatalf("TargetDepth() = %d, want %d", b.TargetDepth(), depth)
		}
		if b.CatchUpDepth() != catchUpMultiplier*depth {
			t.Fatalf("CatchUpDepth() = %d, want %d", b.CatchUpDepth(), catchUpMultiplier*depth)
		}
	}
}

func TestInOrderDelivery(t *testing.T) {
	b := mustNew(t, DefaultTargetDepth)
	// Real-time model: each frame arrives in order and is drained the
	// same tick (arrival pace == playback pace, no jitter).
	for seq := int64(0); seq < 10; seq++ {
		b.Consume(seq, dataFrame(seq))
		f, ok := b.Next()
		if !ok {
			t.Fatalf("seq %d: stall with non-empty buffer", seq)
		}
		if !framesEqual(f, dataFrame(seq)) {
			t.Fatalf("seq %d: content mismatch", seq)
		}
	}
	m := b.Metrics()
	if m.Filled != 10 || m.Delivered != 10 || m.PLC != 0 || m.Drops() != 0 || m.Depth != 0 {
		t.Fatalf("metrics %+v", m)
	}
}

// TestSteadyStateLatency drives the real-time playback model: pre-roll of
// target frames, then one Consume and one Next per 10 ms tick. The buffer
// must hold exactly target frames the whole time — added latency is
// exactly the target depth, never more.
func TestSteadyStateLatency(t *testing.T) {
	const (
		preRoll = DefaultTargetDepth
		n       = 100
	)
	b := mustNew(t, DefaultTargetDepth)

	for seq := int64(0); seq < preRoll; seq++ {
		b.Consume(seq, dataFrame(seq))
	}
	for tick := 0; tick < n-preRoll; tick++ {
		if d := b.Depth(); d != preRoll {
			t.Fatalf("tick %d: depth %d, want %d (steady-state latency bound)", tick, d, preRoll)
		}
		b.Consume(int64(tick+preRoll), dataFrame(int64(tick+preRoll)))
		f, ok := b.Next()
		if !ok {
			t.Fatalf("tick %d: stall with non-empty buffer", tick)
		}
		if !framesEqual(f, dataFrame(int64(tick))) {
			t.Fatalf("tick %d: delivered wrong frame (want seq %d)", tick, tick)
		}
	}
	out := drainAll(t, b)
	if total := n - preRoll + len(out); total != n {
		t.Fatalf("delivered %d frames, want %d", total, n)
	}
	m := b.Metrics()
	if m.PLC != 0 || m.Drops() != 0 || m.Burst != 0 || m.Skipped != 0 {
		t.Fatalf("clean stream produced defects: %+v", m)
	}
}

// TestReorderWithinDepth: arrivals displaced up to the target depth —
// each block of target+1 frames delivered newest-first, the oldest
// displaced by exactly target positions — are reassembled in order
// while playback keeps draining: zero loss concealment, zero drops.
func TestReorderWithinDepth(t *testing.T) {
	for _, target := range []int{MinTargetDepth, DefaultTargetDepth, MaxTargetDepth} {
		b := mustNew(t, target)
		var arrivals []int64
		for seq := int64(0); seq < int64(target); seq++ { // in-order pre-roll
			arrivals = append(arrivals, seq)
		}
		const blocks = 3
		for blk := 0; blk < blocks; blk++ { // reversed blocks
			lo := int64(target) + int64(blk)*(int64(target)+1)
			for seq := lo + int64(target); seq >= lo; seq-- {
				arrivals = append(arrivals, seq)
			}
		}
		total := len(arrivals)

		for _, seq := range arrivals[:target] { // pre-roll, no drains yet
			b.Consume(seq, dataFrame(seq))
		}
		for i := target; i < total; i++ { // one arrival + one drain per tick
			b.Consume(arrivals[i], dataFrame(arrivals[i]))
			f, ok := b.Next()
			if !ok {
				t.Fatalf("target %d tick %d: stall with non-empty buffer", target, i)
			}
			if want := int64(i - target); !framesEqual(f, dataFrame(want)) {
				t.Fatalf("target %d tick %d: got seq %d, want %d",
					target, i, frameSeq(f), want)
			}
		}
		out := drainAll(t, b)
		if len(out) != target {
			t.Fatalf("target %d: %d frames left after ticks, want %d",
				target, len(out), target)
		}
		for i, f := range out {
			if !framesEqual(f, dataFrame(int64(total-target+i))) {
				t.Fatalf("target %d: tail frame %d out of order or corrupt", target, i)
			}
		}
		m := b.Metrics()
		if m.Delivered != uint64(total) || m.PLC != 0 || m.Drops() != 0 {
			t.Fatalf("target %d: reorder within depth caused defects: %+v", target, m)
		}
	}
}

// TestReorderBeyondCatchUp: a frame displaced past the catch-up threshold
// is treated as a burst: the buffer jumps its drain position to
// newest-target+1, discarding buffered frames (burst drops) and skipping
// the never-buffered span in between.
func TestReorderBeyondCatchUp(t *testing.T) {
	b := mustNew(t, DefaultTargetDepth) // catch-up = 9
	b.Consume(0, dataFrame(0))
	b.Consume(10, dataFrame(10)) // span 11 > 9 → jump to 10-3+1 = 8, drop {0}
	b.Consume(8, dataFrame(8))
	b.Consume(9, dataFrame(9))

	m := b.Metrics()
	if m.Burst != 1 || m.Skipped != 7 {
		t.Fatalf("after jump: burst=%d skipped=%d, want 1/7 (seq 1..7 never buffered)", m.Burst, m.Skipped)
	}
	out := drainAll(t, b)
	if len(out) != 3 {
		t.Fatalf("got %d frames, want 3", len(out))
	}
	for i, f := range out {
		if !framesEqual(f, dataFrame(int64(8+i))) {
			t.Fatalf("frame %d: want seq %d", i, 8+i)
		}
	}
	// The stale frame 1 now arrives: older than drain position → late.
	b.Consume(1, dataFrame(1))
	if m = b.Metrics(); m.Late != 1 {
		t.Fatalf("late = %d, want 1", m.Late)
	}
}

func TestDuplicateDropped(t *testing.T) {
	b := mustNew(t, DefaultTargetDepth)
	b.Consume(5, dataFrame(5))
	b.Consume(6, dataFrame(6))
	b.Consume(6, dataFrame(6)) // duplicate while buffered
	b.Consume(5, dataFrame(5)) // stale copy of a still-buffered frame
	m := b.Metrics()
	if m.Duplicates != 2 {
		t.Fatalf("duplicates = %d, want 2", m.Duplicates)
	}
	out := drainAll(t, b)
	if len(out) != 2 {
		t.Fatalf("got %d frames, want 2", len(out))
	}
	// A duplicate of an already-delivered frame is older than the drain
	// position → late drop, not duplicate.
	b.Consume(5, dataFrame(5))
	if m = b.Metrics(); m.Late != 1 || m.Duplicates != 2 {
		t.Fatalf("late=%d duplicates=%d, want 1/2", m.Late, m.Duplicates)
	}
}

func TestLateDiscarded(t *testing.T) {
	b := mustNew(t, DefaultTargetDepth)
	for seq := int64(0); seq < 4; seq++ {
		b.Consume(seq, dataFrame(seq))
	}
	drainAll(t, b)
	b.Consume(2, dataFrame(2)) // long-stale replay
	if m := b.Metrics(); m.Late != 1 || m.Filled != 4 {
		t.Fatalf("metrics %+v: want late=1 filled=4", m)
	}
}

// TestLossPLCFill: interior gaps within the catch-up span are concealed
// exactly once per missing frame with the caller's Concealer output,
// while playback keeps its one-frame-per-tick cadence. (Gaps that push
// the newest frame's span past the catch-up bound are burst-jumped
// instead — see TestReorderBeyondCatchUp and TestIdleRestart.)
func TestLossPLCFill(t *testing.T) {
	for _, tc := range []struct {
		name string
		seqs []int64
	}{
		{"single gaps", []int64{0, 1, 2, 4, 5, 6, 8, 9, 10}},
		{"3-run gap", []int64{3, 4, 5, 9, 10, 11}},
	} {
		b := mustNew(t, DefaultTargetDepth)
		var out [][]int16
		for _, seq := range tc.seqs[:DefaultTargetDepth] { // pre-roll
			b.Consume(seq, dataFrame(seq))
		}
		for i := DefaultTargetDepth; i < len(tc.seqs); i++ { // per-tick arrival + drain
			b.Consume(tc.seqs[i], dataFrame(tc.seqs[i]))
			f, ok := b.Next()
			if !ok {
				t.Fatalf("%s tick %d: stall with non-empty buffer", tc.name, i)
			}
			out = append(out, f)
		}
		out = append(out, drainAll(t, b)...)
		lo, hi := tc.seqs[0], tc.seqs[len(tc.seqs)-1]
		wantPLC := make(map[int64]bool)
		for seq := lo; seq <= hi; seq++ {
			wantPLC[seq] = true
		}
		for _, seq := range tc.seqs {
			delete(wantPLC, seq)
		}
		if len(out) != int(hi-lo+1) {
			t.Fatalf("%s: got %d frames, want %d (one output per seq in [%d,%d])",
				tc.name, len(out), hi-lo+1, lo, hi)
		}
		for i, f := range out {
			seq := lo + int64(i)
			if wantPLC[seq] {
				if !framesEqual(f, plcStub()) {
					t.Fatalf("%s: seq %d: want PLC frame", tc.name, seq)
				}
			} else if !framesEqual(f, dataFrame(seq)) {
				t.Fatalf("%s: seq %d: want real frame", tc.name, seq)
			}
		}
		if m := b.Metrics(); m.PLC != uint64(len(wantPLC)) || m.Drops() != 0 {
			t.Fatalf("%s: metrics %+v, want PLC=%d drops=0", tc.name, m, len(wantPLC))
		}
	}
}

// TestStarvation: an empty buffer reports starvation instead of
// synthesizing audio; PLC only ever fills interior gaps.
func TestStarvation(t *testing.T) {
	b := mustNew(t, DefaultTargetDepth)
	if f, ok := b.Next(); ok || f != nil {
		t.Fatalf("Next on fresh buffer: ok=%v f=%v, want false/nil", ok, f)
	}
	b.Consume(7, dataFrame(7))
	drainAll(t, b)
	if f, ok := b.Next(); ok || f != nil {
		t.Fatalf("Next on drained buffer: ok=%v f=%v, want false/nil", ok, f)
	}
	if m := b.Metrics(); m.PLC != 0 {
		t.Fatalf("starvation produced %d PLC frames, want 0", m.PLC)
	}
}

// TestIdleRestart: after the stream drains, a short idle gap (< catch-up
// span) is PLC-filled; a long one (span > catch-up) is jumped over.
func TestIdleRestart(t *testing.T) {
	b := mustNew(t, DefaultTargetDepth)
	for seq := int64(0); seq < 5; seq++ {
		b.Consume(seq, dataFrame(seq))
	}
	drainAll(t, b)

	// Short gap: seq 5 missing, 6 buffered → interior gap → PLC.
	b.Consume(6, dataFrame(6))
	b.Consume(7, dataFrame(7))
	f1, ok1 := b.Next()
	f2, ok2 := b.Next()
	f3, ok3 := b.Next()
	if !ok1 || !ok2 || !ok3 || !framesEqual(f1, plcStub()) ||
		!framesEqual(f2, dataFrame(6)) || !framesEqual(f3, dataFrame(7)) {
		t.Fatalf("short restart: got %v/%v/%v", ok1, ok2, ok3)
	}

	// Long gap: next drain position is 8; seq 40 has span 33 > 9 and the
	// buffer is starved, so playback resumes at seq 40 itself — the idle
	// span 8..39 is skipped without concealment.
	b.Consume(40, dataFrame(40))
	f4, ok4 := b.Next()
	if !ok4 || !framesEqual(f4, dataFrame(40)) {
		t.Fatalf("long restart: want immediate delivery of seq 40")
	}
	if m := b.Metrics(); m.PLC != 1 || m.Skipped != 32 {
		t.Fatalf("metrics %+v: want PLC=1 (seq 5), Skipped=32 (seq 8..39)", m)
	}
}

func TestConsumeCopiesFrame(t *testing.T) {
	b := mustNew(t, DefaultTargetDepth)
	frame := dataFrame(42)
	b.Consume(42, frame)
	for i := range frame { // caller mutates its buffer afterwards
		frame[i] = -1
	}
	f, ok := b.Next()
	if !ok || !framesEqual(f, dataFrame(42)) {
		t.Fatalf("Consume did not copy: delivered mutated frame")
	}
}

func TestReset(t *testing.T) {
	b := mustNew(t, DefaultTargetDepth)
	b.Consume(0, dataFrame(0))
	b.Consume(1, dataFrame(1))
	b.Reset()
	if m := b.Metrics(); m.Depth != 0 || m.Filled != 0 || m.Delivered != 0 {
		t.Fatalf("Reset left state: %+v", m)
	}
	if _, ok := b.Next(); ok {
		t.Fatalf("Next after Reset: want starvation")
	}
	// A new stream may start at an unrelated (even lower) sequence.
	b.Consume(900, dataFrame(900))
	f, ok := b.Next()
	if !ok || !framesEqual(f, dataFrame(900)) {
		t.Fatalf("post-Reset stream not started cleanly")
	}
}

func TestMetricsArithmetic(t *testing.T) {
	m := Metrics{Filled: 10, Delivered: 6, PLC: 2, Duplicates: 1, Late: 2, Burst: 3, Skipped: 4}
	if m.Drops() != 6 {
		t.Fatalf("Drops() = %d, want 6", m.Drops())
	}
	if m.Outputs() != 8 {
		t.Fatalf("Outputs() = %d, want 8", m.Outputs())
	}
}

// TestExtremeSequences: sequence numbers near int64 boundaries must not
// panic or overflow the catch-up arithmetic. Real-time model: target
// pre-roll, then one arrival (with every 7th frame lost) and one drain
// per tick — each loss is an interior gap concealed exactly once.
func TestExtremeSequences(t *testing.T) {
	for _, base := range []int64{math.MaxInt64 - 60, math.MinInt64 + 100, -5000} {
		b := mustNew(t, DefaultTargetDepth)
		consumed := make(map[string]bool)
		frames := 0
		consume := func(i int64) {
			fr := dataFrame(i)
			fr[3] = int16(uint16(base))
			consumed[key(fr)] = true
			b.Consume(base+i, fr)
			frames++
		}
		for i := int64(0); i < DefaultTargetDepth; i++ { // pre-roll
			consume(i)
		}
		var out [][]int16
		for i := int64(DefaultTargetDepth); i < 50; i++ { // per-tick arrival + drain
			if i%7 != 0 { // i%7==0 && i>0 are lost in transit
				consume(i)
			}
			if f, ok := b.Next(); ok {
				out = append(out, f)
			}
		}
		out = append(out, drainAll(t, b)...)
		real, plc := 0, 0
		for _, f := range out {
			if f[2] == plcMarker && framesEqual(f, plcStub()) {
				plc++
				continue
			}
			if !consumed[key(f)] {
				t.Fatalf("base %d: delivered frame was never consumed", base)
			}
			real++
		}
		if real != frames {
			t.Fatalf("base %d: delivered %d real frames, want %d", base, real, frames)
		}
		// Interior losses (7,14,21,28,35,42 — six of them) are concealed.
		if plc != 6 {
			t.Fatalf("base %d: PLC = %d, want 6", base, plc)
		}
	}
}

func key(f []int16) string {
	b := make([]byte, len(f)*2)
	for i, v := range f {
		b[2*i] = byte(v)
		b[2*i+1] = byte(v >> 8)
	}
	return string(b)
}
