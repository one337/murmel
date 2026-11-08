package pipeline

import (
	"math"
	"slices"
	"time"
)

// Decision is a VAD verdict for one frame.
type Decision int8

const (
	// Silent: no speech episode open (steady state outside episodes).
	Silent Decision = iota
	// Start: episode opened on this frame. Flush PreRoll first: the
	// episode logically began PreRollFrames frames earlier.
	Start
	// Continue: episode open (speaking or within hangover).
	Continue
	// Stop: episode closed on this frame (hangover expired, subject to
	// MinSpeech).
	Stop
)

func (d Decision) Voice() bool { return d == Start || d == Continue }

func (d Decision) String() string {
	switch d {
	case Silent:
		return "Silent"
	case Start:
		return "Start"
	case Continue:
		return "Continue"
	case Stop:
		return "Stop"
	}
	return "?"
}

// VADConfig parameterizes the energy VAD. Durations are converted to
// whole 10 ms frames (rounded). Zero fields take the defaults below.
type VADConfig struct {
	// OpenMargin is how far above the adaptive noise floor (dB) a frame
	// must sit to open an episode. Default 10 dB.
	OpenMargin float64
	// Hysteresis: the close threshold sits this many dB below the open
	// threshold (open > close). Default 6 dB.
	Hysteresis float64
	// AbsFloor is the lowest open threshold (dBFS), so digitally silent
	// input can never open. Default -50 dBFS.
	AbsFloor float64
	// NoiseWindow is the noise-floor tracking window. Default 3 s.
	NoiseWindow time.Duration
	// NoisePercentile is the percentile of window levels used as the
	// noise floor estimate. Default 0.20.
	NoisePercentile float64
	// OpenDelay (≈50 ms): frames of sustained above-threshold audio
	// required before Start fires; the pre-roll buffer covers it so no
	// speech onset is clipped. Default 50 ms.
	OpenDelay time.Duration
	// CloseDelay (~300 ms): hangover below the close threshold before
	// Stop. Default 300 ms.
	CloseDelay time.Duration
	// MinSpeech: an episode is held open at least this long before Stop
	// fires (suppresses flapping on marginal audio). Default 100 ms.
	MinSpeech time.Duration
	// PreRollFrames is the pre-roll ring capacity in frames
	// (≈50 ms = 5). Default OpenDelay worth of frames.
	PreRollFrames int
	// ThresholdRange is the dB span the user calibration SetThreshold
	// slider sweeps (±Range/2 around OpenMargin). Default 12 dB.
	ThresholdRange float64
}

func (c VADConfig) withDefaults() VADConfig {
	if c.OpenMargin == 0 {
		c.OpenMargin = 10
	}
	if c.Hysteresis == 0 {
		c.Hysteresis = 6
	}
	if c.AbsFloor == 0 {
		c.AbsFloor = -50
	}
	if c.NoiseWindow == 0 {
		c.NoiseWindow = 3 * time.Second
	}
	if c.NoisePercentile == 0 {
		c.NoisePercentile = 0.20
	}
	if c.OpenDelay == 0 {
		c.OpenDelay = 50 * time.Millisecond
	}
	if c.CloseDelay == 0 {
		c.CloseDelay = 300 * time.Millisecond
	}
	if c.MinSpeech == 0 {
		c.MinSpeech = 100 * time.Millisecond
	}
	if c.PreRollFrames == 0 {
		c.PreRollFrames = int((c.OpenDelay + FrameDuration - 1) / FrameDuration)
	}
	if c.ThresholdRange == 0 {
		c.ThresholdRange = 12
	}
	return c
}

// VAD is an energy-based voice activity detector: per-frame RMS in dBFS,
// adaptive noise-floor threshold (running percentile), open/close
// hysteresis, 50 ms open-delay with pre-roll, 300 ms close hangover,
// minimum speech duration. Pure logic, allocation-free per Sample, not
// goroutine-safe (call from one goroutine, e.g. as a pipeline tap).
//
// Semantics mirror Mumble's AudioInput energy VAD (threshold + hold) with
// the delays this project's spec fixes (50 ms / 300 ms). Sustained
// loud non-speech (music at speech level) is a known limitation of energy
// VADs; the upgrade path is a WebRTC-VAD behind the same interface.
type VAD struct {
	cfg VADConfig

	// noise floor estimation
	levelRing  []float64 // dBFS, ring over recent frames
	ringIdx    int
	ringFilled int
	scratch    []float64

	// pre-roll ring (episode lead-in audio for the encoder)
	preroll [][]int16
	preIdx  int
	preN    int

	state      int // 0 idle, 1 opening, 2 speaking
	aboveRun   int // consecutive frames ≥ open threshold (opening state)
	episodeAt  int // frame index where the current episode logically began
	hangover   int // frames below close threshold (speaking state)
	frameIdx   int
	userOffset float64 // dB offset from SetThreshold
	// absolute, when set, replaces the adaptive open threshold with
	// absOpenDB (SetOpenThresholdDB).
	absolute  bool
	absOpenDB float64

	lastLevel float64 // dBFS, for GetLevel
	floorDB   float64
	openDB    float64
	closeDB   float64
}

