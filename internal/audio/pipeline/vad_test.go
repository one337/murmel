package pipeline

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// noiseFrameAt returns a deterministic white-noise frame with RMS ≈ level.
func noiseFrameAt(rng *rand.Rand, level float64) []int16 {
	f := make([]int16, FrameSize)
	a := level * math.Sqrt(3)
	for i := range f {
		f[i] = int16(rng.Float64()*2*a - a)
	}
	return f
}

// feed feeds n frames of the given generator into v, returning decisions.
func feed(v *VAD, rng *rand.Rand, level float64, n int) []Decision {
	out := make([]Decision, n)
	for i := range out {
		out[i] = v.Sample(noiseFrameAt(rng, level))
	}
	return out
}

func TestVADNoiseFloorAdapts(t *testing.T) {
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(1))
	feed(v, rng, 30, 300) // 3 s of noise at RMS 30
	floor := v.NoiseFloor()
	want := dbfs(30)
	if floor < want-1 || floor > want+1 {
		t.Fatalf("noise floor = %.1f dBFS, want ≈%.1f", floor, want)
	}

	// sustained louder interference raises the floor (running percentile)
	feed(v, rng, 300, 300)
	floor = v.NoiseFloor()
	want = dbfs(300)
	if floor < want-1 || floor > want+1 {
		t.Fatalf("floor did not adapt: %.1f dBFS, want ≈%.1f", floor, want)
	}
}

func TestVADThresholdsHysteresis(t *testing.T) {
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(2))
	feed(v, rng, 30, 100)
	open, close := v.Thresholds()
	floor := v.NoiseFloor()
	// open = max(floor+10, AbsFloor −50); floor(30) ≈ −61 so AbsFloor binds
	wantOpen := math.Max(floor+10, -50)
	if open < wantOpen-0.1 || open > wantOpen+0.1 {
		t.Fatalf("open = %.1f, want %.1f (floor+10 clamped at AbsFloor)", open, wantOpen)
	}
	if close != open-6 {
		t.Fatalf("close = %.1f, want open-6 (=%.1f)", close, open-6)
	}
	if open <= close {
		t.Fatal("open threshold must exceed close threshold")
	}
}

func TestVADAbsFloorGuardsSilence(t *testing.T) {
	v := NewVAD(VADConfig{})
	// digitally near-silent input: floor plunges, open threshold must
	// clamp at AbsFloor and never trigger
	for i := 0; i < 500; i++ {
		f := make([]int16, FrameSize)
		f[0] = 1
		if d := v.Sample(f); d != Silent {
			t.Fatalf("near-digital-silence triggered %v at frame %d", d, i)
		}
	}
	open, _ := v.Thresholds()
	if open < -50.01 {
		t.Fatalf("open threshold %.2f below AbsFloor", open)
	}
}

func TestVADOpenDelayAndStart(t *testing.T) {
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(4))
	feed(v, rng, 30, 100) // warm up floor

	// 20 loud frames: Start must fire exactly on the 5th consecutive
	// above-threshold frame (50 ms open delay)
	var startIdx int = -1
	for i := 0; i < 20; i++ {
		d := v.Sample(noiseFrameAt(rng, 300))
		if d == Start {
			startIdx = i
			break
		}
		if d != Silent {
			t.Fatalf("decision before open-delay confirmation = %v at %d", d, i)
		}
	}
	if startIdx != 4 {
		t.Fatalf("Start at frame %d, want 4 (5th above-threshold frame)", startIdx)
	}
}

func TestVADTransientRejected(t *testing.T) {
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(5))
	feed(v, rng, 30, 100)
	for i := 0; i < 200; i++ {
		d := Silent
		if i%7 == 0 { // isolated 1-frame spikes (keystrokes)
			d = v.Sample(noiseFrameAt(rng, 3000))
		} else {
			d = v.Sample(noiseFrameAt(rng, 30))
		}
		if d == Start || d == Continue {
			t.Fatalf("transient opened episode: %v at frame %d", d, i)
		}
	}
}

