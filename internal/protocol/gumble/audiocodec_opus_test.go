package gumble

import (
	"math"
	"testing"
)

// TestRegisterMurmelOpusCodec verifies the glue between the vendored fork
// and the project's Opus wrapper: registration lands in the fork's codec
// registry under type 4 and the registered generator produces a working
// encoder/decoder pair through the fork's own AudioCodec interface.
func TestRegisterMurmelOpusCodec(t *testing.T) {
	RegisterMurmelOpusCodec()

	codec := getAudioCodec(audioCodecIDOpus)
	if codec == nil {
		t.Fatal("no codec registered under Opus type 4")
	}
	if got := codec.ID(); got != audioCodecIDOpus {
		t.Fatalf("codec.ID() = %d, want %d", got, audioCodecIDOpus)
	}

	enc := codec.NewEncoder()
	if enc == nil {
		t.Fatal("NewEncoder() = nil")
	}
	dec := codec.NewDecoder()
	if dec == nil {
		t.Fatal("NewDecoder() = nil")
	}

	// One 10 ms frame of a 440 Hz sine at Mumble's 48 kHz mono.
	const frameSize, amplitude = 480, 12000
	pcm := make([]int16, frameSize)
	for i := range pcm {
		pcm[i] = int16(amplitude * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	packet, err := enc.Encode(pcm, frameSize, 127)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(packet) == 0 || len(packet) > 127 {
		t.Fatalf("packet length %d out of range", len(packet))
	}
	out, err := dec.Decode(packet, frameSize)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != frameSize {
		t.Fatalf("decoded %d samples, want %d", len(out), frameSize)
	}
}
