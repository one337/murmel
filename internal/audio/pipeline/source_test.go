package pipeline

import (
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestSinSourceFrequencyAndLevel(t *testing.T) {
	src := NewSinSource(440, 12000)
	var crossings int
	var peak int16
	frames := 10
	for i := 0; i < frames; i++ {
		f, err := src.Read()
		if err != nil {
			t.Fatal(err)
		}
		if len(f) != FrameSize {
			t.Fatalf("frame len = %d", len(f))
		}
		for j := 1; j < len(f); j++ {
			if f[j-1] <= 0 && f[j] > 0 {
				crossings++
			}
		}
		for _, s := range f {
			if s > peak {
				peak = s
			}
		}
	}
	// 10 frames = 0.48 s of audio → ~211 crossings at 440 Hz
	want := int(440 * float64(frames*FrameSize) / float64(SampleRate))
	if crossings < want-2 || crossings > want+2 {
		t.Fatalf("zero crossings = %d, want ≈%d", crossings, want)
	}
	if float64(peak) < 11000 || float64(peak) > 12000 {
		t.Fatalf("peak = %d, want ≈12000", peak)
	}
	rms := 0.0
	for i := 0; i < 100; i++ {
		f, _ := src.Read()
		rms = RMS(f)
	}
	wantRMS := 12000 / math.Sqrt2
	if rms < wantRMS*0.99 || rms > wantRMS*1.01 {
		t.Fatalf("steady-state RMS = %.1f, want ≈%.1f", rms, wantRMS)
	}
}

func TestSinSourcePhaseContinuity(t *testing.T) {
	src := NewSinSource(190, 5000)
	f1, _ := src.Read()
	f2, _ := src.Read()
	joined := append(append([]int16{}, f1...), f2...)
	crossings := 0
	for j := 1; j < len(joined); j++ {
		if joined[j-1] <= 0 && joined[j] > 0 {
			crossings++
		}
	}
	// a phase reset between frames would show as a discontinuity; the
	// crossing count over the joined stream must match the expectation
	want := int(190 * float64(len(joined)) / float64(SampleRate))
	if crossings < want-1 || crossings > want+1 {
		t.Fatalf("phase discontinuity: crossings = %d, want ≈%d", crossings, want)
	}
}

func TestNoiseSourceDeterministicAndRMS(t *testing.T) {
	a := NewNoiseSource(300, 42)
	b := NewNoiseSource(300, 42)
	for i := 0; i < 5; i++ {
		fa, _ := a.Read()
		fb, _ := b.Read()
		for j := range fa {
			if fa[j] != fb[j] {
				t.Fatal("NoiseSource not deterministic for equal seeds")
			}
		}
	}
	c := NewNoiseSource(300, 7)
	f, err := c.Read()
	if err != nil {
		t.Fatal(err)
	}
	rms := RMS(f)
	if rms < 250 || rms > 350 {
		t.Fatalf("RMS = %.1f, want ≈300", rms)
	}
}

func TestFileSourceRoundTrip(t *testing.T) {
	pcm := make([]int16, 2*FrameSize+37)
	for i := range pcm {
		pcm[i] = int16(i % 1000)
	}
	src := NewFileSourceBytes(pcm)

	f1, err := src.Read()
	if err != nil || len(f1) != FrameSize {
		t.Fatalf("frame 1: len=%d err=%v", len(f1), err)
	}
	f2, err := src.Read()
	if err != nil || len(f2) != FrameSize {
		t.Fatalf("frame 2: len=%d err=%v", len(f2), err)
	}
	f3, err := src.Read()
	if err != nil || len(f3) != FrameSize {
		t.Fatalf("tail frame: len=%d err=%v", len(f3), err)
	}
	for i := 0; i < 37; i++ {
		if f3[i] != pcm[2*FrameSize+i] {
			t.Fatalf("tail sample %d = %d, want %d", i, f3[i], pcm[2*FrameSize+i])
		}
	}
	for i := 37; i < FrameSize; i++ {
		if f3[i] != 0 {
			t.Fatalf("tail not zero-padded at %d: %d", i, f3[i])
		}
	}
	if _, err := src.Read(); err != io.EOF {
		t.Fatalf("want io.EOF, got %v", err)
	}
}

func TestFileSourceFileAndFileSink(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "test.pcm")

	sink, err := NewFileSink(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := NewSinSource(300, 8000)
	for i := 0; i < 20; i++ {
		f, _ := want.Read()
		if err := sink.Write(f); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	src, err := NewFileSource(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := NewRecordingSink()
	for {
		f, err := src.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := got.Write(f); err != nil {
			t.Fatal(err)
		}
	}
	if got.NumFrames() != 20 {
		t.Fatalf("frames = %d, want 20", got.NumFrames())
	}
	if math.Abs(got.RMS()-RMS(wantFrames(t, NewSinSource(300, 8000), 20))) > 1 {
		t.Fatal("file round-trip altered PCM")
	}
}

func wantFrames(t *testing.T, src Source, n int) []int16 {
	t.Helper()
	var all []int16
	for i := 0; i < n; i++ {
		f, err := src.Read()
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, f...)
	}
	return all
}

func TestRecordingSinkCaptures(t *testing.T) {
	sink := NewRecordingSink()
	frame := make([]int16, FrameSize)
	for i := range frame {
		frame[i] = int16(i)
	}
	if err := sink.Write(frame); err != nil {
		t.Fatal(err)
	}
	frame[0] = -1 // mutate after write: sink must have copied
	got := sink.Frames()[0]
	if got[0] != 0 {
		t.Fatal("RecordingSink retained caller slice")
	}
	if len(sink.Samples()) != FrameSize {
		t.Fatal("Samples length mismatch")
	}
}

func TestFileSinkWritesLittleEndianPCM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.pcm")
	sink, err := NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	frame := []int16{0x0102, -1, 0x7FFF}
	if err := sink.Write(frame); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 6 {
		t.Fatalf("byte length = %d, want 6", len(raw))
	}
	if raw[0] != 0x02 || raw[1] != 0x01 {
		t.Fatalf("little-endian wrong: % x", raw[:2])
	}
}
