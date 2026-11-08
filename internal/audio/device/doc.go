// Package device implements the pipeline Source and Sink contracts on
// top of malgo (Go bindings for miniaudio): a capture and a playback
// stream (not one duplex stream — miniaudio's PulseAudio duplex mode
// stalls under PipeWire), mono 48 kHz PCM16, 480-frame (10 ms) periods,
// LowLatency performance profile. Callback buffer sizes are whatever the
// backend delivers; the callback re-chunks them into 10 ms periods.
//
// # Backend selection
//
// PulseAudio is preferred (PipeWire serves the same native protocol via
// pipewire-pulse, transparently); ALSA is the fallback. Headless hosts
// (CI containers without a sound server or audio libraries) get clean
// Go errors from Open and GetDevices — never a panic, cgo abort or
// hang.
//
// # Callback discipline
//
// The malgo data callback (callback.go) is the only code that runs on
// the audio thread, and its body is strictly memcpy between the device
// buffers and the pre-allocated SPSC rings from internal/audio/ring,
// plus one atomic counter for underflow statistics: no locks, no
// allocations, no logging, no blocking (keep it that way when editing
// callback.go). Capture
// overflow drops the oldest frame; playback underflow emits silence.
//
// # Failure handling
//
// Device loss (unplugged headset, dead sound server) surfaces as
// ErrDeviceGone on the Errors channel and io.EOF from Read once the
// rings are drained, so callers can re-open with fresh device IDs. It
// is deliberately non-fatal: the connection, pipeline and UI survive.
//
// # Waiting strategy
//
// Read polls its ring at 1 ms granularity instead of having the
// callback signal a channel: channel operations take locks inside the
// runtime and are therefore banned from the audio thread. Sub-
// millisecond average wake-up latency on a 10 ms frame cadence is well
// inside the budget the jitter buffer (T10) absorbs.
package device
