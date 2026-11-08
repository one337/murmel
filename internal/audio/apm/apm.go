// Package apm wraps the WebRTC audio processing module (the library
// PipeWire's echo-cancel module and browsers use) for Murmel's capture
// path: noise suppression and a high-pass filter now, echo cancellation
// (reference input already exposed) later. Frames are 10 ms, 48 kHz mono
// S16, processed in place. No GTK imports.
package apm

/*
#cgo pkg-config: webrtc-audio-processing-2
#cgo CXXFLAGS: -std=c++20
#include <stdlib.h>
#include "shim.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"io.github/murmel/internal/audio/pipeline"
)

// NoiseLevel is the noise suppression strength.
type NoiseLevel int

// Noise suppression levels, matching the noise-suppression setting.
const (
	NoiseOff NoiseLevel = iota
	NoiseLow
	NoiseModerate
	NoiseHigh
	NoiseVeryHigh
)

// Config selects the processing stages.
type Config struct {
	Noise NoiseLevel
	// HighPass removes low-frequency rumble (desk knocks, handling noise).
	HighPass bool
}

// Enabled reports whether cfg does any processing at all.
func (c Config) Enabled() bool { return c.Noise != NoiseOff || c.HighPass }

// Processor is one processing chain. It keeps state across frames, so a
// stream needs its own Processor; it is not safe for concurrent use.
type Processor struct {
	p *C.murmel_apm
}

// New creates a processor with cfg applied.
func New(cfg Config) (*Processor, error) {
	p := C.murmel_apm_new()
	if p == nil {
		return nil, errors.New("apm: cannot create the WebRTC audio processor")
	}
	pr := &Processor{p: p}
	runtime.SetFinalizer(pr, (*Processor).Close)
	pr.Configure(cfg)
	return pr, nil
}

// Configure changes the processing stages; state carries over.
func (pr *Processor) Configure(cfg Config) {
	level := C.int(-1)
	if cfg.Noise != NoiseOff {
		level = C.int(min(NoiseVeryHigh, max(NoiseLow, cfg.Noise)) - NoiseLow)
	}
	C.murmel_apm_configure(pr.p, level, boolInt(cfg.HighPass), 0)
}

// Process processes one 10 ms capture frame in place.
func (pr *Processor) Process(frame []int16) error {
	if len(frame) != pipeline.FrameSize {
		return fmt.Errorf("apm: frame has %d samples, want %d", len(frame), pipeline.FrameSize)
	}
	if rc := C.murmel_apm_process(pr.p, (*C.int16_t)(unsafe.Pointer(&frame[0]))); rc != 0 {
		return fmt.Errorf("apm: process: error %d", int(rc))
	}
	return nil
}

// Close frees the processor. Idempotent.
func (pr *Processor) Close() {
	if pr.p != nil {
		C.murmel_apm_free(pr.p)
		pr.p = nil
		runtime.SetFinalizer(pr, nil)
	}
}

func boolInt(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

// ForLevel is the configuration for a noise-suppression setting value
// (0 off … 4 very high); the high-pass filter runs whenever noise
// suppression does.
func ForLevel(level int) Config {
	n := NoiseLevel(min(int(NoiseVeryHigh), max(0, level)))
	return Config{Noise: n, HighPass: n != NoiseOff}
}
