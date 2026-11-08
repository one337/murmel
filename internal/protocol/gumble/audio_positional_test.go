package gumble

import (
	"encoding/binary"
	"io"
	"math"
	"net"
	"testing"
	"time"

	"io.github/murmel/internal/protocol/gumble/varint"
)

// voiceFrame is one UDPTunnel voice packet as observed on the wire.
type voiceFrame struct {
	headerByte byte
	seq        int64
	final      bool
	payload    []byte
	hasPos     bool
	x, y, z    float32
}

// readVoiceFrames parses UDPTunnel packets from the server side of a pipe:
// 6-byte message header, then type/target byte, varint sequence, varint
// length (bit 13 = terminator), Opus payload, optional 3x float32 position.
func readVoiceFrames(r io.Reader, frames chan<- voiceFrame, errCh chan<- error) {
	var header [6]byte
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			errCh <- err
			return
		}
		if pType := binary.BigEndian.Uint16(header[:]); pType != 1 {
			errCh <- io.ErrUnexpectedEOF
			return
		}
		body := make([]byte, binary.BigEndian.Uint32(header[2:]))
		if _, err := io.ReadFull(r, body); err != nil {
			errCh <- err
			return
		}

		f := voiceFrame{headerByte: body[0]}
		body = body[1:]
		var n int
		f.seq, n = varint.Decode(body)
		body = body[n:]
		length, n := varint.Decode(body)
		body = body[n:]
		f.final = length&0x2000 != 0
		f.payload = body[:length&^0x2000]
		body = body[len(f.payload):]
		if len(body) == 3*4 {
			f.hasPos = true
			f.x = math.Float32frombits(binary.LittleEndian.Uint32(body))
			f.y = math.Float32frombits(binary.LittleEndian.Uint32(body[4:]))
			f.z = math.Float32frombits(binary.LittleEndian.Uint32(body[8:]))
		}
		frames <- f
	}
}

// TestAudioOutgoingPositional verifies that a positional outgoing stream
// appends the X/Y/Z float32s to every voice packet (mid-stream and final),
// while plain AudioOutgoing sends none.
func TestAudioOutgoingPositional(t *testing.T) {
	RegisterMurmelOpusCodec()

	server, wire := net.Pipe()
	defer server.Close()
	defer wire.Close()

	client := &Client{
		Config:       NewConfig(),
		Conn:         NewConn(wire),
		AudioEncoder: getAudioCodec(audioCodecIDOpus).NewEncoder(),
	}

	frames := make(chan voiceFrame, 4)
	errCh := make(chan error, 1)
	go readVoiceFrames(server, frames, errCh)

	x, y, z := float32(1.5), float32(-2.25), float32(100.125)
	out := client.AudioOutgoingPositional(&x, &y, &z)
	out <- AudioBuffer(sinePCM(t, 0))
	out <- AudioBuffer(sinePCM(t, 1))
	close(out)

	for i := 0; i < 2; i++ {
		select {
		case f := <-frames:
			if f.headerByte != byte(audioCodecIDOpus)<<5 {
				t.Fatalf("frame %d: header byte %#x, want opus/target 0", i, f.headerByte)
			}
			if !f.hasPos {
				t.Fatalf("frame %d (final=%v): positional data missing", i, f.final)
			}
			if f.x != x || f.y != y || f.z != z {
				t.Fatalf("frame %d: position (%v, %v, %v), want (%v, %v, %v)", i, f.x, f.y, f.z, x, y, z)
			}
			if len(f.payload) == 0 {
				t.Fatalf("frame %d: empty Opus payload", i)
			}
			if i == 0 && f.final {
				t.Fatal("first frame must not carry the terminator bit")
			}
			if i == 1 && !f.final {
				t.Fatal("final frame must carry the terminator bit")
			}
		case err := <-errCh:
			t.Fatalf("read frame %d: %v", i, err)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for frame %d", i)
		}
	}
}

// TestAudioOutgoingNonPositional pins the legacy wire format: no positional
// bytes after the Opus payload.
func TestAudioOutgoingNonPositional(t *testing.T) {
	RegisterMurmelOpusCodec()

	server, wire := net.Pipe()
	defer server.Close()
	defer wire.Close()

	client := &Client{
		Config:       NewConfig(),
		Conn:         NewConn(wire),
		AudioEncoder: getAudioCodec(audioCodecIDOpus).NewEncoder(),
	}

	frames := make(chan voiceFrame, 4)
	errCh := make(chan error, 1)
	go readVoiceFrames(server, frames, errCh)

	out := client.AudioOutgoing()
	out <- AudioBuffer(sinePCM(t, 0))
	close(out)

	select {
	case f := <-frames:
		if f.hasPos {
			t.Fatal("plain AudioOutgoing must not append positional data")
		}
		if !f.final {
			t.Fatal("single-buffer stream must end with the terminator bit")
		}
	case err := <-errCh:
		t.Fatalf("read frame: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for frame")
	}
}

// sinePCM returns k-th phase-shifted 440 Hz PCM frame; encoding happens
// inside writeAudio, which is what these tests exercise.
func sinePCM(t *testing.T, k int) []int16 {
	t.Helper()
	pcm := make(AudioBuffer, AudioDefaultFrameSize)
	for i := range pcm {
		pcm[i] = int16(12000 * math.Sin(2*math.Pi*440*(float64(i+k*AudioDefaultFrameSize))/float64(AudioSampleRate)))
	}
	return pcm
}
