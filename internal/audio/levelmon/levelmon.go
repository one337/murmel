// Package levelmon measures the microphone level without a server
// connection, for the threshold meter's microphone test. It opens the
// capture device only (no playback stream) and reports the level the same
// way the voice engine does: the loudest 10 ms frame of each 30 ms window.
package levelmon

import (
	"io"
	"log/slog"
	"sync"

	"io.github/murmel/internal/audio/device"
	"io.github/murmel/internal/audio/pipeline"
)

// windowFrames is the report cadence: one Level per 3 frames (30 ms).
const windowFrames = 3

// Level is one meter reading.
type Level struct {
	DB       float64 // dBFS RMS of the loudest frame in the window
	Clipping bool
}

// Source is a capture stream; *device.Device implements it.
type Source interface {
	Read() ([]int16, error)
	Close() error
}

// Open opens the named capture device ("" = system default) for
// measuring. Tests replace it.
var Open = func(name string) (Source, error) {
	cfg := device.Resolve(name, "")
	cfg.CaptureOnly = true
	return device.Open(cfg)
}

// Monitor is a running level measurement.
type Monitor struct {
	src  Source
	once sync.Once
	done chan struct{}
}

// Start opens the capture device and calls report from a background
// goroutine for every 30 ms window until Stop. process, when non-nil, is
// applied to each frame before measuring (the engine's input processing),
// so the reading matches what voice detection sees.
func Start(deviceName string, process func([]int16), report func(Level)) (*Monitor, error) {
	src, err := Open(deviceName)
	if err != nil {
		return nil, err
	}
	m := &Monitor{src: src, done: make(chan struct{})}
	go m.run(process, report)
	return m, nil
}

func (m *Monitor) run(process func([]int16), report func(Level)) {
	defer close(m.done)
	var (
		n    int
		max  = -120.0
		clip bool
	)
	for {
		frame, err := m.src.Read()
		if err != nil {
			if err != io.EOF {
				slog.Debug("level monitor: capture ended", "err", err)
			}
			return
		}
		if process != nil {
			process(frame)
		}
		if db := pipeline.LevelDB(frame); db > max {
			max = db
		}
		clip = clip || pipeline.Clipped(frame)
		if n++; n >= windowFrames {
			report(Level{DB: max, Clipping: clip})
			n, max, clip = 0, -120, false
		}
	}
}

// Stop closes the capture device and waits for the reader to end.
func (m *Monitor) Stop() {
	m.once.Do(func() {
		m.src.Close()
		<-m.done
	})
}
