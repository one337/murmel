package jitter

import "testing"

// TestBurst20AfterIdle is the plan's QA scenario: a 20-frame burst
// arrives after idle with no drains in between (target depth 3, catch-up
// bound 9). Derived expected values:
//
//	Consume 100..108 → spans 1..9, all stored (9 buffered)
//	Consume 109      → span 10 > 9 → jump drain to 109-3+1=107,
//	                   drop 100..106 (7 burst drops)
//	Consume 110..115 → spans 4..9 stored
//	Consume 116      → span 10 > 9 → jump to 114, drop 107..113 (7)
//	Consume 117..119 → spans 4..6 stored → final depth 6
//
// So 14 of the 20 frames are dropped, all of them buffered first
// (Skipped = 0), depth never exceeds the catch-up bound, and the drain
// afterwards delivers 114..119 contiguously with no stalls and no PLC.
func TestBurst20AfterIdle(t *testing.T) {
	b := mustNew(t, DefaultTargetDepth)
	for seq := int64(100); seq < 120; seq++ {
		b.Consume(seq, dataFrame(seq))
		if d := b.Depth(); d > 9 {
			t.Fatalf("depth %d at seq %d exceeds catch-up bound 9", d, seq)
		}
	}
	m := b.Metrics()
	if m.Depth != 6 {
		t.Fatalf("depth after burst = %d, want 6 (2×target bound)", m.Depth)
	}
	if m.Burst != 14 || m.Skipped != 0 || m.Late != 0 || m.Duplicates != 0 {
		t.Fatalf("post-burst metrics %+v: want burst=14 skipped=0 late=0 dup=0", m)
	}
	if m.Filled != 20 || m.Delivered != 0 {
		t.Fatalf("post-burst metrics %+v: want filled=20 delivered=0", m)
	}

	// One drain window: output continues immediately and depth decreases
	// monotonically from 6 toward the target as the drain keeps running.
	first, ok := b.Next()
	if !ok || !framesEqual(first, dataFrame(114)) {
		t.Fatalf("first post-burst drain: want seq 114, got ok=%v", ok)
	}
	if d := b.Depth(); d != 5 {
		t.Fatalf("depth after one drain = %d, want 5", d)
	}

	var got []int64
	for {
		f, ok := b.Next()
		if !ok {
			break
		}
		if !framesEqual(f, dataFrame(frameSeq(f))) {
			t.Fatalf("post-burst drain: corrupt frame")
		}
		got = append(got, frameSeq(f))
	}
	if len(got) != 5 {
		t.Fatalf("drained %d further frames, want 5", len(got))
	}
	for i, seq := range got {
		if seq != 115+int64(i) {
			t.Fatalf("drain seq %d = %d, want %d (contiguous)", i, seq, 115+int64(i))
		}
	}

	m = b.Metrics()
	if m.Delivered != 6 || m.PLC != 0 || m.Depth != 0 {
		t.Fatalf("final metrics %+v: want delivered=6 plc=0 depth=0", m)
	}
	if m.Filled != m.Delivered+m.Burst+uint64(m.Depth) {
		t.Fatalf("conservation violated: %+v", m)
	}
	if _, ok := b.Next(); ok {
		t.Fatalf("Next after full drain: want starvation")
	}
}

// TestBurstSustained200: a sustained burst (200 frames, no drains) never
// grows the buffer past the catch-up bound, ends within [target,
// catch-up], and drains completely with contiguous in-order output.
func TestBurstSustained200(t *testing.T) {
	const lo, hi = int64(5000), int64(5200)
	b := mustNew(t, DefaultTargetDepth)
	for seq := lo; seq < hi; seq++ {
		b.Consume(seq, dataFrame(seq))
		if d := b.Depth(); d > 9 {
			t.Fatalf("depth %d at seq %d exceeds catch-up bound", d, seq)
		}
	}
	m := b.Metrics()
	if m.Depth < DefaultTargetDepth || m.Depth > 9 {
		t.Fatalf("final depth %d outside [%d, 9]", m.Depth, DefaultTargetDepth)
	}

	var got []int64
	for {
		f, ok := b.Next()
		if !ok {
			break
		}
		got = append(got, frameSeq(f))
		if !framesEqual(f, dataFrame(frameSeq(f))) {
			t.Fatalf("drain: corrupt frame at seq %d", frameSeq(f))
		}
	}
	if len(got) != m.Depth {
		t.Fatalf("drained %d frames, want %d", len(got), m.Depth)
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[i-1]+1 {
			t.Fatalf("drain not contiguous: %d after %d", got[i], got[i-1])
		}
	}
	if len(got) > 0 && got[len(got)-1] != hi-1 {
		t.Fatalf("newest frame %d was dropped; want %d buffered", got[len(got)-1], hi-1)
	}
	if mm := b.Metrics(); mm.PLC != 0 {
		t.Fatalf("burst drain used PLC %d times, want 0", mm.PLC)
	}
}

// TestBoundedMemory100k: 100k frames through the buffer — a phase with
// sparse drains and a phase with none — must keep depth ≤ catch-up at
// every step (bounded storage) and conserve metrics at the end.
func TestBoundedMemory100k(t *testing.T) {
	const total = 100_000
	b := mustNew(t, MaxTargetDepth) // worst case: catch-up bound 24
	maxDepth := 0
	delivered := 0
	for seq := int64(0); seq < total; seq++ {
		b.Consume(seq, dataFrame(seq))
		if d := b.Depth(); d > maxDepth {
			maxDepth = d
		}
		if maxDepth > 24 {
			t.Fatalf("depth %d at seq %d exceeds catch-up bound 24", maxDepth, seq)
		}
		if seq%10 == 0 {
			if _, ok := b.Next(); ok {
				delivered++
			}
		}
	}
	m := b.Metrics()
	if m.Filled != total {
		t.Fatalf("filled = %d, want %d", m.Filled, total)
	}
	if m.Filled != m.Delivered+uint64(m.Burst)+uint64(m.Depth) {
		t.Fatalf("conservation violated: %+v", m)
	}
	before := m.Depth
	for {
		if _, ok := b.Next(); !ok {
			break
		}
		delivered++
	}
	if final := b.Metrics(); final.Depth != 0 || final.Filled != total {
		t.Fatalf("final metrics %+v (buffered before drain: %d)", final, before)
	}
	if maxDepth > 24 {
		t.Fatalf("max depth %d over 100k frames, bound is 24", maxDepth)
	}
}