func TestVADPreRollBuffer(t *testing.T) {
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(6))
	feed(v, rng, 30, 100)

	var preroll [][]int16
	var startFrame []int16
	for i := 0; i < 20; i++ {
		f := noiseFrameAt(rng, 300)
		d := v.Sample(f)
		if d == Start {
			startFrame = append([]int16{}, f...)
			preroll = v.PreRoll()
			break
		}
	}
	if preroll == nil {
		t.Fatal("no Start within loud burst")
	}
	if len(preroll) != 5 {
		t.Fatalf("pre-roll frames = %d, want 5", len(preroll))
	}
	for i, f := range preroll {
		if len(f) != FrameSize {
			t.Fatalf("pre-roll frame %d len = %d", i, len(f))
		}
	}
	// 5 pre-roll frames + the Start frame = 60 ms of episode audio
	total := (len(preroll) + 1) * 10
	if total < 50 {
		t.Fatalf("pre-roll coverage %d ms < 50 ms", total)
	}
	// pre-roll must end exactly at the Start frame's audio
	last := preroll[len(preroll)-1]
	if RMS(last) < 200 {
		t.Fatalf("pre-roll tail RMS %.0f: episode lead-in missing", RMS(last))
	}
	_ = startFrame
}

func TestVADPreRollRingIsVADOwned(t *testing.T) {
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(60))
	feed(v, rng, 30, 100)
	f1 := noiseFrameAt(rng, 300)
	f1Copy := append([]int16{}, f1...)
	for i := 0; ; i++ {
		d := v.Sample(f1)
		if d == Start {
			pr := v.PreRoll()
			if len(pr) == 0 || pr[len(pr)-1][0] != f1Copy[0] {
				t.Fatal("pre-roll does not end at current frame")
			}
			break
		}
		if i > 10 {
			t.Fatal("no Start")
		}
	}
}

func TestVADHangover(t *testing.T) {
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(7))
	feed(v, rng, 30, 100)

	// 600 ms speech then silence; Stop must fire exactly 30 frames after
	// the last above-close frame
	var stopIdx, lastLoudIdx int = -1, -1
	var seenStart bool
	for i := 0; i < 200; i++ {
		level := 30.0
		if i < 60 {
			level = 300
			lastLoudIdx = i
		}
		d := v.Sample(noiseFrameAt(rng, level))
		if d == Start {
			seenStart = true
		}
		if seenStart && d == Stop {
			stopIdx = i
			break
		}
	}
	if !seenStart {
		t.Fatal("no Start")
	}
	if stopIdx == -1 {
		t.Fatal("no Stop after speech ended")
	}
	if stopIdx != lastLoudIdx+30 {
		t.Fatalf("Stop at %d, want last-loud %d + 30 hangover frames", stopIdx, lastLoudIdx)
	}
}

func TestVADMinSpeechHold(t *testing.T) {
	v := NewVAD(VADConfig{MinSpeech: 1200 * time.Millisecond})
	rng := rand.New(rand.NewSource(8))
	feed(v, rng, 30, 100)

	// 150 ms burst: burst + 300 ms hangover = 450 ms < MinSpeech, so Stop
	// is deferred to the 1200 ms mark (elapsed 120 frames → i = 169)
	var stopIdx int = -1
	var seenStart bool
	for i := 0; i < 200; i++ {
		level := 30.0
		if i >= 50 && i < 65 {
			level = 300
		}
		d := v.Sample(noiseFrameAt(rng, level))
		if d == Start {
			seenStart = true
		}
		if seenStart && i >= 95 && i < 169 && d != Continue {
			t.Fatalf("episode closed early at %d (%v), MinSpeech must hold", i, d)
		}
		if seenStart && d == Stop {
			stopIdx = i
			break
		}
	}
	if !seenStart {
		t.Fatal("no Start")
	}
	// episode began at frame 50 (elapsed 1); Stop once elapsed hits 120
	if stopIdx != 169 {
		t.Fatalf("Stop at %d, want 169 (MinSpeech hold)", stopIdx)
	}
}

