// Package engine wires the audio pipeline components (T11–T12) with the
// gumble transport (T9) and event bus (T14) into the voice end-to-end path:
//
//   - Capture: malgo device → energy VAD → gumble AudioOutgoing channel
//     (gumble encodes Opus and routes via UDP/TCP)
//   - Playback: gumble AudioListener (decoded PCM) → per-user gain → malgo
//     device Write
//   - Bus: emits VoiceActivity for self; subscribes to UserVolumeChanged for
//     per-user gain; reads LatencyStats from UDP ping stats
//
// No GTK imports: layer boundary preserved.
package engine

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"io.github/murmel/internal/audio/apm"
	"io.github/murmel/internal/audio/device"
	"io.github/murmel/internal/audio/opus"
	"io.github/murmel/internal/audio/pipeline"
	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/protocol/gumble"
)

// levelEveryFrames is the InputLevel cadence: one event per 3 frames
// (30 ms), about 33 per second — smooth for the meter, light on the bus.
const levelEveryFrames = 3

// IO is the audio endpoint an Engine captures from and plays to.
// *device.Device implements it; tests substitute synthetic sources.
type IO interface {
	// Read blocks for the next captured 10 ms frame. The slice may be
	// reused by the next Read.
	Read() ([]int16, error)
	Write(frame []int16) error
	Errors() <-chan error
	Close() error
}

// Options selects the devices and the VAD calibration for an Engine.
type Options struct {
	// CaptureDevice and PlaybackDevice are device names as listed by
	// device.GetDevices; "" (or a name that is not present) selects the
	// system default.
	CaptureDevice  string
	PlaybackDevice string
	// Threshold is the activation threshold as a position on the level
	// meter in [0,1], see pipeline.ThresholdDB: speech at or above it is
	// transmitted.
	Threshold float64
	// JitterFrames is the receive jitter buffer in 10 ms frames (0 keeps
	// the default of 3).
	JitterFrames int
	// PacketFrames is the outgoing audio per packet in 10 ms frames: 1, 2,
	// 4 or 6 (0 or another value selects 2, i.e. 20 ms).
	PacketFrames int
	// NoiseSuppression is the noise-suppression setting (0 off … 4 very
	// high, see apm.ForLevel), applied to the microphone before voice
	// detection and encoding.
	NoiseSuppression int
	// Bitrate is the wanted Opus bitrate in bit/s (0 = opus.BitrateDefault).
	// The server's bandwidth limit may lower it, see EffectiveBitrate.
	Bitrate int
	// Open overrides how the audio endpoint is opened (tests, QA tools);
	// nil opens the named system devices. playbackPeriod is the requested
	// playback buffer granularity (see device.Config.PlaybackPeriod).
	Open func(capture, playback string, playbackPeriod time.Duration) (IO, error)
}

// playbackPeriods is the escalation ladder for the playback buffer: start
// at the smallest safe size; when the output device stalls anyway, reopen
// with the next size.
//
// The ladder must not start lower. Through pipewire-pulse a 10 ms buffer
// requests a 256-sample graph quantum (40 ms still rounds down to 256),
// and some USB headsets (measured: EPOS GSA 70) cannot run at that: the
// ALSA device xruns into a permanent "broken pipe" state that silences
// every application on it until the device is reopened — a larger buffer
// afterwards no longer helps. 50 ms requests 600 samples, i.e. a 512
// quantum (~10.7 ms per period), which those devices handle.
var playbackPeriods = []time.Duration{
	50 * time.Millisecond,
	100 * time.Millisecond,
	200 * time.Millisecond,
}

// Engine owns the audio I/O for one server connection.
//
// Start opens the audio device and begins the capture and playback goroutines.
// Stop tears them down cleanly. Engine is not reusable: create a new one for
// each connection.
type Engine struct {
	client *gumble.Client
	b      *bus.Bus
	self   uint32

	ctx context.Context

	devMu sync.RWMutex
	dev   IO
	open  func(capture, playback string, playbackPeriod time.Duration) (IO, error)
	// playStep indexes playbackPeriods for the current output device.
	playStep int
	capName  string
	outName  string

	threshold    atomic.Uint64 // math.Float64bits of the VAD calibration
	packetFrames atomic.Int32  // outgoing 10 ms frames per packet
	bitrate      atomic.Int64  // wanted Opus bitrate in bit/s
	noise        atomic.Int32  // noise-suppression setting

	enc *opus.Encoder

	gainMu sync.RWMutex
	gains  map[uint32]float64 // per-user local volume (1.0 = 100%)

	mix *mixer
}

