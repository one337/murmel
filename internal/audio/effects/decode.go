package effects

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/jfreymuth/oggvorbis"
)

const (
	sampleRate = 48000
	// maxSeconds bounds a sound: event sounds are short, and a mistakenly
	// chosen song must not play for minutes.
	maxSeconds = 5
	// maxFileBytes bounds what Load reads from disk.
	maxFileBytes = 16 << 20
)

// ErrUnsupported is returned for files that are neither WAV nor Ogg Vorbis.
var ErrUnsupported = errors.New("effects: unsupported sound format (use WAV or Ogg Vorbis)")

// Load returns the 48 kHz mono PCM for a choice (nil for Off).
func Load(choice string) ([]int16, error) {
	if choice == Off || choice == "" {
		return nil, nil
	}
	if id, ok := BuiltinID(choice); ok {
		data, err := builtinFS.ReadFile("sounds/" + id + ".ogg")
		if err != nil {
			return nil, fmt.Errorf("effects: unknown built-in sound %q", id)
		}
		return Decode(data)
	}
	if path, ok := FilePath(choice); ok {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxFileBytes))
		if err != nil {
			return nil, err
		}
		return Decode(data)
	}
	return nil, fmt.Errorf("effects: invalid sound choice %q", choice)
}

// Decode converts a WAV (integer or float PCM) or Ogg Vorbis file into
// 48 kHz mono 16-bit PCM, truncated to maxSeconds.
func Decode(data []byte) ([]int16, error) {
	switch {
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE":
		return decodeWAV(data)
	case len(data) >= 4 && string(data[:4]) == "OggS":
		samples, format, err := oggvorbis.ReadAll(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("effects: ogg vorbis: %w", err)
		}
		return toPCM(samples, format.Channels, format.SampleRate)
	}
	return nil, ErrUnsupported
}

func decodeWAV(data []byte) ([]int16, error) {
	var (
		format, channels, bits uint16
		rate                   uint32
		pcm                    []byte
		haveFmt                bool
	)
	for p := 12; p+8 <= len(data); {
		id := string(data[p : p+4])
		size := int(binary.LittleEndian.Uint32(data[p+4 : p+8]))
		body := data[p+8:]
		if size > len(body) {
			size = len(body)
		}
		body = body[:size]
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("effects: wav: short fmt chunk")
			}
			format = binary.LittleEndian.Uint16(body[0:2])
			channels = binary.LittleEndian.Uint16(body[2:4])
			rate = binary.LittleEndian.Uint32(body[4:8])
			bits = binary.LittleEndian.Uint16(body[14:16])
			if format == 0xFFFE && size >= 26 { // WAVE_FORMAT_EXTENSIBLE
				format = binary.LittleEndian.Uint16(body[24:26])
			}
			haveFmt = true
		case "data":
			pcm = body
		}
		p += 8 + size + size%2
	}
	if !haveFmt || pcm == nil || channels == 0 || rate == 0 {
		return nil, errors.New("effects: wav: missing fmt or data chunk")
	}
	width := int(bits / 8)
	if width == 0 {
		return nil, errors.New("effects: wav: invalid sample width")
	}
	n := len(pcm) / width
	if limit := maxSeconds * int(rate) * int(channels); n > limit {
		n = limit
	}
	samples := make([]float32, n)
	for i := range samples {
		b := pcm[i*width:]
		switch {
		case format == 1 && bits == 8:
			samples[i] = (float32(b[0]) - 128) / 128
		case format == 1 && bits == 16:
			samples[i] = float32(int16(binary.LittleEndian.Uint16(b))) / 32768
		case format == 1 && bits == 24:
			v := int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24) >> 8
			samples[i] = float32(v) / (1 << 23)
		case format == 1 && bits == 32:
			samples[i] = float32(int32(binary.LittleEndian.Uint32(b))) / (1 << 31)
		case format == 3 && bits == 32:
			samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(b))
		default:
			return nil, fmt.Errorf("effects: wav: unsupported encoding (format %d, %d bit)", format, bits)
		}
	}
	return toPCM(samples, int(channels), int(rate))
}

// toPCM downmixes interleaved float samples to mono, resamples linearly to
// 48 kHz and converts to int16, keeping at most maxSeconds.
func toPCM(samples []float32, channels, rate int) ([]int16, error) {
	if channels < 1 || rate < 1 {
		return nil, errors.New("effects: invalid channel count or sample rate")
	}
	frames := len(samples) / channels
	mono := make([]float32, frames)
	for i := range mono {
		var sum float32
		for c := 0; c < channels; c++ {
			sum += samples[i*channels+c]
		}
		mono[i] = sum / float32(channels)
	}
	if frames == 0 {
		return nil, errors.New("effects: empty sound")
	}
	outLen := int(int64(frames) * sampleRate / int64(rate))
	outLen = min(outLen, maxSeconds*sampleRate)
	out := make([]int16, outLen)
	step := float64(rate) / sampleRate
	for i := range out {
		pos := float64(i) * step
		j := int(pos)
		frac := float32(pos - float64(j))
		v := mono[min(j, frames-1)]
		if j+1 < frames {
			v += (mono[j+1] - v) * frac
		}
		out[i] = int16(max(-32768, min(32767, v*32767)))
	}
	return out, nil
}