func TestVADSetThresholdCalibration(t *testing.T) {
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(9))
	feed(v, rng, 30, 100)

	// thresholds are per-frame quantities: Sample once after calibration
	// so openDB reflects the new margin
	v.SetThreshold(1.0)
	v.Sample(noiseFrameAt(rng, 30))
	highOpen, _ := v.Thresholds()
	floor := v.NoiseFloor()
	if highOpen < floor+15.9 || highOpen > floor+16.1 {
		t.Fatalf("SetThreshold(1.0): open %.1f, want floor+16 (=%.1f)", highOpen, floor+16)
	}

	v.SetThreshold(0.0)
	v.Sample(noiseFrameAt(rng, 30))
	lowOpen, _ := v.Thresholds()
	// floor+4 ≈ −57 sits below AbsFloor −50, so the clamp binds
	if lowOpen != -50 {
		t.Fatalf("SetThreshold(0.0): open %.1f, want AbsFloor clamp at −50", lowOpen)
	}

	v.SetThreshold(5) // clamped
	v.Sample(noiseFrameAt(rng, 30))
	if o, _ := v.Thresholds(); o != highOpen {
		t.Fatal("SetThreshold not clamped to [0,1]")
	}

	// soft speech that opens at neutral calibration must stay closed
	// with the threshold raised
	v2 := NewVAD(VADConfig{})
	v2.SetThreshold(1.0)
	rng2 := rand.New(rand.NewSource(10))
	feed(v2, rng2, 30, 100)
	for i := 0; i < 100; i++ {
		d := v2.Sample(noiseFrameAt(rng2, 100)) // +10.5 dB over floor
		if d == Start || d == Continue {
			t.Fatalf("raised threshold still opens on soft audio: %v", d)
		}
	}
}

func TestVADGetLevelMeter(t *testing.T) {
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(11))
	f := noiseFrameAt(rng, 1000)
	v.Sample(f)
	want := dbfs(1000)
	if v.GetLevel() < want-0.5 || v.GetLevel() > want+0.5 {
		t.Fatalf("GetLevel = %.2f, want ≈%.2f", v.GetLevel(), want)
	}
	v.Sample(make([]int16, FrameSize))
	if v.GetLevel() > -100 {
		t.Fatalf("GetLevel for digital zero = %.2f, want ≤ -100 dBFS", v.GetLevel())
	}
}

func TestVADContinuousSpeechStaysOpen(t *testing.T) {
	v := NewVAD(VADConfig{})
	rng := rand.New(rand.NewSource(12))
	feed(v, rng, 30, 100)
	decisions := feed(v, rng, 300, 200) // 2 s of continuous speech
	starts, continues := 0, 0
	for _, d := range decisions {
		switch d {
		case Start:
			starts++
		case Continue:
			continues++
		case Stop:
			t.Fatal("continuous above-threshold speech must not Stop")
		}
	}
	if starts != 1 {
		t.Fatalf("Start fired %d times on continuous speech", starts)
	}
	if continues < 190 {
		t.Fatalf("Continue decisions = %d, want ≈195", continues)
	}
}

func TestVADAbsoluteThreshold(t *testing.T) {
	v := NewVAD(VADConfig{})
	v.SetOpenThresholdDB(-30)
	quiet := make([]int16, FrameSize) // about -40 dBFS
	loud := make([]int16, FrameSize)  // about -20 dBFS
	for i := range quiet {
		sign := int16(1)
		if i%2 == 1 {
			sign = -1
		}
		quiet[i] = sign * 328
		loud[i] = sign * 3277
	}
	for i := 0; i < 50; i++ {
		if d := v.Sample(quiet); d != Silent {
			t.Fatalf("frame %d: quiet input below the absolute threshold gave %v", i, d)
		}
	}
	if open, closeDB := v.Thresholds(); open != -30 || closeDB != -36 {
		t.Fatalf("thresholds = %.1f/%.1f, want -30/-36", open, closeDB)
	}
	started := false
	for i := 0; i < 10; i++ {
		if v.Sample(loud) == Start {
			started = true
		}
	}
	if !started {
		t.Fatal("loud input above the absolute threshold did not start an episode")
	}
}

func TestThresholdMapping(t *testing.T) {
	for _, tt := range []struct{ x, db float64 }{{0, -60}, {0.5, -30}, {1, 0}, {-1, -60}, {2, 0}} {
		if got := ThresholdDB(tt.x); got != tt.db {
			t.Errorf("ThresholdDB(%v) = %v, want %v", tt.x, got, tt.db)
		}
	}
	if got := LevelFraction(-45); got != 0.25 {
		t.Errorf("LevelFraction(-45) = %v, want 0.25", got)
	}
}
