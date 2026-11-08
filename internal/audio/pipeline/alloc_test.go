package pipeline

import (
	"math/rand"
	"testing"
)

// The hot path (RMS, VAD.Sample, pool/queue handoff) must not allocate:
// per-frame allocation is GC churn that blows the latency budget.

func TestAllocRMS(t *testing.T) {
	frame := noiseFrameAt(rand.New(rand.NewSource(1)), 500)
	allocs := testing.AllocsPerRun(200, func() {
		_ = RMS(frame)
	})
	if allocs != 0 {
		t.Fatalf("RMS allocates %.0f bytes-op per call", allocs)
	}
}

func TestAllocVADSample(t *testing.T) {
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(2))
	frame := noiseFrameAt(rng, 300)
	allocs := testing.AllocsPerRun(200, func() {
		v.Sample(frame)
	})
	if allocs != 0 {
		t.Fatalf("VAD.Sample allocates: %.0f allocs/op", allocs)
	}
}

func TestAllocVADSampleMixedStates(t *testing.T) {
	// cover idle → opening → speaking → idle across the measured run
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(3))
	loud := noiseFrameAt(rng, 300)
	quiet := noiseFrameAt(rng, 30)
	allocs := testing.AllocsPerRun(200, func() {
		for i := 0; i < 60; i++ {
			v.Sample(quiet)
		}
		for i := 0; i < 60; i++ {
			v.Sample(loud)
		}
		for i := 0; i < 60; i++ {
			v.Sample(quiet)
		}
	})
	if allocs != 0 {
		t.Fatalf("VAD.Sample state-machine cycle allocates: %.0f allocs/op", allocs)
	}
}

func TestAllocFrameHandoff(t *testing.T) {
	pool := newFramePool(16)
	q := newQueueRelay(8, pool)
	pcm := noiseFrameAt(rand.New(rand.NewSource(4)), 100)
	var vadFrame *Frame
	allocs := testing.AllocsPerRun(200, func() {
		f := pool.Get()
		copy(f.Data[:], pcm)
		vadFrame = f
		q.Push(f)
		got, ok := q.Pull()
		if !ok {
			t.Fatal("push/pull lost frame")
		}
		pool.Put(got)
	})
	_ = vadFrame
	if allocs != 0 {
		t.Fatalf("frame handoff allocates: %.0f allocs/op", allocs)
	}
}
