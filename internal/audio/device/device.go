package device

import (
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"

	"io.github/murmel/internal/audio/pipeline"
	"io.github/murmel/internal/audio/ring"
)

// Compile-time proof that Device satisfies both halves of the T11
// pipeline contract.
var (
	_ pipeline.Source = (*Device)(nil)
	_ pipeline.Sink   = (*Device)(nil)
)

// ErrDeviceGone is delivered on the Errors channel when the audio device
// stops for any reason other than an explicit Close (unplugged headset,
// killed sound server). Non-fatal: drain remaining audio, re-open.
var ErrDeviceGone = errors.New("device: audio device stopped unexpectedly")

// ErrClosed is returned by Write after Close.
var ErrClosed = errors.New("device: closed")

// Config selects the devices for Open. Nil IDs mean the system default;
// non-nil IDs come from GetDevices and are opaque values the settings
// module (T25) stores verbatim.
type Config struct {
	CaptureID  *malgo.DeviceID
	PlaybackID *malgo.DeviceID
	// PlaybackPeriod is the playback buffer granularity requested from the
	// backend (0 = one 10 ms frame). Some PipeWire sinks (e.g. certain USB
	// headsets via pipewire-pulse) never start a stream that asks for less
	// than ~50 ms; callers raise it when playback stalls.
	PlaybackPeriod time.Duration
	// PlaybackOnly opens no capture stream (event sounds): Read then
	// blocks until Close, and the desktop shows no microphone in use.
	PlaybackOnly bool
}

// Device pairs a capture and a playback malgo stream (see package doc).
// Each stream's data callback runs on its own audio thread and touches
// only its ring and staging buffer; everything else here runs on pipeline
// goroutines.
type Device struct {
	ctx     *malgo.AllocatedContext
	capDev  *malgo.Device
	playDev *malgo.Device

	capRing  *ring.Ring // data callback → Read
	playRing *ring.Ring // Write → data callback

	errCh     chan error
	closing   atomic.Bool   // Close began; suppresses the gone-signal
	done      atomic.Bool   // no more frames will arrive; Read unblocks
	underruns atomic.Uint64 // playback periods filled with silence

	// Audio-thread-only re-chunking state (see data): partially filled
	// capture period, and the remainder of the period being played.
	capStage  [frameBytes]byte
	capFill   int
	playStage [frameBytes]byte
	playLeft  int

	frame   [periodFrames]int16 // Read result buffer, valid until next Read
	capBuf  [frameBytes]byte    // Read-side ring pop scratch
	playBuf [frameBytes]byte    // Write-side ring push staging
}

// readPoll is the Read wake-up granularity. 1 ms on a 10 ms frame
// cadence: bounded latency without any signalling from the callback
// (channel ops lock in the runtime; the audio thread must not lock).
const readPoll = time.Millisecond

// openContext creates the malgo backend context for the given
// preference order (PulseAudio preferred, ALSA fallback). Shared by
// Open and GetDevices.
func openContext(backends []malgo.Backend) (*malgo.AllocatedContext, error) {
	return malgo.InitContext(backends, malgo.ContextConfig{}, nil)
}

// Open initializes and starts the duplex stream. It fails with a clean
// error (wrapped, non-fatal) when no audio backend is available — e.g.
// headless CI containers without a sound server.
func Open(cfg Config) (*Device, error) {
	d := &Device{
		capRing:  ring.New(frameBytes, captureSlots),
		playRing: ring.New(frameBytes, playbackRingSlots(cfg.PlaybackPeriod)),
		errCh:    make(chan error, 1),
	}
	ctx, err := openContext(preferredBackends)
	if err != nil {
		return nil, fmt.Errorf("device: init audio backends (no sound server?): %w", err)
	}
	d.ctx = ctx
	// Two independent streams instead of one duplex stream: miniaudio's
	// PulseAudio duplex mode never invokes the data callback when the
	// server negotiates a different playback period (PipeWire does), which
	// silently disabled both capture and playback.
	streams := streamConfigs(cfg.CaptureID, cfg.PlaybackID, cfg.PlaybackPeriod)
	if !cfg.PlaybackOnly {
		capDev, err := malgo.InitDevice(ctx.Context, streams.capture, malgo.DeviceCallbacks{
			Data: func(_, input []byte, frames uint32) { d.data(nil, input, frames) },
			Stop: d.stopped,
		})
		if err != nil {
			d.release()
			return nil, fmt.Errorf("device: open capture stream (48 kHz mono S16): %w", err)
		}
		d.capDev = capDev
	}
	playDev, err := malgo.InitDevice(ctx.Context, streams.playback, malgo.DeviceCallbacks{
		Data: func(output, _ []byte, frames uint32) { d.data(output, nil, frames) },
		Stop: d.stopped,
	})
	if err != nil {
		d.release()
		return nil, fmt.Errorf("device: open playback stream (48 kHz mono S16): %w", err)
	}
	d.playDev = playDev
	// malgo Start() pulls the first playback period synchronously, so the
	// rings must exist before Start — they do; d was built complete.
	if d.capDev != nil {
		if err := d.capDev.Start(); err != nil {
			d.release()
			return nil, fmt.Errorf("device: start capture stream: %w", err)
		}
	}
	if err := playDev.Start(); err != nil {
		d.release()
		return nil, fmt.Errorf("device: start playback stream: %w", err)
	}
	return d, nil
}