// New creates a voice engine for the given live gumble client. self is the
// local user's session ID (for VoiceActivity events). The engine registers
// itself as an AudioListener with cfg.AudioListeners so incoming voice
// streams are routed here.
func New(client *gumble.Client, b *bus.Bus, self uint32, opts Options) (*Engine, error) {
	enc, err := opus.NewEncoder()
	if err != nil {
		return nil, err
	}

	// Wire the encoder into the gumble client so AudioOutgoing channel
	// traffic is encoded by this exact encoder instance.
	client.AudioEncoder = enc

	e := &Engine{
		client:  client,
		b:       b,
		self:    self,
		enc:     enc,
		gains:   make(map[uint32]float64),
		mix:     newMixer(),
		capName: opts.CaptureDevice,
		outName: opts.PlaybackDevice,
		open:    opts.Open,
	}
	if e.open == nil {
		e.open = func(capture, playback string, period time.Duration) (IO, error) {
			cfg := device.Resolve(capture, playback)
			cfg.PlaybackPeriod = period
			return device.Open(cfg)
		}
	}
	e.SetThreshold(opts.Threshold)
	e.SetPacketFrames(opts.PacketFrames)
	e.SetBitrate(opts.Bitrate)
	e.SetNoiseSuppression(opts.NoiseSuppression)
	if opts.JitterFrames > 0 {
		e.SetJitterFrames(opts.JitterFrames)
	}

	client.Config.AudioListeners.Attach(e)
	return e, nil
}

// Start opens the audio device and begins streaming. ctx cancellation stops
// the engine. If the audio device cannot be opened (headless environment),
// Start logs the error and returns nil — voice is then silently unavailable.
func (e *Engine) Start(ctx context.Context) {
	e.ctx = ctx
	e.devMu.Lock()
	e.openLocked()
	e.devMu.Unlock()
	go e.mixLoop(ctx)
	bus.SubscribeCtx(ctx, e.b, func(ev bus.UserVolumeChanged) {
		e.gainMu.Lock()
		e.gains[ev.Session] = ev.Volume
		e.gainMu.Unlock()
	})
}

// openLocked opens the configured devices and starts a capture loop bound
// to that device. e.devMu must be held for writing.
func (e *Engine) openLocked() {
	dev, err := e.open(e.capName, e.outName, playbackPeriods[e.playStep])
	if err != nil {
		slog.Warn("voice engine: audio device unavailable", "err", err)
		return
	}
	e.dev = dev
	go e.captureLoop(e.ctx, dev)
	go e.errLoop(e.ctx, dev)
}

// SetDevices switches capture and playback to the named devices ("" =
// system default) by reopening the duplex stream. Safe from any goroutine;
// a no-op before Start or when nothing changed.
func (e *Engine) SetDevices(capture, playback string) {
	e.devMu.Lock()
	defer e.devMu.Unlock()
	if capture == e.capName && playback == e.outName {
		return
	}
	if playback != e.outName {
		e.playStep = 0 // new output device: try the lowest latency again
	}
	e.capName, e.outName = capture, playback
	e.reopenLocked()
}

// reopenLocked closes and reopens the audio endpoint with the current
// settings. e.devMu must be held for writing.
func (e *Engine) reopenLocked() {
	if e.ctx == nil || e.ctx.Err() != nil {
		return
	}
	if e.dev != nil {
		// The old capture loop ends on the closed device's Read error
		// and closes its own outgoing stream.
		e.dev.Close()
		e.dev = nil
	}
	e.openLocked()
}

