package effects

import (
	"log/slog"
	"sync"
	"time"

	"io.github/murmel/internal/audio/device"
)

const (
	frameSize = sampleRate / 100 // 10 ms
	// idleClose releases the output stream after this long without sound.
	// Generous, because some sinks take seconds to start a stream.
	idleClose = 30 * time.Second
	// maxVoices bounds overlapping sounds; the oldest is dropped.
	maxVoices = 4
	// streamPeriod is the playback buffer: event sounds need no low
	// latency, and some USB headsets only start streams at 100–200 ms
	// (see engine.playbackPeriods).
	streamPeriod = 200 * time.Millisecond
	// queueAhead is how many frames the player keeps queued in a pacing
	// sink: a bit more than one stream period.
	queueAhead = int(streamPeriod/(10*time.Millisecond)) + 5
	// stallGiveUp abandons a stream that takes nothing for this long
	// (dead sink): queued sounds are dropped and the stream is closed.
	stallGiveUp = 8 * time.Second
)

// Sink is the output a Player writes 10 ms frames to.
type Sink interface {
	Write(frame []int16) error
	Close() error
}

// pacingSink is a Sink that reports how much audio it still holds; the
// player then follows the device's actual consumption, so a stream that
// starts late delays a sound instead of dropping its beginning.
// *device.Device implements it.
type pacingSink interface {
	PlaybackQueued() int
}

// Player mixes event sounds onto an output device. The device is opened
// on demand (playback only) and closed again when idle. Safe for
// concurrent use.
type Player struct {
	// Open opens the named output ("" = system default); tests replace it.
	Open func(output string) (Sink, error)

	mu      sync.Mutex
	voices  [][]int16
	running bool
	output  string
	cache   map[string][]int16
}

// NewPlayer returns a player for the system's audio devices.
func NewPlayer() *Player {
	return &Player{
		Open: func(output string) (Sink, error) {
			cfg := device.Resolve("", output)
			cfg.PlaybackPeriod = streamPeriod
			cfg.PlaybackOnly = true
			return device.Open(cfg)
		},
		cache: map[string][]int16{},
	}
}

// PlayChoice loads (cached) and plays the sound for choice on output. It
// returns immediately; loading and playback run in the background.
func (p *Player) PlayChoice(choice, output string) {
	if choice == Off || choice == "" {
		return
	}
	go func() {
		p.mu.Lock()
		pcm, ok := p.cache[choice]
		p.mu.Unlock()
		if !ok {
			var err error
			if pcm, err = Load(choice); err != nil {
				slog.Warn("sound: cannot load", "sound", choice, "err", err)
				return
			}
			p.mu.Lock()
			p.cache[choice] = pcm
			p.mu.Unlock()
		}
		p.Play(pcm, output)
	}()
}

// Play mixes pcm (48 kHz mono) into the output.
func (p *Player) Play(pcm []int16, output string) {
	if len(pcm) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.voices) >= maxVoices {
		p.voices = p.voices[1:]
	}
	p.voices = append(p.voices, pcm)
	p.output = output
	if !p.running {
		p.running = true
		go p.run()
	}
}

// run owns the output stream: it writes one mixed frame every 10 ms and
// closes the stream after idleClose of silence. It reopens the stream
// when the output device changes.
func (p *Player) run() {
	var (
		sink    Sink
		opened  string
		idle    time.Duration
		blocked time.Duration
		frame   = make([]int16, frameSize)
		acc     = make([]int32, frameSize)
	)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	defer func() {
		if sink != nil {
			sink.Close()
		}
	}()
	for range tick.C {
		if ps, ok := sink.(pacingSink); ok && ps.PlaybackQueued() >= queueAhead {
			// The device has not taken what is queued yet.
			if blocked += 10 * time.Millisecond; blocked >= stallGiveUp {
				slog.Warn("sound: output device is not playing; sounds dropped", "device", opened)
				p.mu.Lock()
				p.voices = nil
				p.running = false
				p.mu.Unlock()
				return
			}
			continue
		}
		blocked = 0
		p.mu.Lock()
		if len(p.voices) == 0 && idle >= idleClose {
			p.running = false
			p.mu.Unlock()
			return
		}
		output := p.output
		active := len(p.voices) > 0
		clear(acc)
		kept := p.voices[:0]
		for _, v := range p.voices {
			n := min(frameSize, len(v))
			for i := 0; i < n; i++ {
				acc[i] += int32(v[i])
			}
			if v = v[n:]; len(v) > 0 {
				kept = append(kept, v)
			}
		}
		p.voices = kept
		p.mu.Unlock()

		if !active {
			idle += 10 * time.Millisecond
		} else {
			idle = 0
		}
		if sink == nil || output != opened {
			if sink != nil {
				sink.Close()
				sink = nil
			}
			s, err := p.Open(output)
			if err != nil {
				slog.Warn("sound: output unavailable", "device", output, "err", err)
				p.mu.Lock()
				p.voices = nil
				p.running = false
				p.mu.Unlock()
				return
			}
			sink, opened = s, output
		}
		for i, v := range acc {
			frame[i] = int16(max(-32768, min(32767, v)))
		}
		sink.Write(frame)
	}
}