// Read implements pipeline.Source. It blocks until a captured frame is
// available and returns it (valid until the next Read). After Close or
// device loss it first drains the rings, then returns io.EOF.
func (d *Device) Read() ([]int16, error) {
	for {
		if d.capRing.Pop(d.capBuf[:]) {
			for i := range d.frame {
				d.frame[i] = int16(uint16(d.capBuf[2*i]) | uint16(d.capBuf[2*i+1])<<8)
			}
			return d.frame[:], nil
		}
		if d.done.Load() {
			return nil, io.EOF
		}
		time.Sleep(readPoll)
	}
}

// Write implements pipeline.Sink: stages one frame into the playback
// ring and returns immediately; never blocks. Frames shorter than 480
// samples are zero-padded, longer ones truncated, so the ring always
// receives whole periods. Overflow drops the oldest queued frame.
func (d *Device) Write(frame []int16) error {
	if d.done.Load() {
		return ErrClosed
	}
	n := len(frame)
	if n > periodFrames {
		n = periodFrames
	}
	for i := 0; i < periodFrames; i++ {
		s := int16(0)
		if i < n {
			s = frame[i]
		}
		d.playBuf[2*i] = byte(s)
		d.playBuf[2*i+1] = byte(s >> 8)
	}
	d.playRing.Push(d.playBuf[:])
	return nil
}

// PlaybackQueued returns the number of 10 ms frames written but not yet
// taken by the playback stream. Writers that must not lose audio (event
// sounds) pace themselves on it instead of on the wall clock: a stream
// that starts late then delays the audio rather than dropping it.
func (d *Device) PlaybackQueued() int { return d.playRing.Len() }

// Errors returns the device-loss channel: cap 1, ErrDeviceGone at most
// once per device lifetime.
func (d *Device) Errors() <-chan error { return d.errCh }

// CaptureDrops counts captured periods lost because the pipeline read
// slower than real.
func (d *Device) CaptureDrops() uint64 { return d.capRing.Drops() }

// PlaybackDrops counts written periods lost because playback consumed
// slower than the decode stage produced.
func (d *Device) PlaybackDrops() uint64 { return d.playRing.Drops() }

// Underruns counts playback periods the device consumed in silence
// because the ring was empty.
func (d *Device) Underruns() uint64 { return d.underruns.Load() }

// Close stops and releases the device and its context. Idempotent.
// Device loss before or during Close is benign: the gone-signal is
// suppressed and the resources are released regardless.
func (d *Device) Close() error {
	if !d.closing.CompareAndSwap(false, true) {
		return nil
	}
	d.done.Store(true)
	return d.release()
}

// release stops and frees whatever Open managed to create.
func (d *Device) release() error {
	d.closing.Store(true)
	for _, dev := range []*malgo.Device{d.capDev, d.playDev} {
		if dev != nil {
			dev.Stop()
			dev.Uninit()
		}
	}
	d.capDev, d.playDev = nil, nil
	if d.ctx != nil {
		err := d.ctx.Uninit()
		d.ctx.Free()
		d.ctx = nil
		if err != nil {
			return fmt.Errorf("device: uninit context: %w", err)
		}
	}
	return nil
}

// stopped is the malgo stop callback (lifecycle path, not the data
// path): planned stops — Close sets closing first — pass silently;
// unplanned ones unblock Read and surface ErrDeviceGone. The channel
// send is non-blocking (buffered, cap 1).
func (d *Device) stopped() {
	if d.closing.Load() {
		return
	}
	d.done.Store(true)
	select {
	case d.errCh <- ErrDeviceGone:
	default:
	}
}
