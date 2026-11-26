package effects

import (
	"encoding/binary"
	"testing"
)

// wavFile builds a WAV file with the given format fields and PCM bytes.
func wavFile(format, channels uint16, rate uint32, bits uint16, pcm []byte) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WAVEfmt \x10\x00\x00\x00")
	b = binary.LittleEndian.AppendUint16(b, format)
	b = binary.LittleEndian.AppendUint16(b, channels)
	b = binary.LittleEndian.AppendUint32(b, rate)
	b = binary.LittleEndian.AppendUint32(b, 0) // byte rate, unused
	b = binary.LittleEndian.AppendUint16(b, 0) // block align, unused
	b = binary.LittleEndian.AppendUint16(b, bits)
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(pcm)))
	return append(b, pcm...)
}

// FuzzDecode feeds arbitrary files to the sound decoder; whatever it
// accepts is at most maxSeconds long.
func FuzzDecode(f *testing.F) {
	pcm := []byte{1, 0, 2, 0, 0xff, 0x7f, 0, 0x80}
	f.Add(wavFile(1, 1, 48000, 16, pcm))
	f.Add(wavFile(1, 2, 44100, 8, pcm))
	f.Add(wavFile(1, 1, 8000, 24, pcm[:6]))
	f.Add(wavFile(1, 1, 1, 32, pcm))
	f.Add(wavFile(3, 2, 96000, 32, pcm))
	f.Add(wavFile(1, 1, 1, 8, make([]byte, maxSeconds+1)))
	ogg, err := builtinFS.ReadFile("sounds/blip-up.ogg")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(ogg)
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := Decode(data)
		if err != nil {
			return
		}
		if len(out) > maxSeconds*sampleRate {
			t.Fatalf("decoded %d samples, more than %d", len(out), maxSeconds*sampleRate)
		}
	})
}
