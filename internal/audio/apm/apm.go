// Package apm wraps the WebRTC audio processing module (the library
// PipeWire's echo-cancel module and browsers use) for Murmel's voice
// path: noise suppression, a high-pass filter and echo cancellation.
// Frames are 10 ms, 48 kHz mono S16. No GTK imports.
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
	"sync"
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
	// EchoCancel removes the played-back audio (ProcessReverse) from the
	// microphone signal: needed with speakers instead of a headset.
	EchoCancel bool
}

// Enabled reports whether cfg does any processing at all.
func (c Config) Enabled() bool { return c.Noise != NoiseOff || c.HighPass || c.EchoCancel }

// Processor is one processing chain with state across frames. Process
// (capture) and ProcessReverse (playback reference) may run on different
// goroutines — the WebRTC module synchronizes them internally — but each
// direction must be fed from one goroutine at a time.
type Processor struct {
	mu sync.RWMutex // guards p against Close; RLock for every call
	p  *C.murmel_apm
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

var errClosed = errors.New("apm: processor closed")

// Configure changes the processing stages; state carries over.
func (pr *Processor) Configure(cfg Config) {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	if pr.p == nil {
		return
	}
	level := C.int(-1)
	if cfg.Noise != NoiseOff {
		level = C.int(min(NoiseVeryHigh, max(NoiseLow, cfg.Noise)) - NoiseLow)
	}
	C.murmel_apm_configure(pr.p, level, boolInt(cfg.HighPass), boolInt(cfg.EchoCancel))
}

// Process processes one 10 ms capture frame in place.
func (pr *Processor) Process(frame []int16) error {
	if len(frame) != pipeline.FrameSize {
		return fmt.Errorf("apm: frame has %d samples, want %d", len(frame), pipeline.FrameSize)
	}
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	if pr.p == nil {
		return errClosed
	}
	if rc := C.murmel_apm_process(pr.p, (*C.int16_t)(unsafe.Pointer(&frame[0]))); rc != 0 {
		return fmt.Errorf("apm: process: error %d", int(rc))
	}
	return nil
}

// ProcessReverse feeds one 10 ms frame of the audio being played back —
// the echo cancellation reference. frame is not modified.
func (pr *Processor) ProcessReverse(frame []int16) error {
	if len(frame) != pipeline.FrameSize {
		return fmt.Errorf("apm: frame has %d samples, want %d", len(frame), pipeline.FrameSize)
	}
	var buf [pipeline.FrameSize]int16
	copy(buf[:], frame)
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	if pr.p == nil {
		return errClosed
	}
	if rc := C.murmel_apm_process_reverse(pr.p, (*C.int16_t)(unsafe.Pointer(&buf[0]))); rc != 0 {
		return fmt.Errorf("apm: process reverse: error %d", int(rc))
	}
	return nil
}

// SetStreamDelay tells the echo canceller roughly how long played audio
// takes to reach the microphone again (output plus input buffering); it
// refines the estimate itself. Call it before Process while echo
// cancellation is on.
func (pr *Processor) SetStreamDelay(ms int) {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	if pr.p != nil {
		C.murmel_apm_set_delay(pr.p, C.int(max(0, ms)))
	}
}

// Close frees the processor. Idempotent; waits for running calls.
func (pr *Processor) Close() {
	pr.mu.Lock()
	defer pr.mu.Unlock()
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
