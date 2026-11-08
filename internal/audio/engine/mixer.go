package engine

import (
	"sync"
	"time"
)

const (
	// mixChunk is the mixer's unit: one 10 ms frame. Incoming packets carry
	// 10–60 ms of audio (the official client sends 20 ms by default) and
	// are split into chunks of this size.
	mixChunk = 480
	// mixQueueFrames bounds each speaker's queue in chunks (320 ms); older
	// chunks are dropped when a speaker's packets arrive faster than they
	// play.
	mixQueueFrames = 32
	// defaultPrebuffer chunks are queued before a speaker starts playing,
	// so network jitter does not immediately starve the stream (the jitter
	// buffer, configurable via setPrebuffer).
	defaultPrebuffer = 3
	// speakingHold keeps a remote speaking indicator on across short gaps.
	speakingHold = 250 * time.Millisecond
)

// speakerStop reports the end of a remote speaking episode.
type speakerStop struct {
	session uint32
	packets int
}

// mixer holds one frame queue per remote speaker. push runs on the gumble
// stream goroutines, pull on the engine's playback clock.
type mixer struct {
	mu        sync.Mutex
	queues    map[uint32]*speakerQueue
	prebuffer int
}

type speakerQueue struct {
	frames   [][]int16
	playing  bool // prebuffer satisfied
	speaking bool // indicator state
	last     time.Time
	packets  int // packets received in the current speaking episode
}

func newMixer() *mixer {
	return &mixer{queues: map[uint32]*speakerQueue{}, prebuffer: defaultPrebuffer}
}

// setPrebuffer sets the jitter buffer depth in 10 ms chunks (clamped to
// 1..mixQueueFrames/2). It applies to speakers that (re)start buffering.
func (m *mixer) setPrebuffer(chunks int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prebuffer = max(1, min(mixQueueFrames/2, chunks))
}

func (m *mixer) push(session uint32, pcm []int16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queues[session]
	if q == nil {
		q = &speakerQueue{}
		m.queues[session] = q
	}
	for len(pcm) > 0 {
		chunk := make([]int16, mixChunk) // copy; a short tail is zero-padded
		n := copy(chunk, pcm)
		pcm = pcm[n:]
		if len(q.frames) >= mixQueueFrames {
			q.frames = q.frames[1:]
		}
		q.frames = append(q.frames, chunk)
	}
	q.packets++
	q.last = time.Now()
}

// pull takes the next frame of every playing speaker and reports speaking
// indicator transitions.
func (m *mixer) pull(now time.Time) (frames map[uint32][]int16, started []uint32, stopped []speakerStop) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for session, q := range m.queues {
		if !q.playing && len(q.frames) >= m.prebuffer {
			q.playing = true
		}
		if q.playing {
			if len(q.frames) > 0 {
				if frames == nil {
					frames = map[uint32][]int16{}
				}
				frames[session] = q.frames[0]
				q.frames = q.frames[1:]
			} else {
				q.playing = false // underrun: prebuffer again
			}
		}
		active := now.Sub(q.last) < speakingHold
		switch {
		case active && !q.speaking:
			q.speaking = true
			started = append(started, session)
		case !active && q.speaking:
			q.speaking = false
			stopped = append(stopped, speakerStop{session: session, packets: q.packets})
			q.packets = 0
		}
		if !active && len(q.frames) == 0 {
			delete(m.queues, session)
		}
	}
	return frames, started, stopped
}
