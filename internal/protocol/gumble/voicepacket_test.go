package gumble

import (
	"testing"

	"github.com/one337/murmel/internal/protocol/gumble/varint"
)

// voicePacketWithSize builds a voice packet for session 42 whose size field
// carries the raw bytes size, followed by payload.
func voicePacketWithSize(size, payload []byte) []byte {
	b := []byte{byte(audioCodecIDOpus) << 5, 42, 0}
	b = append(b, size...)
	return append(b, payload...)
}

func encodeVarint(v int64) []byte {
	var b [varint.MaxVarintLen]byte
	return b[:varint.Encode(b[:], v)]
}

func newVoicePacketTestClient() *Client {
	RegisterMurmelOpusCodec()
	c := &Client{
		Config:     NewConfig(),
		Users:      make(Users),
		audioCodec: getAudioCodec(audioCodecIDOpus),
		audioQueue: make(chan *audioJob, 1),
	}
	c.Users[42] = &User{Session: 42, client: c}
	return c
}

// TestHandleVoicePacketFrameSize checks that only the 13 size bits of the
// frame size field count, and that a frame larger than the packet is
// dropped instead of reaching the decoder.
func TestHandleVoicePacketFrameSize(t *testing.T) {
	payload := []byte{0xf8, 0xff, 0xfe}
	tests := []struct {
		name   string
		size   []byte
		accept bool
		length int
	}{
		{"exact size", encodeVarint(3), true, 3},
		{"last frame", encodeVarint(0x2003), true, 3},
		{"bits above the size are ignored", encodeVarint(0x4003), true, 3},
		{"larger than the packet", encodeVarint(0x1000), false, 0},
		{"negative, short form", encodeVarint(-1), false, 0},
		{"negative, long form", []byte{0xf8, 0x03}, false, 0},
		{"negative 64-bit", []byte{0xf4, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xe0, 0x03}, true, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newVoicePacketTestClient()
			err := c.handleVoicePacket(voicePacketWithSize(tt.size, payload))
			if !tt.accept {
				if err == nil {
					job := <-c.audioQueue
					t.Fatalf("packet accepted with frame length %d", job.audioLength)
				}
				return
			}
			if err != nil {
				t.Fatalf("packet rejected: %v", err)
			}
			if job := <-c.audioQueue; job.audioLength != tt.length {
				t.Fatalf("frame length %d, want %d", job.audioLength, tt.length)
			}
		})
	}
}

// FuzzHandleVoicePacket feeds arbitrary voice packets through parsing and
// decoding; neither may panic.
func FuzzHandleVoicePacket(f *testing.F) {
	f.Add(voicePacketWithSize(encodeVarint(3), []byte{0xf8, 0xff, 0xfe}))
	f.Add(voicePacketWithSize(encodeVarint(0x2001), []byte{0xf8, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}))
	f.Fuzz(func(t *testing.T, packet []byte) {
		c := newVoicePacketTestClient()
		if c.handleVoicePacket(packet) != nil {
			return
		}
		c.dispatchAudio(<-c.audioQueue)
	})
}