// escalatePlayback reacts to a stalled output: reopen with the next larger
// playback buffer, or report the problem once the ladder is exhausted.
// Runs on its own goroutine (reopening blocks briefly).
func (e *Engine) escalatePlayback() {
	e.devMu.Lock()
	if e.playStep+1 >= len(playbackPeriods) {
		e.devMu.Unlock()
		slog.Warn("voice engine: output device is not playing audio", "device", e.outputName())
		e.b.Emit(bus.AudioDeviceProblem{Output: true})
		return
	}
	e.playStep++
	period := playbackPeriods[e.playStep]
	e.reopenLocked()
	e.devMu.Unlock()
	slog.Info("voice engine: output stalled, reopened with a larger buffer",
		"device", e.outputName(), "buffer", period)
}

// SetThreshold updates the activation threshold (meter position in [0,1],
// see Options.Threshold); the capture loop picks it up with the next
// frame. Safe from any goroutine.
func (e *Engine) SetThreshold(x float64) {
	e.threshold.Store(math.Float64bits(x))
}

// SetJitterFrames sets the receive jitter buffer in 10 ms frames. Speakers
// pick it up when they next start buffering. Safe from any goroutine.
func (e *Engine) SetJitterFrames(n int) { e.mix.setPrebuffer(n) }

// SetPacketFrames sets the outgoing audio per packet in 10 ms frames (see
// Options.PacketFrames). It applies from the next transmission on. Safe
// from any goroutine.
func (e *Engine) SetPacketFrames(n int) {
	e.packetFrames.Store(int32(validPacketFrames(n)))
}

// validPacketFrames maps n onto the packet lengths Opus and Mumble accept
// (10, 20, 40, 60 ms), defaulting to 20 ms.
func validPacketFrames(n int) int {
	switch n {
	case 1, 2, 4, 6:
		return n
	}
	return 2
}

// SetNoiseSuppression changes the noise suppression (0 off … 4 very high)
// from the next captured frame on. Safe from any goroutine.
func (e *Engine) SetNoiseSuppression(level int) { e.noise.Store(int32(level)) }

// SetBitrate sets the wanted Opus bitrate in bit/s (0 = default). It
// applies from the next transmission on. Safe from any goroutine.
func (e *Engine) SetBitrate(bitsPerSecond int) {
	if bitsPerSecond <= 0 {
		bitsPerSecond = opus.BitrateDefault
	}
	e.bitrate.Store(int64(bitsPerSecond))
}

// applyBitrate configures encoder and packet budget for the next
// transmission; applied is the bitrate in effect so far (0 = none).
func (e *Engine) applyBitrate(applied, packetFrames int) int {
	want := int(e.bitrate.Load())
	server := e.client.MaxBandwidth()
	b := EffectiveBitrate(want, packetFrames, server)
	if b == applied {
		return applied
	}
	if err := e.enc.SetBitrate(b); err != nil {
		slog.Warn("voice: cannot set bitrate", "bitrate", b, "err", err)
		return applied
	}
	e.client.SetAudioBitrate(b)
	if b < want {
		slog.Info("voice: bitrate limited by the server", "wanted", want, "bitrate", b,
			"server_max_bandwidth", server)
	}
	return b
}

// Stop closes the audio device.
func (e *Engine) Stop() {
	e.devMu.Lock()
	defer e.devMu.Unlock()
	if e.dev != nil {
		e.dev.Close()
		e.dev = nil
	}
	e.ctx = nil
}

// --- Outgoing (capture → VAD → AudioOutgoing) ------------------------------

