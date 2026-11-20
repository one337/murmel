package pipeline

import "time"

// Fixed voice format: mono 48 kHz PCM16, 480-sample frames = 10 ms,
// 100 frames/s (matches internal/audio/opus constants).
const (
	SampleRate      = 48000
	FrameSize       = 480
	FrameDuration   = 10 * time.Millisecond
	FramesPerSecond = 100
)
