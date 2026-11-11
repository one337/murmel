package engine

import "io.github/murmel/internal/audio/pipeline"

// TransmitMode decides when the microphone is sent.
type TransmitMode int32

const (
	// TransmitVoiceActivation sends while the voice activity detection
	// hears speech above the threshold.
	TransmitVoiceActivation TransmitMode = iota
	// TransmitPushToTalk sends while the push-to-talk key is held, and
	// pttHoldFrames after it is released, whatever the level.
	TransmitPushToTalk
	// TransmitContinuous always sends.
	TransmitContinuous
)

// pttHoldFrames keeps sending 150 ms after push-to-talk is released, so the
// end of the last word is not cut off.
const pttHoldFrames = 15

// gate turns the transmit mode, the VAD verdict and the push-to-talk key
// into the stream decision for one 10 ms frame.
type gate struct {
	open bool
	hold int // frames left after push-to-talk was released
}

func (g *gate) next(mode TransmitMode, vad pipeline.Decision, pressed bool) pipeline.Decision {
	if mode == TransmitVoiceActivation {
		g.open = vad.Voice()
		g.hold = 0
		return vad
	}
	want := true
	if mode == TransmitPushToTalk {
		switch {
		case pressed:
			g.hold = pttHoldFrames
		case g.hold > 0:
			g.hold--
		default:
			want = false
		}
	}
	switch {
	case want && !g.open:
		g.open = true
		return pipeline.Start
	case want:
		return pipeline.Continue
	case g.open:
		g.open = false
		return pipeline.Stop
	}
	return pipeline.Silent
}
