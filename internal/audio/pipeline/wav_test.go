package pipeline

import (
	"encoding/binary"
	"fmt"
	"os"
)

// WAVClip is a parsed WAV file: mono PCM16 audio plus its sample rate.
type WAVClip struct {
	SampleRate int
	PCM        []int16
}

// ReadWAV parses a RIFF/WAVE file with a PCM16 data chunk (the corpus
// format: mono 48 kHz). Tolerates extra chunks (LIST etc.) between fmt
// and data.
func ReadWAV(path string) (*WAVClip, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, fmt.Errorf("wav: %s: not a RIFF/WAVE file", path)
	}
	var sampleRate, channels, bits int
	var pcm []int16
	pos := 12
	for pos+8 <= len(raw) {
		id := string(raw[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(raw[pos+4 : pos+8]))
		body := pos + 8
		if body+size > len(raw) {
			return nil, fmt.Errorf("wav: %s: chunk %q overruns file", path, id)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, fmt.Errorf("wav: %s: fmt chunk too small", path)
			}
			format := binary.LittleEndian.Uint16(raw[body:])
			channels = int(binary.LittleEndian.Uint16(raw[body+2:]))
			sampleRate = int(binary.LittleEndian.Uint32(raw[body+4:]))
			bits = int(binary.LittleEndian.Uint16(raw[body+14:]))
			if format != 1 {
				return nil, fmt.Errorf("wav: %s: format %d is not PCM", path, format)
			}
		case "data":
			if size%2 != 0 {
				return nil, fmt.Errorf("wav: %s: odd data size", path)
			}
			pcm = make([]int16, size/2)
			for i := range pcm {
				pcm[i] = int16(binary.LittleEndian.Uint16(raw[body+2*i:]))
			}
		}
		pos = body + size
		if size%2 == 1 {
			pos++
		}
	}
	if sampleRate == 0 || pcm == nil {
		return nil, fmt.Errorf("wav: %s: missing fmt or data chunk", path)
	}
	if channels != 1 || bits != 16 {
		return nil, fmt.Errorf("wav: %s: want mono 16-bit, got %dch %dbit", path, channels, bits)
	}
	return &WAVClip{SampleRate: sampleRate, PCM: pcm}, nil
}
