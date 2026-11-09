package pipeline

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
)

// Source yields 480-sample mono PCM frames. Read blocks until a frame is
// available and returns io.EOF at end of stream. The returned slice is
// valid until the next Read. malgo's capture callback adapts by
// handing each callback buffer to the same consumption path.
type Source interface {
	Read() ([]int16, error)
}

// SinSource generates a sine wave of Freq Hz at Level peak amplitude with
// continuous phase across frames. Deterministic: output depends only on
// the sample index.
type SinSource struct {
	Freq   float64
	Level  float64
	sample uint64
	buf    [FrameSize]int16
}

func NewSinSource(freq float64, level float64) *SinSource {
	return &SinSource{Freq: freq, Level: level}
}

func (s *SinSource) Read() ([]int16, error) {
	for i := range s.buf {
		t := float64(s.sample+uint64(i)) / float64(SampleRate)
		s.buf[i] = int16(s.Level * math.Sin(2*math.Pi*s.Freq*t))
	}
	s.sample += FrameSize
	return s.buf[:], nil
}

// NoiseSource generates white noise with RMS level Level, deterministic
// for a given Seed.
type NoiseSource struct {
	Level float64
	rng   *rand.Rand
	buf   [FrameSize]int16
}

func NewNoiseSource(level float64, seed int64) *NoiseSource {
	return &NoiseSource{Level: level, rng: rand.New(rand.NewSource(seed))}
}

func (s *NoiseSource) Read() ([]int16, error) {
	// uniform [-a, a] has RMS a/√3, so a = Level·√3 for RMS Level
	a := s.Level * math.Sqrt(3)
	for i := range s.buf {
		s.buf[i] = int16(s.rng.Float64()*2*a - a)
	}
	return s.buf[:], nil
}

// FileSource streams raw little-endian PCM16 mono 48 kHz frames from a
// file. A short trailing frame is zero-padded; the following Read returns
// io.EOF.
type FileSource struct {
	pcm  []int16
	next int
	eof  bool
	buf  [FrameSize]int16
}

func NewFileSource(path string) (*FileSource, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pipeline: read %s: %w", path, err)
	}
	if len(raw)%2 != 0 {
		return nil, fmt.Errorf("pipeline: %s: odd byte length %d", path, len(raw))
	}
	pcm := make([]int16, len(raw)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}
	return &FileSource{pcm: pcm}, nil
}

// NewFileSourceBytes builds a FileSource from in-memory raw PCM16.
func NewFileSourceBytes(pcm []int16) *FileSource {
	return &FileSource{pcm: pcm}
}

func (s *FileSource) Read() ([]int16, error) {
	if s.eof {
		return nil, io.EOF
	}
	if s.next >= len(s.pcm) {
		s.eof = true
		return nil, io.EOF
	}
	n := copy(s.buf[:], s.pcm[s.next:])
	for i := n; i < FrameSize; i++ {
		s.buf[i] = 0
	}
	s.next += n
	if n < FrameSize {
		s.eof = true
	}
	return s.buf[:], nil
}
