package ui

import (
	"math"
	"sync/atomic"

	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"io.github/murmel/internal/audio/apm"
	"io.github/murmel/internal/audio/levelmon"
	"io.github/murmel/internal/audio/pipeline"
	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/settings"
)

// idleHoldWindows keeps "voice detected" up for 300 ms (10 × 30 ms) after
// the level drops below the threshold, like the voice engine's hangover,
// so the meter does not flicker between syllables.
const idleHoldWindows = 10

// idleMic listens to the microphone while no server is connected, so the
// status bar meter shows the voice from the start (detected, not sent).
// It reports bus.InputLevel like the voice engine, which also feeds the
// threshold popover's level meter. The engine takes over while connected.
type idleMic struct {
	st  settings.Settings
	bus *bus.Bus

	mon       *levelmon.Monitor
	gen       int  // invalidates starts that finish too late
	running   bool // wanted: started and not stopped since
	threshold atomic.Uint64
}

func newIdleMic(st settings.Settings, b *bus.Bus) *idleMic {
	m := &idleMic{st: st, bus: b}
	m.updateThreshold()
	st.ConnectChanged(func(key string) {
		switch key {
		case "vad-threshold":
			m.updateThreshold()
		case "audio-input-device", "noise-suppression":
			if m.running {
				m.stop()
				m.start()
			}
		}
	})
	return m
}

func (m *idleMic) updateThreshold() {
	m.threshold.Store(math.Float64bits(pipeline.ThresholdDB(m.st.VADThreshold())))
}

// start opens the microphone off the main loop (opening blocks briefly).
func (m *idleMic) start() {
	if m.running {
		return
	}
	m.running = true
	m.gen++
	gen := m.gen
	dev := m.st.AudioInputDevice()
	// The same noise suppression as the voice engine, so detection here
	// matches what a connection would transmit.
	var process func([]int16)
	if level := m.st.NoiseSuppression(); level > 0 {
		if proc, err := apm.New(apm.ForLevel(level)); err == nil {
			process = func(f []int16) { _ = proc.Process(f) }
		}
	}
	go func() {
		hold := 0
		mon, err := levelmon.Start(dev, process, func(l levelmon.Level) {
			thr := math.Float64frombits(m.threshold.Load())
			if l.DB >= thr {
				hold = idleHoldWindows
			} else if hold > 0 {
				hold--
			}
			m.bus.Emit(bus.InputLevel{
				LevelDB:      l.DB,
				ThresholdDB:  thr,
				Transmitting: hold > 0, // detected; nothing is sent
				Clipping:     l.Clipping,
			})
		})
		glib.IdleAdd(func() {
			if err != nil {
				return // no microphone: the meter stays flat
			}
			if gen != m.gen {
				go mon.Stop()
				return
			}
			m.mon = mon
		})
	}()
}

// stop closes the microphone (a connection's voice engine takes over).
func (m *idleMic) stop() {
	m.running = false
	m.gen++
	if m.mon != nil {
		mon := m.mon
		m.mon = nil
		go mon.Stop()
	}
}
