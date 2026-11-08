package engine

import (
	"testing"

	"io.github/murmel/internal/audio/pipeline"
)

func frameOf(v int16) []int16 {
	f := make([]int16, pipeline.FrameSize)
	for i := range f {
		f[i] = v
	}
	return f
}

func TestPackerJoinsFrames(t *testing.T) {
	var p packer
	p.reset(2)
	src := frameOf(1)
	if got := p.add(src); got != nil {
		t.Fatalf("first frame emitted a packet of %d samples", len(got))
	}
	src[0] = 99 // capture buffer reuse must not leak into the packet
	got := p.add(frameOf(2))
	if len(got) != 2*pipeline.FrameSize {
		t.Fatalf("packet has %d samples, want %d", len(got), 2*pipeline.FrameSize)
	}
	if got[0] != 1 || got[pipeline.FrameSize] != 2 {
		t.Fatalf("packet content wrong: %d, %d", got[0], got[pipeline.FrameSize])
	}
	if p.flush() != nil {
		t.Fatal("flush after a full packet returned data")
	}
}

func TestPackerFlushPadsSilence(t *testing.T) {
	var p packer
	p.reset(4)
	p.add(frameOf(7))
	got := p.flush()
	if len(got) != 4*pipeline.FrameSize {
		t.Fatalf("flushed %d samples, want %d", len(got), 4*pipeline.FrameSize)
	}
	if got[0] != 7 || got[pipeline.FrameSize] != 0 || got[len(got)-1] != 0 {
		t.Fatal("partial packet not padded with silence")
	}
}

func TestPackerSingleFrame(t *testing.T) {
	var p packer
	p.reset(1)
	if got := p.add(frameOf(3)); len(got) != pipeline.FrameSize {
		t.Fatalf("10 ms packet has %d samples", len(got))
	}
}

func TestValidPacketFrames(t *testing.T) {
	for in, want := range map[int]int{0: 2, 1: 1, 2: 2, 3: 2, 4: 4, 5: 2, 6: 6, 9: 2} {
		if got := validPacketFrames(in); got != want {
			t.Errorf("validPacketFrames(%d) = %d, want %d", in, got, want)
		}
	}
}
