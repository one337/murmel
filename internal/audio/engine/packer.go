package engine

import (
	"io.github/murmel/internal/audio/pipeline"
	"io.github/murmel/internal/protocol/gumble"
)

// packer joins captured 10 ms frames into outgoing packets of frames × 10
// ms; gumble encodes each packet as one Opus frame. Larger packets halve
// (20 ms) or further cut the per-packet overhead at the cost of latency.
type packer struct {
	frames int
	buf    gumble.AudioBuffer
}

// reset starts a new stream with packets of n frames.
func (p *packer) reset(n int) {
	p.frames = max(1, n)
	p.buf = nil
}

// add appends a copy of frame (capture buffers are reused by the next
// Read) and returns the packet once it holds p.frames frames.
func (p *packer) add(frame []int16) gumble.AudioBuffer {
	if p.buf == nil {
		p.buf = make(gumble.AudioBuffer, 0, p.frames*pipeline.FrameSize)
	}
	p.buf = append(p.buf, frame...)
	if len(p.buf) < p.frames*pipeline.FrameSize {
		return nil
	}
	out := p.buf
	p.buf = nil
	return out
}

// flush returns the pending partial packet padded with silence to the full
// packet length (Opus only encodes whole frame sizes), or nil when empty.
func (p *packer) flush() gumble.AudioBuffer {
	if len(p.buf) == 0 {
		return nil
	}
	out := p.buf[:p.frames*pipeline.FrameSize]
	clear(out[len(p.buf):])
	p.buf = nil
	return out
}