func (e *Engine) captureLoop(ctx context.Context, dev IO) {
	vad := pipeline.NewVAD(pipeline.VADConfig{})
	appliedThreshold := math.NaN()

	// Input processing (noise suppression). Without the WebRTC module
	// voice still works, just unprocessed.
	appliedNoise := int32(-1)
	proc, err := apm.New(apm.Config{})
	if err != nil {
		slog.Warn("voice: input processing unavailable", "err", err)
	} else {
		defer proc.Close()
	}
	var (
		outCh   chan<- gumble.AudioBuffer
		pack    packer
		bitrate int // in effect for the current/last transmission
	)
	send := func(frame []int16) {
		if p := pack.add(frame); p != nil {
			sendPacket(outCh, p)
		}
	}
	// endStream sends the partial last packet (padded with silence) and
	// closes the stream: gumble flags its final packet as end-of-stream.
	endStream := func() {
		if p := pack.flush(); p != nil {
			sendPacket(outCh, p)
		}
		close(outCh)
		outCh = nil
	}

	// Meter window state.
	var (
		frames   int
		maxLevel = -120.0
		clipping bool
	)

	for {
		if ctx.Err() != nil {
			break
		}
		frame, err := dev.Read()
		if err != nil {
			break
		}
		clipped := pipeline.Clipped(frame) // raw input overdrive
		if proc != nil {
			if n := e.noise.Load(); n != appliedNoise {
				proc.Configure(apm.ForLevel(int(n)))
				appliedNoise = n
				slog.Info("voice: noise suppression", "level", n)
			}
			if err := proc.Process(frame); err != nil {
				slog.Debug("voice: input processing failed", "err", err)
			}
		}
		if t := math.Float64frombits(e.threshold.Load()); t != appliedThreshold {
			vad.SetOpenThresholdDB(pipeline.ThresholdDB(t))
			appliedThreshold = t
		}

		dec := vad.Sample(frame)
		switch dec {
		case pipeline.Start:
			// Pre-roll: open channel and flush buffered pre-speech frames.
			pack.reset(int(e.packetFrames.Load()))
			bitrate = e.applyBitrate(bitrate, pack.frames)
			outCh = e.client.AudioOutgoing()
			slog.Info("voice: transmitting", "transport", e.client.VoiceTransport(),
				"packet_ms", pack.frames*10, "bitrate", bitrate)
			for _, pre := range vad.PreRoll() {
				send(pre)
			}
			send(frame)
			e.b.Emit(bus.VoiceActivity{Session: e.self, Active: true})

		case pipeline.Continue:
			if outCh != nil {
				send(frame)
			}

		case pipeline.Stop:
			e.logVoiceStats()
			if outCh != nil {
				endStream()
			}
			e.b.Emit(bus.VoiceActivity{Session: e.self, Active: false})
		}

		// Voice meter.
		if l := vad.GetLevel(); l > maxLevel {
			maxLevel = l
		}
		if !clipping && clipped {
			clipping = true
		}
		frames++
		if frames >= levelEveryFrames {
			openDB, _ := vad.Thresholds()
			e.b.Emit(bus.InputLevel{
				LevelDB:      maxLevel,
				ThresholdDB:  openDB,
				Transmitting: outCh != nil,
				Clipping:     clipping,
			})
			frames, maxLevel, clipping = 0, -120, false
		}
	}

	if outCh != nil {
		endStream()
		e.b.Emit(bus.VoiceActivity{Session: e.self, Active: false})
	}
}

// logVoiceStats reports, at the end of a transmission, what reached the
// server: our UDP counters and the server's own decrypt counters from its
// ping replies (they lag up to one ping interval).
func (e *Engine) logVoiceStats() {
	good, late, lost := e.client.ServerCryptStats()
	st := e.client.UDPVoiceStats()
	slog.Info("voice: stopped",
		"transport", e.client.VoiceTransport(),
		"udp_voice_sent", st.VoiceSent,
		"udp_pings_sent", st.PingsSent, "udp_pings_received", st.PingsReceived,
		"server_decrypted", good, "server_late", late, "server_lost", lost)
}

// sendPacket hands one packet to gumble's encoder goroutine. The send never
// blocks the capture loop for longer than one frame period.
func sendPacket(out chan<- gumble.AudioBuffer, packet gumble.AudioBuffer) {
	select {
	case out <- packet:
	case <-time.After(pipeline.FrameDuration):
		slog.Debug("voice: outgoing packet dropped (encoder busy)")
	}
}

// errLoop watches the device error channel and logs unexpected stops.
func (e *Engine) errLoop(ctx context.Context, dev IO) {
	select {
	case <-ctx.Done():
	case err := <-dev.Errors():
		if err != nil {
			slog.Warn("voice engine: device error", "err", err)
		}
	}
}

// --- Incoming (AudioListener → per-user queue → mixer → device) -----------

// OnAudioStream implements gumble.AudioListener. gumble calls it once per
// remote user stream; the stream's packets are queued for the mixer.
func (e *Engine) OnAudioStream(ev *gumble.AudioStreamEvent) {
	go e.drainStream(ev)
}

