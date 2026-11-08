package levelmon

import (
	"io"
	"sync"
	"testing"

	"io.github/murmel/internal/audio/pipeline"
)

type fakeSource struct {
	mu     sync.Mutex
	frames [][]int16
	closed bool
}

func (f *fakeSource) Read() ([]int16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || len(f.frames) == 0 {
		return nil, io.EOF
	}
	fr := f.frames[0]
	f.frames = f.frames[1:]
	return fr, nil
}

func (f *fakeSource) Close() error { f.mu.Lock(); f.closed = true; f.mu.Unlock(); return nil }

func constFrame(v int16) []int16 {
	f := make([]int16, pipeline.FrameSize)
	for i := range f {
		if i%2 == 0 {
			f[i] = v
		} else {
			f[i] = -v
		}
	}
	return f
}

func TestMonitorReportsWindowMaxAndClipping(t *testing.T) {
	src := &fakeSource{frames: [][]int16{constFrame(328), constFrame(3277), constFrame(328), constFrame(32767), constFrame(1), constFrame(1)}}
	Open = func(string) (Source, error) { return src, nil }
	var got []Level
	var mu sync.Mutex
	m, err := Start("", nil, func(l Level) { mu.Lock(); got = append(got, l); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	<-m.done
	m.Stop()
	if len(got) != 2 {
		t.Fatalf("got %d readings, want 2", len(got))
	}
	if got[0].DB < -21 || got[0].DB > -19 || got[0].Clipping {
		t.Errorf("window 1 = %+v, want ≈-20 dBFS, no clipping", got[0])
	}
	if !got[1].Clipping {
		t.Errorf("window 2 = %+v, want clipping", got[1])
	}
}

func TestMonitorAppliesProcessing(t *testing.T) {
	src := &fakeSource{frames: [][]int16{constFrame(3277), constFrame(3277), constFrame(3277)}}
	Open = func(string) (Source, error) { return src, nil }
	var got Level
	m, _ := Start("", func(f []int16) { clear(f) }, func(l Level) { got = l })
	<-m.done
	m.Stop()
	if got.DB > -100 {
		t.Fatalf("processing not applied before measuring: %+v", got)
	}
}