// NewVAD returns a VAD with the given config (zeros → defaults).
func NewVAD(cfg VADConfig) *VAD {
	cfg = cfg.withDefaults()
	n := int(cfg.NoiseWindow / FrameDuration)
	return &VAD{
		cfg:       cfg,
		levelRing: make([]float64, n),
		scratch:   make([]float64, n),
		preroll:   make([][]int16, cfg.PreRollFrames),
	}
}

func dbfs(rms float64) float64 {
	if rms <= 0 {
		return -120
	}
	return 20 * math.Log10(rms/32768)
}

// Sample ingests one frame and returns the decision for it.
func (v *VAD) Sample(frame []int16) Decision {
	level := dbfs(RMS(frame))
	v.lastLevel = level

	v.levelRing[v.ringIdx] = level
	v.ringIdx = (v.ringIdx + 1) % len(v.levelRing)
	if v.ringFilled < len(v.levelRing) {
		v.ringFilled++
	}
	v.floorDB = v.noiseFloor()
	if v.absolute {
		v.openDB = v.absOpenDB
	} else {
		margin := v.cfg.OpenMargin + v.userOffset
		v.openDB = math.Max(v.floorDB+margin, v.cfg.AbsFloor)
	}
	v.closeDB = v.openDB - v.cfg.Hysteresis

	v.frameIdx++
	switch v.state {
	case 0: // idle
		v.pushPreroll(frame)
		if level >= v.openDB {
			v.state = 1
			v.aboveRun = 1
			v.episodeAt = v.frameIdx - 1
		}
		return Silent
	case 1: // opening
		v.pushPreroll(frame)
		if level >= v.openDB {
			v.aboveRun++
			if v.aboveRun >= v.openDelayFrames() {
				v.state = 2
				v.hangover = 0
				return Start
			}
			return Silent
		}
		v.state = 0 // transient (click/keystroke): rejected
		v.aboveRun = 0
		return Silent
	}

	// speaking
	if level >= v.closeDB {
		v.hangover = 0
	} else {
		v.hangover++
	}
	if v.hangover >= v.closeDelayFrames() {
		if v.frameIdx-v.episodeAt < v.minSpeechFrames() {
			return Continue // MinSpeech hold: Stop deferred
		}
		v.state = 0
		v.aboveRun = 0
		return Stop
	}
	return Continue
}

func (v *VAD) pushPreroll(frame []int16) {
	slot := v.preroll[v.preIdx]
	if slot == nil {
		slot = make([]int16, FrameSize)
		v.preroll[v.preIdx] = slot
	}
	copy(slot, frame)
	v.preIdx = (v.preIdx + 1) % len(v.preroll)
	if v.preN < len(v.preroll) {
		v.preN++
	}
}

func (v *VAD) noiseFloor() float64 {
	n := v.ringFilled
	if n == 0 {
		return v.cfg.AbsFloor - v.cfg.OpenMargin
	}
	copy(v.scratch[:n], v.levelRing[:n])
	slices.Sort(v.scratch[:n])
	idx := int(v.cfg.NoisePercentile * float64(n-1))
	return v.scratch[idx]
}

func (v *VAD) openDelayFrames() int {
	return max(1, int((v.cfg.OpenDelay+FrameDuration-1)/FrameDuration))
}

func (v *VAD) closeDelayFrames() int {
	return max(1, int((v.cfg.CloseDelay+FrameDuration-1)/FrameDuration))
}

func (v *VAD) minSpeechFrames() int {
	return max(1, int((v.cfg.MinSpeech+FrameDuration-1)/FrameDuration))
}

// PreRoll returns the buffered episode lead-in frames, oldest first, so
// the consumer can transmit ≥OpenDelay worth of audio before the Start
// frame. The returned slices alias VAD-owned buffers valid until the next
// Sample call.
func (v *VAD) PreRoll() [][]int16 {
	out := make([][]int16, v.preN)
	start := (v.preIdx - v.preN + len(v.preroll)) % len(v.preroll)
	for i := 0; i < v.preN; i++ {
		out[i] = v.preroll[(start+i)%len(v.preroll)]
	}
	return out
}

// PreRollFrames returns how many frames the pre-roll ring currently holds.
func (v *VAD) PreRollFrames() int { return v.preN }

// SetThreshold applies user calibration in [0,1]: 0.5 is neutral, each
// extreme shifts the open threshold by ±ThresholdRange/2 dB. Values are
// clamped.
// SetOpenThresholdDB switches the VAD to an absolute threshold: frames
// at or above db (dBFS RMS) open an episode, regardless of the noise
// floor. This is what the user's threshold slider sets — the line drawn
// on the level meter is exactly where transmission starts.
func (v *VAD) SetOpenThresholdDB(db float64) {
	v.absolute = true
	v.absOpenDB = db
}

func (v *VAD) SetThreshold(x float64) {
	x = math.Min(1, math.Max(0, x))
	v.userOffset = (x - 0.5) * v.cfg.ThresholdRange
}

// GetLevel returns the last frame's level in dBFS (for the UI meter).
func (v *VAD) GetLevel() float64 { return v.lastLevel }

// NoiseFloor returns the current noise-floor estimate in dBFS.
func (v *VAD) NoiseFloor() float64 { return v.floorDB }

// Thresholds returns the current open and close thresholds in dBFS.
func (v *VAD) Thresholds() (openDB, closeDB float64) { return v.openDB, v.closeDB }
