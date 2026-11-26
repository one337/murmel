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
		for range 60 {
			v.Sample(quiet)
		}
		for range 60 {
			v.Sample(loud)
		}
		for range 60 {
			v.Sample(quiet)
		}
	})
	if allocs != 0 {
		t.Fatalf("VAD.Sample state-machine cycle allocates: %.0f allocs/op", allocs)
	}
}
