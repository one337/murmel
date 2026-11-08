package apm

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"io.github/murmel/internal/audio/pipeline"
)

// run feeds seconds of audio from gen through a processor and returns the
// mean level (dBFS) of the last second.
func run(t *testing.T, cfg Config, seconds int, gen func(i int) float64) float64 {
	t.Helper()
	pr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	frames := seconds * 100
	var sum float64
	n := 0
	for f := 0; f < frames; f++ {
		frame := make([]int16, pipeline.FrameSize)
		for i := range frame {
			frame[i] = int16(gen(f*pipeline.FrameSize+i) * 32767)
		}
		if err := pr.Process(frame); err != nil {
			t.Fatal(err)
		}
		if f >= frames-100 {
			sum += pipeline.LevelDB(frame)
			n++
		}
	}
	return sum / float64(n)
}

func noise(seed int64, amp float64) func(int) float64 {
	r := rand.New(rand.NewSource(seed))
	return func(int) float64 { return amp * (2*r.Float64() - 1) }
}

func TestNoiseSuppressionAttenuatesSteadyNoise(t *testing.T) {
	off := run(t, Config{}, 3, noise(1, 0.05))
	on := run(t, Config{Noise: NoiseHigh}, 3, noise(1, 0.05))
	t.Logf("steady noise: off %.1f dBFS, high %.1f dBFS", off, on)
	if off-on < 10 {
		t.Fatalf("noise suppression reduced steady noise by only %.1f dB", off-on)
	}
}

// TestSpeechToNoiseImproves runs the synthetic speech corpus clip (speech
// episodes over a room-noise floor, exact labels) through noise
// suppression: the noise between episodes must drop and the speech-to-noise
// distance must grow.
func TestSpeechToNoiseImproves(t *testing.T) {
	pcm := loadCorpus(t, "speech_normal.wav")
	speech := corpusRegions(t, "speech_normal.wav")
	measure := func(cfg Config) (speechDB, noiseDB float64) {
		pr, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer pr.Close()
		var s, n float64
		var ns, nn int
		for f := 0; (f+1)*pipeline.FrameSize <= len(pcm); f++ {
			frame := append([]int16(nil), pcm[f*pipeline.FrameSize:(f+1)*pipeline.FrameSize]...)
			if err := pr.Process(frame); err != nil {
				t.Fatal(err)
			}
			if f < 100 { // let the suppressor adapt
				continue
			}
			db := pipeline.LevelDB(frame)
			if inRegion(speech, f*10) {
				s += db
				ns++
			} else {
				n += db
				nn++
			}
		}
		return s / float64(ns), n / float64(nn)
	}
	sOff, nOff := measure(Config{})
	sOn, nOn := measure(Config{Noise: NoiseModerate, HighPass: true})
	t.Logf("off: speech %.1f, noise %.1f dBFS; moderate: speech %.1f, noise %.1f dBFS", sOff, nOff, sOn, nOn)
	if nOff-nOn < 6 {
		t.Errorf("noise between speech dropped only %.1f dB", nOff-nOn)
	}
	if gain := (sOn - nOn) - (sOff - nOff); gain < 3 {
		t.Errorf("speech-to-noise distance improved only %.1f dB", gain)
	}
}

func TestDisabledIsTransparent(t *testing.T) {
	pr, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	frame := make([]int16, pipeline.FrameSize)
	for i := range frame {
		frame[i] = int16(i * 37 % 20000)
	}
	want := append([]int16(nil), frame...)
	if err := pr.Process(frame); err != nil {
		t.Fatal(err)
	}
	for i := range frame {
		if frame[i] != want[i] {
			t.Fatalf("disabled processor changed sample %d: %d → %d", i, want[i], frame[i])
		}
	}
}

func TestRejectsWrongFrameSize(t *testing.T) {
	pr, _ := New(Config{Noise: NoiseLow})
	defer pr.Close()
	if err := pr.Process(make([]int16, 100)); err == nil {
		t.Fatal("short frame accepted")
	}
}

type region struct{ start, end int } // ms

func inRegion(rs []region, ms int) bool {
	for _, r := range rs {
		if ms >= r.start && ms < r.end {
			return true
		}
	}
	return false
}

func corpusRegions(t *testing.T, name string) []region {
	t.Helper()
	data, err := os.ReadFile("../testdata/labels.csv")
	if err != nil {
		t.Fatal(err)
	}
	var rs []region
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(strings.TrimSpace(line), ",")
		if len(f) != 3 || f[0] != name {
			continue
		}
		a, _ := strconv.Atoi(f[1])
		b, _ := strconv.Atoi(f[2])
		rs = append(rs, region{a, b})
	}
	if len(rs) == 0 {
		t.Fatalf("no labels for %s", name)
	}
	return rs
}

