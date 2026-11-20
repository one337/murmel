package pipeline

import (
	"testing"
	"time"
)

func TestFrameConstants(t *testing.T) {
	if FrameSize != 480 || SampleRate != 48000 {
		t.Fatalf("frame format drifted: %d samples @ %d Hz", FrameSize, SampleRate)
	}
	if FrameDuration != 10*time.Millisecond {
		t.Fatalf("frame duration = %v", FrameDuration)
	}
}