func (e *Engine) drainStream(ev *gumble.AudioStreamEvent) {
	session := ev.User.Session
	for pkt := range ev.C {
		if len(pkt.AudioBuffer) == 0 {
			continue
		}
		e.mix.push(session, pkt.AudioBuffer)
	}
}

// mixLoop runs the playback clock: every 10 ms it takes at most one frame
// per speaking user, applies the per-user gain, sums them and writes the
// mix to the device. It also derives the remote speaking indicators and
// watches for an output device that stopped consuming audio.
func (e *Engine) mixLoop(ctx context.Context) {
	tick := time.NewTicker(pipeline.FrameDuration)
	defer tick.Stop()
	acc := make([]int32, pipeline.FrameSize)
	out := make([]int16, pipeline.FrameSize)
	var (
		watch   stallWatch
		lastDev IO
	)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			frames, started, stopped := e.mix.pull(now)
			e.devMu.RLock()
			dev := e.dev
			e.devMu.RUnlock()
			for _, s := range started {
				slog.Info("voice: receiving", "session", s)
				e.b.Emit(bus.VoiceActivity{Session: s, Active: true})
			}
			for _, s := range stopped {
				attrs := []any{"session", s.session, "packets", s.packets,
					"output", e.outputName()}
				if st, ok := dev.(playbackStats); ok {
					attrs = append(attrs, "output_underruns", st.Underruns(),
						"output_dropped", st.PlaybackDrops())
				}
				slog.Info("voice: received", attrs...)
				e.b.Emit(bus.VoiceActivity{Session: s.session, Active: false})
			}
			if dev == nil {
				continue
			}
			if dev != lastDev {
				// Fresh device: its counters start from zero.
				watch, lastDev = stallWatch{}, dev
			}
			if len(frames) > 0 {
				clear(acc)
				for session, f := range frames {
					e.gainMu.RLock()
					gain, ok := e.gains[session]
					e.gainMu.RUnlock()
					if !ok {
						gain = 1
					}
					for i := 0; i < len(f) && i < len(acc); i++ {
						acc[i] += int32(float64(f[i]) * gain)
					}
				}
				for i, v := range acc {
					out[i] = int16(max(-32768, min(32767, v)))
				}
				if err := dev.Write(out); err != nil {
					slog.Debug("voice engine: playback write error", "err", err)
				}
			}
			if st, ok := dev.(playbackStats); ok && watch.stalled(now, st) {
				lastDev = nil // the reopened device gets a fresh watch
				go e.escalatePlayback()
			}
		}
	}
}

func (e *Engine) outputName() string {
	e.devMu.RLock()
	defer e.devMu.RUnlock()
	if e.outName == "" {
		return "system default"
	}
	return e.outName
}

// playbackStats is implemented by *device.Device.
type playbackStats interface {
	PlaybackDrops() uint64
	Underruns() uint64
}

// stallWatch flags an output device whose callback stopped pulling audio:
// frames are written (and dropped on the full ring) while the device
// reports no underruns either, i.e. it consumes nothing at all. A device
// must look stuck for stallWindows consecutive windows — a freshly opened
// stream may take a moment to start. It fires once per stall.
type stallWatch struct {
	at               time.Time
	drops, underruns uint64
	stuckWindows     int
	reported         bool
}

const (
	stallWindow  = 2 * time.Second
	stallWindows = 2
)

func (w *stallWatch) stalled(now time.Time, st playbackStats) bool {
	if w.at.IsZero() {
		w.at, w.drops, w.underruns = now, st.PlaybackDrops(), st.Underruns()
		return false
	}
	if now.Sub(w.at) < stallWindow {
		return false
	}
	drops, under := st.PlaybackDrops(), st.Underruns()
	stuck := drops-w.drops > 50 && under == w.underruns
	w.at, w.drops, w.underruns = now, drops, under
	if !stuck {
		w.stuckWindows, w.reported = 0, false
		return false
	}
	w.stuckWindows++
	if w.stuckWindows < stallWindows || w.reported {
		return false
	}
	w.reported = true
	return true
}