// loadCorpus reads a PCM16 mono WAV from the test corpus.
func loadCorpus(t *testing.T, name string) []int16 {
	t.Helper()
	data, err := os.ReadFile("../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(data, []byte("data"))
	if i < 0 {
		t.Fatalf("%s: no data chunk", name)
	}
	body := data[i+8:]
	pcm := make([]int16, len(body)/2)
	for j := range pcm {
		pcm[j] = int16(binary.LittleEndian.Uint16(body[2*j:]))
	}
	return pcm
}

// TestEchoCancellationRemovesPlayback plays a speech-like signal as the
// reference and feeds the microphone a delayed, attenuated copy of it (the
// echo from the speakers). Compared is the echo energy that reaches the
// output while the far end talks, after the canceller converged.
func TestEchoCancellationRemovesPlayback(t *testing.T) {
	far := loadCorpus(t, "speech_normal.wav")
	for i := range far { // a remote talker at a normal playback level
		far[i] = int16(max(-32768, min(32767, int(far[i])*20)))
	}
	const delayFrames = 6 // 60 ms acoustic + buffer delay
	frames := len(far) / pipeline.FrameSize
	echoAt := func(f int) []int16 {
		mic := make([]int16, pipeline.FrameSize)
		if g := f - delayFrames; g >= 0 {
			for i := range mic {
				mic[i] = far[g*pipeline.FrameSize+i] / 2 // room attenuation
			}
		}
		return mic
	}
	run := func(echo bool) float64 {
		pr, err := New(Config{EchoCancel: echo, HighPass: echo})
		if err != nil {
			t.Fatal(err)
		}
		defer pr.Close()
		var energy float64
		for f := 0; f < frames; f++ {
			if err := pr.ProcessReverse(far[f*pipeline.FrameSize : (f+1)*pipeline.FrameSize]); err != nil {
				t.Fatal(err)
			}
			mic := echoAt(f)
			active := pipeline.LevelDB(mic) > -40 // far end talking
			if echo {
				pr.SetStreamDelay(delayFrames * 10)
			}
			if err := pr.Process(mic); err != nil {
				t.Fatal(err)
			}
			if f >= frames/2 && active {
				for _, v := range mic {
					energy += float64(v) * float64(v)
				}
			}
		}
		return 10 * math.Log10(energy+1)
	}
	off, on := run(false), run(true)
	t.Logf("echo energy while the far end talks: reduced by %.1f dB", off-on)
	if off-on < 20 {
		t.Fatalf("echo cancellation removed only %.1f dB of the echo", off-on)
	}
}

func TestClosedProcessorIsSafe(t *testing.T) {
	pr, _ := New(Config{EchoCancel: true})
	pr.Close()
	pr.Close()
	frame := make([]int16, pipeline.FrameSize)
	if pr.Process(frame) == nil || pr.ProcessReverse(frame) == nil {
		t.Fatal("closed processor accepted frames")
	}
	pr.SetStreamDelay(10)
	pr.Configure(Config{})
}

// TestEchoCancellationKeepsOwnVoice: with nothing being played back, the
// microphone signal must pass (almost) unchanged.
func TestEchoCancellationKeepsOwnVoice(t *testing.T) {
	near := loadCorpus(t, "speech_normal.wav")
	for i := range near {
		near[i] = int16(max(-32768, min(32767, int(near[i])*20)))
	}
	energy := func(echo bool) float64 {
		pr, err := New(Config{EchoCancel: echo})
		if err != nil {
			t.Fatal(err)
		}
		defer pr.Close()
		silence := make([]int16, pipeline.FrameSize)
		var e float64
		for f := 0; (f+1)*pipeline.FrameSize <= len(near); f++ {
			_ = pr.ProcessReverse(silence)
			mic := append([]int16(nil), near[f*pipeline.FrameSize:(f+1)*pipeline.FrameSize]...)
			if echo {
				pr.SetStreamDelay(60)
			}
			if err := pr.Process(mic); err != nil {
				t.Fatal(err)
			}
			if f >= 100 {
				for _, v := range mic {
					e += float64(v) * float64(v)
				}
			}
		}
		return 10 * math.Log10(e+1)
	}
	if loss := energy(false) - energy(true); loss > 3 {
		t.Fatalf("own voice lost %.1f dB with echo cancellation and silent playback", loss)
	}
}
