package engine

import (
	"testing"
	"time"
)

func frame(v int16) []int16 {
	f := make([]int16, 480)
	for i := range f {
		f[i] = v
	}
	return f
}

func TestMixerPrebufferAndOrder(t *testing.T) {
	m := newMixer()
	now := time.Now()
	m.push(1, frame(1))
	m.push(1, frame(2))
	if f, started, _ := m.pull(now); len(f) != 0 || len(started) != 1 || started[0] != 1 {
		t.Fatalf("before prebuffer: frames=%d started=%v, want 0 frames and speaker 1 started", len(f), started)
	}
	m.push(1, frame(3))
	for want := int16(1); want <= 3; want++ {
		f, _, _ := m.pull(now)
		if got := f[1][0]; got != want {
			t.Fatalf("frame order: got %d, want %d", got, want)
		}
	}
}

func TestMixerTwoSpeakersAndStop(t *testing.T) {
	m := newMixer()
	now := time.Now()
	for i := 0; i < defaultPrebuffer; i++ {
		m.push(1, frame(10))
		m.push(2, frame(20))
	}
	f, started, _ := m.pull(now)
	if len(f) != 2 || len(started) != 2 {
		t.Fatalf("frames=%d started=%v, want both speakers", len(f), started)
	}
	// The pushed slice must have been copied.
	src := frame(5)
	m.push(3, src)
	src[0] = 99
	if m.queues[3].frames[0][0] != 5 {
		t.Fatal("push must copy the frame")
	}
	_, _, stopped := m.pull(now.Add(speakingHold + time.Millisecond))
	got := map[uint32]bool{}
	for _, s := range stopped {
		got[s.session] = true
	}
	if !got[1] || !got[2] {
		t.Fatalf("stopped=%v, want speakers 1 and 2 after the hold time", stopped)
	}
}

type fakeStats struct{ drops, under uint64 }

func (f *fakeStats) PlaybackDrops() uint64 { return f.drops }
func (f *fakeStats) Underruns() uint64     { return f.under }

func TestStallWatch(t *testing.T) {
	var w stallWatch
	st := &fakeStats{}
	t0 := time.Now()
	w.stalled(t0, st)
	st.drops = 200 // frames written, nothing consumed, no underruns
	if w.stalled(t0.Add(stallWindow), st) {
		t.Fatal("one stuck window must not be enough (stream start-up)")
	}
	st.drops = 400
	if !w.stalled(t0.Add(2*stallWindow), st) {
		t.Fatal("stuck output not detected after two windows")
	}
	st.drops = 600
	if w.stalled(t0.Add(3*stallWindow), st) {
		t.Fatal("stall must be reported once")
	}
	st.under = 10 // device pulls again (underruns while idle)
	if w.stalled(t0.Add(4*stallWindow), st) {
		t.Fatal("healthy device flagged")
	}
}

// The official client sends 20 ms packets (960 samples): they must be
// played completely, as two consecutive 10 ms chunks.
func TestMixerSplitsLargePackets(t *testing.T) {
	m := newMixer()
	now := time.Now()
	pkt := make([]int16, 960)
	for i := range pkt {
		pkt[i] = int16(i)
	}
	m.push(1, pkt)
	m.push(1, pkt)
	var got []int16
	for i := 0; i < 4; i++ {
		f, _, _ := m.pull(now)
		if f[1] == nil {
			t.Fatalf("pull %d: no frame", i)
		}
		if len(f[1]) != 480 {
			t.Fatalf("chunk len = %d, want 480", len(f[1]))
		}
		got = append(got, f[1]...)
	}
	for i := 0; i < 1920; i++ {
		if got[i] != int16(i%960) {
			t.Fatalf("sample %d = %d, want %d", i, got[i], i%960)
		}
	}
}

func TestMixerConfigurablePrebuffer(t *testing.T) {
	m := newMixer()
	m.setPrebuffer(6)
	now := time.Now()
	for i := 0; i < 5; i++ {
		m.push(1, make([]int16, mixChunk))
	}
	if f, _, _ := m.pull(now); len(f) != 0 {
		t.Fatal("playback started below the 6-frame jitter buffer")
	}
	m.push(1, make([]int16, mixChunk))
	if f, _, _ := m.pull(now); len(f) != 1 {
		t.Fatal("playback did not start once the jitter buffer was full")
	}
}

// TestPlaybackLadderStartsSafe guards the EPOS GSA 70 finding: a first
// playback buffer below 50 ms makes pipewire-pulse pick a 256-sample
// quantum that wedges some USB headsets for every application.
func TestPlaybackLadderStartsSafe(t *testing.T) {
	if playbackPeriods[0] < 50*time.Millisecond {
		t.Fatalf("first playback buffer %v < 50 ms", playbackPeriods[0])
	}
	for i := 1; i < len(playbackPeriods); i++ {
		if playbackPeriods[i] <= playbackPeriods[i-1] {
			t.Fatalf("ladder not increasing at %d", i)
		}
	}
}
