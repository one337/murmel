package engine

import (
	"testing"

	"io.github/murmel/internal/audio/pipeline"
)

func TestGateVoiceActivationPassesVAD(t *testing.T) {
	var g gate
	for _, d := range []pipeline.Decision{pipeline.Silent, pipeline.Start, pipeline.Continue, pipeline.Stop} {
		if got := g.next(TransmitVoiceActivation, d, true); got != d {
			t.Errorf("VAD %v → %v, want it unchanged", d, got)
		}
	}
}

func TestGatePushToTalkSendsWhileHeldPlusHold(t *testing.T) {
	var g gate
	// Loud speech without the key: nothing is sent.
	if got := g.next(TransmitPushToTalk, pipeline.Start, false); got != pipeline.Silent {
		t.Fatalf("unpressed = %v, want Silent", got)
	}
	// Pressed, even in silence: start, then continue.
	if got := g.next(TransmitPushToTalk, pipeline.Silent, true); got != pipeline.Start {
		t.Fatalf("press = %v, want Start", got)
	}
	if got := g.next(TransmitPushToTalk, pipeline.Silent, true); got != pipeline.Continue {
		t.Fatalf("held = %v, want Continue", got)
	}
	// Released: pttHoldFrames more frames, then stop.
	for i := 0; i < pttHoldFrames; i++ {
		if got := g.next(TransmitPushToTalk, pipeline.Silent, false); got != pipeline.Continue {
			t.Fatalf("hold frame %d = %v, want Continue", i, got)
		}
	}
	if got := g.next(TransmitPushToTalk, pipeline.Silent, false); got != pipeline.Stop {
		t.Fatalf("after hold = %v, want Stop", got)
	}
	if got := g.next(TransmitPushToTalk, pipeline.Silent, false); got != pipeline.Silent {
		t.Fatalf("idle = %v, want Silent", got)
	}
}

func TestGateContinuousAlwaysSends(t *testing.T) {
	var g gate
	if got := g.next(TransmitContinuous, pipeline.Silent, false); got != pipeline.Start {
		t.Fatalf("first = %v, want Start", got)
	}
	if got := g.next(TransmitContinuous, pipeline.Stop, false); got != pipeline.Continue {
		t.Fatalf("then = %v, want Continue", got)
	}
}
