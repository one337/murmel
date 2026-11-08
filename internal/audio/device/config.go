package device

import (
	"time"

	"github.com/gen2brain/malgo"

	"io.github/murmel/internal/audio/pipeline"
)

// The stream format is the pipeline voice format by construction — the
// constants alias T11's, so capture output can flow into the encode
// stage and decode output into playback with no conversion anywhere.
// There is no resampling in this system: 48 kHz end to end.
const (
	sampleRate   = pipeline.SampleRate    // 48000
	periodFrames = pipeline.FrameSize     // 480 samples per callback
	frameBytes   = 2 * pipeline.FrameSize // mono S16: 960 bytes per period
)

// Ring depths in whole periods. The capture ring only has to absorb
// scheduler jitter on the capture goroutine; the playback ring also
// absorbs bursts from the decode stage, so it is twice as deep. Both
// drop oldest on overflow — voice is delay-sensitive, stale frames are
// worthless.
const (
	captureSlots  = 8  // 80 ms
	playbackSlots = 16 // 160 ms
)

// preferredBackends is the backend preference order: PulseAudio first
// (pipewire-pulse serves the same protocol transparently), ALSA as the
// fallback. miniaudio dlopen()s these lazily, so an unavailable backend
// degrades to the next one instead of failing the import.
var preferredBackends = []malgo.Backend{malgo.BackendPulseaudio, malgo.BackendAlsa}

// duplexConfig assembles the malgo device configuration for the voice
// stream: duplex, 48 kHz, S16, mono capture and playback, one 10 ms
// period per callback with two periods of buffering, low-latency
// profile. Nil device IDs select the system defaults; explicit IDs are
// copied into C memory by malgo (256 bytes per Open — an accepted,
// documented leak; device re-init is rare).
func duplexConfig(captureID, playbackID *malgo.DeviceID) malgo.DeviceConfig {
	cfg := malgo.DefaultDeviceConfig(malgo.Duplex)
	cfg.SampleRate = sampleRate
	cfg.PeriodSizeInFrames = periodFrames
	cfg.Periods = 2
	cfg.PerformanceProfile = malgo.LowLatency
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = 1
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = 1
	if captureID != nil {
		cfg.Capture.DeviceID = captureID.Pointer()
	}
	if playbackID != nil {
		cfg.Playback.DeviceID = playbackID.Pointer()
	}
	return cfg
}

// streamPair holds the capture and playback stream configurations Open
// uses: the duplex configuration split into two single-direction streams.
type streamPair struct {
	capture, playback malgo.DeviceConfig
}

func streamConfigs(captureID, playbackID *malgo.DeviceID, playbackPeriod time.Duration) streamPair {
	base := duplexConfig(captureID, playbackID)
	capCfg, playCfg := base, base
	capCfg.DeviceType = malgo.Capture
	playCfg.DeviceType = malgo.Playback
	playCfg.PeriodSizeInFrames = playbackPeriodFrames(playbackPeriod)
	return streamPair{capture: capCfg, playback: playCfg}
}

// playbackPeriodFrames converts the requested playback period into whole
// 10 ms frames (at least one).
func playbackPeriodFrames(d time.Duration) uint32 {
	n := int(d / pipeline.FrameDuration)
	if n < 1 {
		n = 1
	}
	return uint32(n * periodFrames)
}

// playbackRingSlots sizes the playback ring so it holds two backend
// periods plus the usual decode headroom: the device pulls a whole period
// at once, while the mixer writes 10 ms at a time.
func playbackRingSlots(d time.Duration) int {
	periods := int(playbackPeriodFrames(d) / periodFrames)
	return max(playbackSlots, 2*periods+playbackSlots/2)
}
