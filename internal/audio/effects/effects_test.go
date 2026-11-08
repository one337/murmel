package effects

import (
	"bytes"
	"encoding/binary"
	"sync"
	"testing"
	"time"
)

func TestBuiltinsDecode(t *testing.T) {
	for _, id := range Builtins {
		pcm, err := Load(BuiltinChoice(id))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if len(pcm) < sampleRate/10 || len(pcm) > sampleRate {
			t.Errorf("%s: %d samples, want 0.1–1 s", id, len(pcm))
		}
		peak := 0
		for _, s := range pcm {
			peak = max(peak, int(s), -int(s))
		}
		if peak < 3000 || peak > 20000 {
			t.Errorf("%s: peak %d outside the expected loudness", id, peak)
		}
	}
}

func TestEveryDefaultIsValid(t *testing.T) {
	for _, ev := range Events {
		if !valid(Default(ev)) {
			t.Errorf("%s: invalid default %q", ev, Default(ev))
		}
		if _, err := Load(BuiltinChoice(SuggestedBuiltin(ev))); err != nil {
			t.Errorf("%s: suggested builtin: %v", ev, err)
		}
	}
}

func TestChoice(t *testing.T) {
	stored := map[string]string{
		string(UserJoinedChannel): Off,
		string(PrivateMessage):    FileChoice("/tmp/x.ogg"),
		string(SelfMoved):         "builtin:nope",
	}
	if got := Choice(stored, UserJoinedChannel); got != Off {
		t.Errorf("override off: %q", got)
	}
	if got := Choice(stored, PrivateMessage); got != "file:/tmp/x.ogg" {
		t.Errorf("file: %q", got)
	}
	if got := Choice(stored, SelfMoved); got != Default(SelfMoved) {
		t.Errorf("invalid stored value must fall back to the default: %q", got)
	}
	if got := Choice(nil, UserConnected); got != Off {
		t.Errorf("server joins must default to off: %q", got)
	}
}

func wav(rate, channels, bits int, samples []int32) []byte {
	var data bytes.Buffer
	for _, s := range samples {
		switch bits {
		case 16:
			binary.Write(&data, binary.LittleEndian, int16(s))
		case 24:
			data.Write([]byte{byte(s), byte(s >> 8), byte(s >> 16)})
		}
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+data.Len()))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(channels))
	binary.Write(&b, binary.LittleEndian, uint32(rate))
	binary.Write(&b, binary.LittleEndian, uint32(rate*channels*bits/8))
	binary.Write(&b, binary.LittleEndian, uint16(channels*bits/8))
	binary.Write(&b, binary.LittleEndian, uint16(bits))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(data.Len()))
	b.Write(data.Bytes())
	return b.Bytes()
}

func TestDecodeWAVStereoResample(t *testing.T) {
	// 24 kHz stereo, 16 bit: left 10000, right 0 → mono 5000 at 48 kHz.
	var s []int32
	for i := 0; i < 2400; i++ {
		s = append(s, 10000, 0)
	}
	pcm, err := Decode(wav(24000, 2, 16, s))
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 4800 {
		t.Fatalf("got %d samples, want 4800", len(pcm))
	}
	if d := int(pcm[100]) - 5000; d < -2 || d > 2 {
		t.Fatalf("sample %d, want ≈5000", pcm[100])
	}
}

func TestDecodeWAV24AndLimit(t *testing.T) {
	s := make([]int32, 48000*7) // 7 s mono 24 bit
	for i := range s {
		s[i] = 1 << 22 // half scale
	}
	pcm, err := Decode(wav(48000, 1, 24, s))
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != maxSeconds*sampleRate {
		t.Fatalf("got %d samples, want the %d s cap", len(pcm), maxSeconds)
	}
	if d := int(pcm[0]) - 16383; d < -2 || d > 2 {
		t.Fatalf("sample %d, want ≈16383", pcm[0])
	}
}

func TestDecodeRejectsOther(t *testing.T) {
	if _, err := Decode([]byte("ID3 not really an mp3")); err != ErrUnsupported {
		t.Fatalf("err = %v", err)
	}
}

type fakeSink struct {
	mu     sync.Mutex
	frames [][]int16
	closed bool
}

func (f *fakeSink) Write(frame []int16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = append(f.frames, append([]int16(nil), frame...))
	return nil
}
func (f *fakeSink) Close() error { f.mu.Lock(); f.closed = true; f.mu.Unlock(); return nil }

func TestPlayerMixesOverlappingSounds(t *testing.T) {
	sink := &fakeSink{}
	p := &Player{Open: func(string) (Sink, error) { return sink, nil }, cache: map[string][]int16{}}
	a := make([]int16, 3*frameSize)
	b := make([]int16, frameSize)
	for i := range a {
		a[i] = 1000
	}
	for i := range b {
		b[i] = 500
	}
	p.mu.Lock()
	p.voices = [][]int16{a, b}
	p.output = ""
	p.running = true
	p.mu.Unlock()
	go p.run()
	deadline := time.Now().Add(2 * time.Second)
	for {
		sink.mu.Lock()
		n := len(sink.frames)
		sink.mu.Unlock()
		if n >= 4 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.frames) < 4 {
		t.Fatalf("only %d frames written", len(sink.frames))
	}
	if sink.frames[0][0] != 1500 || sink.frames[1][0] != 1000 || sink.frames[3][0] != 0 {
		t.Fatalf("mix wrong: %d %d %d", sink.frames[0][0], sink.frames[1][0], sink.frames[3][0])
	}
}

// pacedSink takes nothing until started, then everything.
type pacedSink struct {
	fakeSink
	started bool
}

func (p *pacedSink) PlaybackQueued() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started {
		return len(p.frames)
	}
	return 0
}

func TestPlayerWaitsForSlowStream(t *testing.T) {
	sink := &pacedSink{}
	p := &Player{Open: func(string) (Sink, error) { return sink, nil }, cache: map[string][]int16{}}
	pcm := make([]int16, 40*frameSize) // 400 ms, longer than queueAhead
	for i := range pcm {
		pcm[i] = int16(i/frameSize + 1) // frame number as sample value
	}
	p.Play(pcm, "")
	time.Sleep(600 * time.Millisecond) // stream "starting": nothing consumed
	sink.mu.Lock()
	queued := len(sink.frames)
	sink.started = true
	sink.mu.Unlock()
	if queued != queueAhead {
		t.Fatalf("queued %d frames before the stream started, want %d", queued, queueAhead)
	}
	time.Sleep(400 * time.Millisecond)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	// Every frame of the sound must arrive, in order: none dropped.
	for i := 0; i < 40; i++ {
		if sink.frames[i][0] != int16(i+1) {
			t.Fatalf("frame %d = %d: sound truncated or reordered", i, sink.frames[i][0])
		}
	}
}
