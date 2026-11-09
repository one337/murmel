package pipeline

import (
	"bufio"
	"encoding/binary"
	"math"
	"os"
	"sync"
)

// Sink consumes decoded 480-sample frames. Write is called from the sink
// stage goroutine only. The frame slice aliases a pooled buffer and is
// valid only for the duration of the call — implementations that retain
// it must copy. The malgo playback callback copies into the device
// buffer and returns.
type Sink interface {
	Write(frame []int16) error
}

// RecordingSink captures every frame into memory for test assertions.
// Not for production use: Write copies (allocates).
type RecordingSink struct {
	mu     sync.Mutex
	frames [][]int16
	total  int
}

func NewRecordingSink() *RecordingSink { return &RecordingSink{} }

func (r *RecordingSink) Write(frame []int16) error {
	cp := make([]int16, len(frame))
	copy(cp, frame)
	r.mu.Lock()
	r.frames = append(r.frames, cp)
	r.total += len(frame)
	r.mu.Unlock()
	return nil
}

// Frames returns a snapshot of all captured frames.
func (r *RecordingSink) Frames() [][]int16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]int16, len(r.frames))
	copy(out, r.frames)
	return out
}

// Samples returns all captured PCM concatenated.
func (r *RecordingSink) Samples() []int16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int16, 0, r.total)
	for _, f := range r.frames {
		out = append(out, f...)
	}
	return out
}

func (r *RecordingSink) NumFrames() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.frames)
}

// RMS returns the RMS level of everything captured so far.
func (r *RecordingSink) RMS() float64 { return RMS(r.Samples()) }

// FileSink appends raw little-endian PCM16 to a file. Close flushes.
type FileSink struct {
	w   *bufio.Writer
	f   *os.File
	buf [2 * FrameSize]byte
}

func NewFileSink(path string) (*FileSink, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &FileSink{f: f, w: bufio.NewWriter(f)}, nil
}

func (s *FileSink) Write(frame []int16) error {
	if len(frame) > FrameSize {
		frame = frame[:FrameSize]
	}
	for i, v := range frame {
		binary.LittleEndian.PutUint16(s.buf[2*i:], uint16(v))
	}
	_, err := s.w.Write(s.buf[:2*len(frame)])
	return err
}

func (s *FileSink) Close() error {
	if err := s.w.Flush(); err != nil {
		s.f.Close()
		return err
	}
	return s.f.Close()
}

// RMS returns the root-mean-square level of a PCM frame in linear
// amplitude units (0..32768).
func RMS(frame []int16) float64 {
	if len(frame) == 0 {
		return 0
	}
	var sum float64
	for _, s := range frame {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(frame)))
}
