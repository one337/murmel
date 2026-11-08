package pipeline

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// speechBurst generates a speech-like episode: syllable-shaped noise
// bursts (one-pole lowpassed, 15% attack / 30% release envelope), peak
// RMS `level`, starting after `leadFrames` of room noise at `noiseLevel`,
// with `nFrames` (10 ms each) per syllable and 6-frame gaps between
// syllables.
func speechBurst(rng *rand.Rand, noiseLevel, level float64, leadFrames, syllables int) [][]int16 {
	var frames [][]int16
	lp := 0.0
	syllable := func(target float64, nFrames int) {
		for f := 0; f < nFrames; f++ {
			frame := make([]int16, FrameSize)
			for i := range frame {
				x := rng.Float64()*2 - 1
				lp += 0.18 * (x - lp)
				env := 1.0
				pos := float64(f) / float64(nFrames)
				if pos < 0.15 {
					env = pos / 0.15
				} else if pos > 0.7 {
					env = math.Max(0, (1-pos)/0.3)
				}
				frame[i] = int16(target * env * lp)
			}
			frames = append(frames, frame)
		}
	}
	for i := 0; i < leadFrames; i++ {
		frames = append(frames, noiseFrameAt(rng, noiseLevel))
	}
	for s := 0; s < syllables; s++ {
		stress := 1.0
		if s%3 == 1 {
			stress = 0.8
		}
		syllable(level*stress, 12+s) // 120-... ms per syllable
		for g := 0; g < 6; g++ {     // 60 ms inter-syllable gap
			frames = append(frames, noiseFrameAt(rng, noiseLevel))
		}
	}
	for i := 0; i < 100; i++ {
		frames = append(frames, noiseFrameAt(rng, noiseLevel))
	}
	return frames
}

// TestVADTiming verifies the two exact timing properties from the spec:
// pre-roll ≥ 50 ms of episode audio at Start, and Stop ~300 ms
// (±1 frame) after the speech signal ends.
func TestVADTiming(t *testing.T) {
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(2026))

	lead := 100 // 1 s room-noise lead-in (floor adaptation)
	// 500 ms speech: 4 syllables × (120..150 ms) + gaps. Target amplitude
	// 1000: the one-pole lowpass leaves ≈31% RMS → effective RMS ≈310,
	// ≈10 dB above the open threshold.
	frames := speechBurst(rng, 30, 1000, lead, 4)

	var startIdx, stopIdx = -1, -1
	var lastVoiceIdx int = -1
	for i, f := range frames {
		d := v.Sample(f)
		switch d {
		case Start:
			if startIdx != -1 {
				t.Fatalf("second Start at %d", i)
			}
			startIdx = i
		case Continue:
			lastVoiceIdx = i
		case Stop:
			stopIdx = i
		}
	}
	if startIdx == -1 {
		t.Fatal("no Start on speech burst")
	}

	// --- pre-roll: ≥ 50 ms of episode audio available at Start ---
	preroll := v.PreRoll()
	if len(preroll) != 5 {
		t.Fatalf("pre-roll = %d frames, want 5 (50 ms)", len(preroll))
	}
	preMs := time.Duration(len(preroll)) * FrameDuration
	if preMs < 50*time.Millisecond {
		t.Fatalf("pre-roll %v < 50 ms", preMs)
	}
	// pre-roll + Start frame must cover from the episode's logical start:
	// the oldest pre-roll frame is at/near the first above-threshold
	// frame (the attack ramp spans ≈2 frames)
	firstLoudRMS := RMS(frames[startIdx-4])
	if firstLoudRMS < 50 {
		t.Fatalf("pre-roll does not reach back to episode start: frame %d RMS %.0f",
			startIdx-4, firstLoudRMS)
	}

	// --- open delay: Start is the 5th above-threshold frame (±2 for the
	// attack ramp) ---
	if startIdx < lead+2 || startIdx > lead+6 {
		t.Fatalf("Start at %d, want ≈%d±2 (50 ms open delay)", startIdx, lead+4)
	}

	// --- hangover: Stop 300 ms ± 1 frame after last above-close frame ---
	if stopIdx == -1 {
		t.Fatal("no Stop after burst")
	}
	// last frame whose level is above the close threshold
	_, closeDB := v.Thresholds()
	lastAbove := -1
	for i, f := range frames {
		if dbfs(RMS(f)) >= closeDB {
			lastAbove = i
		}
	}
	wantStop := lastAbove + 30
	if stopIdx < wantStop-1 || stopIdx > wantStop+1 {
		t.Fatalf("Stop at %d, want %d ±1 (300 ms hangover after last voice frame %d)",
			stopIdx, wantStop, lastAbove)
	}
	if stopIdx < lastVoiceIdx {
		t.Fatalf("Stop %d must follow the last Continue %d", stopIdx, lastVoiceIdx)
	}

	t.Logf("timing: start=%d (open delay %d frames), preroll=%d frames (50 ms), last-above=%d, stop=%d (hangover %d frames)",
		startIdx, startIdx-lead, len(preroll), lastAbove, stopIdx, stopIdx-lastAbove)
}
