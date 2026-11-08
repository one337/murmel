package jitter

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"testing"
)

// seedFlag selects property-test seeds: "all" (default) runs the fixed
// seed list 0..999; a single non-negative integer runs exactly that seed.
// Reproduce a failing seed with:
//
//	go test ./internal/audio/jitter -run TestProperties -v -seed=42
var seedFlag = flag.String("seed", "all", `property seed: "all" or a single non-negative integer`)

const propertySeedCount = 1000

func TestProperties(t *testing.T) {
	var seeds []uint64
	if *seedFlag == "all" {
		for i := uint64(0); i < propertySeedCount; i++ {
			seeds = append(seeds, i)
		}
	} else {
		s, err := strconv.ParseUint(*seedFlag, 10, 64)
		if err != nil {
			t.Fatalf("bad -seed %q: %v", *seedFlag, err)
		}
		seeds = []uint64{s}
	}
	for _, s := range seeds {
		t.Run(fmt.Sprintf("seed=%d", s), func(t *testing.T) {
			runProperty(t, s)
		})
	}
}

// runProperty drives one seeded random arrival pattern through the
// buffer and checks the task's invariants against observable behavior
// only (no reference reimplementation):
//
//   - no panic
//   - every output is either exactly the consumed frame for its seq or
//     exactly the Concealer's frame (content addressing, full compare)
//   - output sequence numbers strictly increase, and concealment fills
//     exactly on gaps: PLC outputs between two real frames never exceed
//     the count of actually-missing seqs in that span
//   - Next succeeds iff the buffer is non-empty (constant cadence, no
//     stalls while buffered audio exists)
//   - buffer depth never exceeds the catch-up bound (bounded memory)
//   - metrics conserve: consumes = filled+duplicates+late,
//     filled = delivered+burst+depth, outputs = delivered+plc, and the
//     non-output span between real frames ≤ drops+skipped
func runProperty(t *testing.T, seed uint64) {
	rng := rand.New(rand.NewPCG(0xA0B71E5, seed))

	target := MinTargetDepth + rng.IntN(MaxTargetDepth-MinTargetDepth+1)
	catchUp := catchUpMultiplier * target
	n := 200 + rng.IntN(500)
	lossP := 0.05 + rng.Float64()*0.25
	dupP := rng.Float64() * 0.10
	reorderMax := rng.IntN(target + 1)

	var base int64
	if rng.IntN(4) == 0 {
		base = int64(rng.Uint64() >> 40) // < 2^24: encodable in a frame
	}

	// Arrival schedule: every frame gets a random forward displacement
	// (≤ reorderMax, so ≤ target depth and absorbable), except 5% which
	// are displaced far past the catch-up bound (late arrivals). Frames
	// are lost with probability lossP and duplicated with dupP.
	type frame struct{ seq int64 }
	slotted := make([]struct{ pos, idx int }, 0, n)
	for i := 0; i < n; i++ {
		d := rng.IntN(reorderMax + 1)
		if rng.Float64() < 0.05 {
			d += target + rng.IntN(catchUp)
		}
		slotted = append(slotted, struct{ pos, idx int }{i + d, i})
	}
	sort.Slice(slotted, func(a, b int) bool {
		if slotted[a].pos != slotted[b].pos {
			return slotted[a].pos < slotted[b].pos
		}
		return slotted[a].idx < slotted[b].idx
	})
	events := make([]frame, 0, n)
	for _, s := range slotted {
		if rng.Float64() < lossP {
			continue
		}
		seq := base + int64(s.idx)
		events = append(events, frame{seq})
		if rng.Float64() < dupP {
			events = append(events, frame{seq})
		}
	}

	// Playback driver: pre-roll `target` frames, then one drain per tick
	// at the fixed 10 ms cadence, with an arrival clump (burst, 3 arrivals
	// per tick) and an arrival lull (starvation window) mixed in.
	burstStart := n/3 + rng.IntN(n/4)
	const burstTicks = 7
	idleStart := n/2 + rng.IntN(n/4)
	idleTicks := 3 + rng.IntN(8)

	b, err := New(target, testConcealer())
	if err != nil {
		t.Fatalf("New(%d): %v", target, err)
	}

	consumeCalls := 0
	consumed := make(map[int64]bool)
	idx := 0
	feed := func(k int) {
		for ; k > 0 && idx < len(events); k-- {
			ev := events[idx]
			idx++
			b.Consume(ev.seq, dataFrame(ev.seq))
			consumeCalls++
			consumed[ev.seq] = true
		}
	}

	var (
		haveLast      bool
		last          int64
		pendingPLC    uint64 // PLC outputs since the previous real output
		windowJumpSum uint64 // non-output seqs between consecutive reals
		deliveredSeen uint64
		plcSeen       uint64
	)
	record := func(f []int16) {
		if len(f) != FrameSize {
			t.Fatalf("seed %d: output frame len %d, want %d", seed, len(f), FrameSize)
		}
		switch f[2] {
		case realMarker:
			seq := frameSeq(f)
			if !framesEqual(f, dataFrame(seq)) {
				t.Fatalf("seed %d: corrupt real frame (seq field %d)", seed, seq)
			}
			if !consumed[seq] {
				t.Fatalf("seed %d: delivered seq %d was never consumed", seed, seq)
			}
			if haveLast {
				if seq <= last {
					t.Fatalf("seed %d: output not monotonic: seq %d after %d", seed, seq, last)
				}
				gap := uint64(seq - last - 1)
				// Concealment fills exactly on gaps: PLC outputs since the
				// last real frame can never exceed the actually-missing seqs.
				if pendingPLC > gap {
					t.Fatalf("seed %d: %d PLC frames for a %d-seq gap at seq %d",
						seed, pendingPLC, gap, seq)
				}
				windowJumpSum += gap - pendingPLC
			}
			last, haveLast = seq, true
			pendingPLC = 0
			deliveredSeen++
		case plcMarker:
			if !framesEqual(f, plcStub()) {
				t.Fatalf("seed %d: corrupt PLC frame", seed)
			}
			if haveLast {
				pendingPLC++
			}
			plcSeen++
		default:
			t.Fatalf("seed %d: output frame with unknown marker %d", seed, f[2])
		}
	}

	maxDepth := 0
	feed(target)
	tick := 0
	for idx < len(events) || b.Depth() > 0 {
		switch {
		case tick >= idleStart && tick < idleStart+idleTicks:
		case tick >= burstStart && tick < burstStart+burstTicks:
			feed(3)
		default:
			feed(1)
		}
		if d := b.Depth(); d > maxDepth {
			maxDepth = d
		}
		if maxDepth > catchUp {
			t.Fatalf("seed %d: depth %d exceeds catch-up bound %d", seed, maxDepth, catchUp)
		}
		if b.Depth() > 0 {
			f, ok := b.Next()
			if !ok || f == nil {
				t.Fatalf("seed %d tick %d: stall with depth %d", seed, tick, b.Depth())
			}
			record(f)
		} else if f, ok := b.Next(); ok || f != nil {
			t.Fatalf("seed %d tick %d: Next ok=%v with empty buffer", seed, tick, ok)
		}
		tick++
	}

	m := b.Metrics()
	if m.Delivered != deliveredSeen || m.PLC != plcSeen {
		t.Fatalf("seed %d: metrics delivered=%d plc=%d, observed %d/%d",
			seed, m.Delivered, m.PLC, deliveredSeen, plcSeen)
	}
	if m.Outputs() != deliveredSeen+plcSeen {
		t.Fatalf("seed %d: Outputs()=%d, observed %d", seed, m.Outputs(), deliveredSeen+plcSeen)
	}
	if uint64(consumeCalls) != m.Filled+m.Duplicates+m.Late {
		t.Fatalf("seed %d: %d consumes != filled(%d)+dups(%d)+late(%d)",
			seed, consumeCalls, m.Filled, m.Duplicates, m.Late)
	}
	if m.Filled != m.Delivered+m.Burst+uint64(m.Depth) || m.Depth != 0 {
		t.Fatalf("seed %d: filled(%d) != delivered(%d)+burst(%d)+depth(%d)",
			seed, m.Filled, m.Delivered, m.Burst, m.Depth)
	}
	// Non-output seqs between consecutive real outputs can only be frames
	// the metrics already charged as dropped or skipped.
	if windowJumpSum > m.Drops()+m.Skipped {
		t.Fatalf("seed %d: output stream hides %d seqs, metrics only dropped %d + skipped %d",
			seed, windowJumpSum, m.Drops(), m.Skipped)
	}
	if deliveredSeen > uint64(len(consumed)) {
		t.Fatalf("seed %d: delivered %d frames from %d unique consumed",
			seed, deliveredSeen, len(consumed))
	}
}
