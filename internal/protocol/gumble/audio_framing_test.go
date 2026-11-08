package gumble

import (
	"encoding/binary"
	"io"
	"math"
	"net"
	"strconv"
	"testing"
	"time"

	"io.github/murmel/internal/protocol/gumble/varint"
)

// Framing contract locked by this file (Mumble UDP/TCP voice packets, cross-
// checked against the official client: PacketDataStream.h, AudioInput.cpp and
// Mumble.proto "UDPTunnel" in mumble-voip/mumble):
//
//	TCP: [uint16 BE type=1 UDPTunnel][uint32 BE length][voice packet]
//	voice packet:  (codec << 5) | target   — Opus = codec 4, target 0 normal,
//	                                             1..30 whisper ids, 31 loopback
//	               varint sequence          — wraps at 2^31-1 (32-bit counter)
//	               varint length | 0x2000   — bit 13 set = terminator frame
//	               <length> Opus payload bytes
//	               [3x float32 LE X,Y,Z]    — only for positional streams
//
// The voice packet body must parse standalone: T9 (UDP transport) sends the
// same bytes as the datagram payload, without the 6-byte TCP message header.

// udpVoicePacket is a parsed standalone voice packet (what a UDP datagram or
// a UDPTunnel payload decodes to).
type udpVoicePacket struct {
	headerByte byte
	seq        int64
	final      bool
	payload    []byte
	hasPos     bool
	x, y, z    float32
}

// parseUDPVoicePacket parses a complete standalone voice packet. It fails if
// bytes remain after the positional data (or if a truncated varint or a
// position block of the wrong size is found) — a datagram must be consumed
// exactly.
func parseUDPVoicePacket(b []byte) (udpVoicePacket, error) {
	var p udpVoicePacket
	if len(b) == 0 {
		return p, io.ErrUnexpectedEOF
	}
	p.headerByte = b[0]
	b = b[1:]

	var n int
	p.seq, n = varint.Decode(b)
	if n == 0 {
		return p, io.ErrUnexpectedEOF
	}
	b = b[n:]

	length, n := varint.Decode(b)
	if n == 0 {
		return p, io.ErrUnexpectedEOF
	}
	b = b[n:]
	p.final = length&0x2000 != 0
	length &^= 0x2000
	if length < 0 || length > int64(len(b)) {
		return p, io.ErrUnexpectedEOF
	}
	p.payload = b[:length]
	b = b[length:]

	switch len(b) {
	case 0:
	case 3 * 4:
		p.hasPos = true
		p.x = math.Float32frombits(binary.LittleEndian.Uint32(b))
		p.y = math.Float32frombits(binary.LittleEndian.Uint32(b[4:]))
		p.z = math.Float32frombits(binary.LittleEndian.Uint32(b[8:]))
	default:
		return p, io.ErrUnexpectedEOF
	}
	return p, nil
}

// rawPacket is one TCP control message exactly as it appeared on the wire.
type rawPacket struct {
	pType uint16
	body  []byte
}

// framingConn returns a Client whose Conn writes to a net.Pipe drained by a
// reader goroutine started before the client is handed out. net.Pipe is
// synchronous: every Write blocks until a reader consumes it, so a test that
// calls WriteAudio and only afterwards reads from the pipe deadlocks — the
// reader-first pattern from the positional dispatch tests is mandatory here
// too. Written packets come back byte-exact through the returned channel.
//
// The goroutine exits when the pipe is closed by t.Cleanup; no test writes
// more packets than the channel buffers.
func framingConn(t *testing.T) (client *Client, packets <-chan rawPacket) {
	t.Helper()
	RegisterMurmelOpusCodec()
	server, wire := net.Pipe()
	t.Cleanup(func() { server.Close(); wire.Close() })
	client = &Client{
		Config: NewConfig(),
		Conn:   NewConn(wire),
	}

	ch := make(chan rawPacket, 16)
	go func() {
		for {
			var header [6]byte
			if _, err := io.ReadFull(server, header[:]); err != nil {
				return
			}
			body := make([]byte, binary.BigEndian.Uint32(header[2:]))
			if _, err := io.ReadFull(server, body); err != nil {
				return
			}
			ch <- rawPacket{
				pType: binary.BigEndian.Uint16(header[:]),
				body:  body,
			}
		}
	}()
	return client, ch
}

// framingEncoderConn is framingConn with an Opus encoder attached, for tests
// that drive the AudioBuffer.writeAudio codec path instead of the raw
// Conn.WriteAudio framing seam.
func framingEncoderConn(t *testing.T) (client *Client, packets <-chan rawPacket) {
	t.Helper()
	client, packets = framingConn(t)
	client.AudioEncoder = getAudioCodec(audioCodecIDOpus).NewEncoder()
	return client, packets
}

// readPacket waits for the next packet the drain goroutine observed on the
// wire. The timeout turns a missing write into a test failure instead of the
// deadlock a synchronous pipe would otherwise produce.
func readPacket(t *testing.T, packets <-chan rawPacket) (uint16, []byte) {
	t.Helper()
	select {
	case p := <-packets:
		return p.pType, p.body
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a packet on the wire")
		return 0, nil // not reached
	}
}

// TestWriteAudioGoldenNonPositional pins the exact bytes of a plain Opus
// voice packet inside its UDPTunnel envelope.
func TestWriteAudioGoldenNonPositional(t *testing.T) {
	client, packets := framingConn(t)

	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if err := client.Conn.WriteAudio(byte(4), 0, 0, false, payload, nil, nil, nil); err != nil {
		t.Fatalf("WriteAudio: %v", err)
	}

	pType, body := readPacket(t, packets)
	if pType != 1 {
		t.Fatalf("message type = %d, want 1 (UDPTunnel)", pType)
	}
	want := []byte{
		0x80, // Opus (4) << 5 | target 0
		0x00, // sequence 0
		0x04, // length 4, no terminator bit
		0xDE, 0xAD, 0xBE, 0xEF,
	}
	if uint32(len(body)) != 7 {
		t.Fatalf("declared/actual body length = %d, want 7", len(body))
	}
	if string(body) != string(want) {
		t.Fatalf("body:\n got: % x\nwant: % x", body, want)
	}
}

// TestWriteAudioHeaderByteTargetIDs verifies the (codec << 5) | target
// header byte for every target class: 0 normal speech, 1..30 whisper
// targets, 31 server loopback (VoiceTargetLoopback.ID).
func TestWriteAudioHeaderByteTargetIDs(t *testing.T) {
	for _, target := range []byte{0, 1, 5, 30, 31} {
		t.Run(targetName(target), func(t *testing.T) {
			client, packets := framingConn(t)

			if err := client.Conn.WriteAudio(byte(4), target, 0, false, []byte{0x01}, nil, nil, nil); err != nil {
				t.Fatalf("WriteAudio: %v", err)
			}
			_, body := readPacket(t, packets)

			p, err := parseUDPVoicePacket(body)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if want := byte(4)<<5 | target; p.headerByte != want {
				t.Fatalf("header byte = %#02x, want (4<<5)|%d = %#02x", p.headerByte, target, want)
			}
		})
	}
}

func targetName(target byte) string {
	switch target {
	case 0:
		return "normal"
	case 31:
		return "loopback"
	default:
		return "whisper"
	}
}

// TestWriteAudioSequenceVarints pins the sequence varint bytes for the
// encoding boundaries that matter on the wire, including the stream-level
// wraparound ceiling 2^31-1 (the client increments sequence modulo
// math.MaxInt32, mirroring the official 32-bit counter).
func TestWriteAudioSequenceVarints(t *testing.T) {
	tests := []struct {
		seq     int64
		wantSeq []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7F}},
		{128, []byte{0x80, 0x80}},
		{16383, []byte{0xBF, 0xFF}},
		{16384, []byte{0xC0, 0x40, 0x00}},
		{2097151, []byte{0xDF, 0xFF, 0xFF}},
		{2147483647, []byte{0xF0, 0x7F, 0xFF, 0xFF, 0xFF}}, // math.MaxInt32 wrap ceiling
	}

	for _, tt := range tests {
		t.Run(strconv.FormatInt(tt.seq, 10), func(t *testing.T) {
			client, packets := framingConn(t)

			if err := client.Conn.WriteAudio(byte(4), 0, tt.seq, false, []byte{0x42}, nil, nil, nil); err != nil {
				t.Fatalf("WriteAudio: %v", err)
			}
			_, body := readPacket(t, packets)

			want := append([]byte{0x80}, tt.wantSeq...)
			want = append(want, 0x01, 0x42)
			if len(body) != len(want) {
				t.Fatalf("body = % x, want % x", body, want)
			}
			for i := range want {
				if body[i] != want[i] {
					t.Fatalf("seq %d: body = % x, want % x", tt.seq, body, want)
				}
			}

			p, err := parseUDPVoicePacket(body)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if p.seq != tt.seq {
				t.Fatalf("parsed sequence = %d, want %d", p.seq, tt.seq)
			}
		})
	}
}

// TestWriteAudioTerminatorFlag pins the 0x2000 bit in the length varint:
// set exactly when final == true. With a 4-byte payload the length varint is
// 0x2004 -> 0xA0 0x04; an empty final frame carries the bare terminator
// 0x2000 -> 0xA0 0x00 (Conn.WriteAudio frames it; the writeAudio codec path
// rejects empty buffers before they ever reach the wire, see
// TestWriteAudioRejectsEmptyBuffer).
func TestWriteAudioTerminatorFlag(t *testing.T) {
	tests := []struct {
		name    string
		final   bool
		payload []byte
		wantLen []byte
	}{
		{"mid-stream", false, []byte{0xDE, 0xAD, 0xBE, 0xEF}, []byte{0x04}},
		{"final", true, []byte{0xDE, 0xAD, 0xBE, 0xEF}, []byte{0xA0, 0x04}},
		{"final single byte", true, []byte{0x01}, []byte{0xA0, 0x01}},
		{"final empty", true, nil, []byte{0xA0, 0x00}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, packets := framingConn(t)

			if err := client.Conn.WriteAudio(byte(4), 0, 0, tt.final, tt.payload, nil, nil, nil); err != nil {
				t.Fatalf("WriteAudio: %v", err)
			}
			_, body := readPacket(t, packets)

			p, err := parseUDPVoicePacket(body)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if p.final != tt.final {
				t.Fatalf("terminator bit = %v, want %v (body % x)", p.final, tt.final, body)
			}
			if len(p.payload) != len(tt.payload) {
				t.Fatalf("payload length = %d, want %d", len(p.payload), len(tt.payload))
			}

			want := append([]byte{0x80, 0x00}, tt.wantLen...)
			want = append(want, tt.payload...)
			if string(body) != string(want) {
				t.Fatalf("body = % x, want % x", body, want)
			}
		})
	}
}

// TestWriteAudioPositional pins byte-for-byte the positional audio layout:
// the three coordinate float32s follow the Opus payload, little-endian, X
// then Y then Z. The first case uses hand-computed literal goldens; the
// coordinate table constructs expected bytes independently via
// math.Float32bits + LittleEndian (the same construction the Mumble client
// performs in PacketDataStream::operator<<(float)).
func TestWriteAudioPositional(t *testing.T) {
	client, packets := framingConn(t)

	x, y, z := float32(1.5), float32(-2.25), float32(100.125)
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if err := client.Conn.WriteAudio(byte(4), 0, 0, true, payload, &x, &y, &z); err != nil {
		t.Fatalf("WriteAudio: %v", err)
	}

	pType, body := readPacket(t, packets)
	if pType != 1 {
		t.Fatalf("message type = %d, want 1 (UDPTunnel)", pType)
	}
	if uint32(len(body)) != 20 {
		t.Fatalf("declared/actual body length = %d, want 20 (header+seq+len+payload+position)", len(body))
	}
	want := []byte{
		0x80,       // Opus, target 0
		0x00,       // sequence 0
		0xA0, 0x04, // length 4 | terminator 0x2000
		0xDE, 0xAD, 0xBE, 0xEF,
		0x00, 0x00, 0xC0, 0x3F, // X = 1.5f       (0x3FC00000 LE)
		0x00, 0x00, 0x10, 0xC0, // Y = -2.25f     (0xC0100000 LE)
		0x00, 0x40, 0xC8, 0x42, // Z = 100.125f   (0x42C84000 LE)
	}
	if string(body) != string(want) {
		t.Fatalf("body:\n got: % x\nwant: % x", body, want)
	}

	p, err := parseUDPVoicePacket(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !p.hasPos || p.x != x || p.y != y || p.z != z {
		t.Fatalf("parsed position = (%v,%v,%v) hasPos=%v, want (%v,%v,%v)", p.x, p.y, p.z, p.hasPos, x, y, z)
	}

	coordinateTable := []struct {
		name       string
		cx, cy, cz float32
	}{
		{"zeros", 0, 0, 0},
		{"negative zero", float32(math.Copysign(0, -1)), 0, 0},
		{"negative unit", -1, -1, -1},
		{"large meters", 1.0e6, -1.0e6, 5.5e5},
		{"sub-unit", 0.001, -0.001, 0.25},
	}
	for _, tt := range coordinateTable {
		t.Run(tt.name, func(t *testing.T) {
			client, packets := framingConn(t)

			if err := client.Conn.WriteAudio(byte(4), 0, 7, false, []byte{0x11}, &tt.cx, &tt.cy, &tt.cz); err != nil {
				t.Fatalf("WriteAudio: %v", err)
			}
			_, body := readPacket(t, packets)

			want := []byte{0x80, 0x07, 0x01, 0x11}
			for _, f := range []float32{tt.cx, tt.cy, tt.cz} {
				var le [4]byte
				binary.LittleEndian.PutUint32(le[:], math.Float32bits(f))
				want = append(want, le[:]...)
			}
			if string(body) != string(want) {
				t.Fatalf("body:\n got: % x\nwant: % x", body, want)
			}
		})
	}
}

// TestWriteAudioPositionalGatedOnX documents that the 12 trailing bytes are
// emitted only when X is non-nil: a Y/Z value without X is ignored (nil X
// disables positional audio for the stream).
func TestWriteAudioPositionalGatedOnX(t *testing.T) {
	client, packets := framingConn(t)

	y := float32(-2.25)
	if err := client.Conn.WriteAudio(byte(4), 0, 0, true, []byte{0x01}, nil, &y, &y); err != nil {
		t.Fatalf("WriteAudio: %v", err)
	}
	_, body := readPacket(t, packets)

	want := []byte{0x80, 0x00, 0xA0, 0x01, 0x01}
	if string(body) != string(want) {
		t.Fatalf("body = % x, want % x (no positional bytes without X)", body, want)
	}
}

// TestWriteAudioTCPTunnelUDPParity locks the T9 contract: the UDPTunnel
// body must be a complete, standalone voice packet — the same bytes a UDP
// socket will later send as the (plaintext) datagram payload. The declared
// TCP length must equal the body length, and parsing the body standalone
// must consume it exactly with all fields intact.
func TestWriteAudioTCPTunnelUDPParity(t *testing.T) {
	x, y, z := float32(3.25), float32(0), float32(-9.5)
	cases := []struct {
		name    string
		seq     int64
		final   bool
		payload []byte
		pos     *[3]float32
	}{
		{"minimal", 0, false, []byte{0x42}, nil},
		{"final", 0, true, []byte{0x42}, nil},
		{"large seq", 2097152, false, []byte{0x01, 0x02}, nil},
		{"positional final", 4242, true, []byte{0xAA, 0xBB, 0xCC}, &[3]float32{x, y, z}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			client, packets := framingConn(t)

			var cx, cy, cz *float32
			if tt.pos != nil {
				cx, cy, cz = &tt.pos[0], &tt.pos[1], &tt.pos[2]
			}
			if err := client.Conn.WriteAudio(byte(4), 1, tt.seq, tt.final, tt.payload, cx, cy, cz); err != nil {
				t.Fatalf("WriteAudio: %v", err)
			}

			pType, body := readPacket(t, packets)
			if pType != 1 {
				t.Fatalf("message type = %d, want 1", pType)
			}

			p, err := parseUDPVoicePacket(body)
			if err != nil {
				t.Fatalf("body is not a standalone UDP voice packet: %v (% x)", err, body)
			}
			if p.headerByte != byte(4)<<5|1 {
				t.Fatalf("header byte = %#02x, want %#02x", p.headerByte, byte(4)<<5|1)
			}
			if p.seq != tt.seq {
				t.Fatalf("sequence = %d, want %d", p.seq, tt.seq)
			}
			if p.final != tt.final {
				t.Fatalf("terminator = %v, want %v", p.final, tt.final)
			}
			if string(p.payload) != string(tt.payload) {
				t.Fatalf("payload = % x, want % x", p.payload, tt.payload)
			}
			if tt.pos != nil {
				if !p.hasPos || p.x != x || p.y != y || p.z != z {
					t.Fatalf("position = (%v,%v,%v) hasPos=%v, want (%v,%v,%v)", p.x, p.y, p.z, p.hasPos, x, y, z)
				}
			} else if p.hasPos {
				t.Fatal("unexpected positional bytes in non-positional packet")
			}
		})
	}
}

// TestWriteAudioStreamSequencing drives the full AudioOutgoing stream path:
// three buffers produce sequence varints 0, 1, 2, the terminator bit only on
// the last frame, and the Opus codec byte in every header. The pipe drain
// goroutine is what lets the AudioOutgoingPositional writer goroutine make
// progress while the buffers are still being fed into the channel.
func TestWriteAudioStreamSequencing(t *testing.T) {
	client, packets := framingEncoderConn(t)

	out := client.AudioOutgoing()
	out <- AudioBuffer(sinePCM(t, 0))
	out <- AudioBuffer(sinePCM(t, 1))
	out <- AudioBuffer(sinePCM(t, 2))
	close(out)

	wantSeq := []int64{0, 1, 2}
	for i, seq := range wantSeq {
		pType, body := readPacket(t, packets)
		if pType != 1 {
			t.Fatalf("frame %d: message type = %d, want 1", i, pType)
		}
		p, err := parseUDPVoicePacket(body)
		if err != nil {
			t.Fatalf("frame %d: parse: %v", i, err)
		}
		if p.headerByte != byte(4)<<5 {
			t.Fatalf("frame %d: header byte = %#02x, want %#02x", i, p.headerByte, byte(4)<<5)
		}
		if p.seq != seq {
			t.Fatalf("frame %d: sequence = %d, want %d", i, p.seq, seq)
		}
		if len(p.payload) == 0 {
			t.Fatalf("frame %d: empty Opus payload", i)
		}
		if last := i == len(wantSeq)-1; p.final != last {
			t.Fatalf("frame %d: terminator = %v, want %v", i, p.final, last)
		}
		if p.hasPos {
			t.Fatalf("frame %d: unexpected positional bytes", i)
		}
	}
}

// TestWriteAudioVoiceTargetByte drives writeAudio (not just Conn.WriteAudio)
// with a registered VoiceTarget: the target id must land in the low five
// bits of the header byte.
func TestWriteAudioVoiceTargetByte(t *testing.T) {
	for _, tt := range []struct {
		name   string
		target *VoiceTarget
		want   byte
	}{
		{"no target", nil, 0x80},
		{"whisper id 5", &VoiceTarget{ID: 5}, 0x80 | 0x05},
		{"loopback", VoiceTargetLoopback, 0x80 | 0x1F},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, packets := framingEncoderConn(t)
			client.VoiceTarget = tt.target

			if err := (AudioBuffer(sinePCM(t, 0))).writeAudio(client, 0, true, nil, nil, nil); err != nil {
				t.Fatalf("writeAudio: %v", err)
			}

			pType, body := readPacket(t, packets)
			if pType != 1 {
				t.Fatalf("message type = %d, want 1", pType)
			}
			p, err := parseUDPVoicePacket(body)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if p.headerByte != tt.want {
				t.Fatalf("header byte = %#02x, want %#02x", p.headerByte, tt.want)
			}
		})
	}
}

// TestWriteAudioRejectsEmptyBuffer pins the zero-payload rejection: the
// codec seam refuses an empty AudioBuffer and nothing reaches the wire.
func TestWriteAudioRejectsEmptyBuffer(t *testing.T) {
	client, packets := framingEncoderConn(t)

	if err := (AudioBuffer{}).writeAudio(client, 0, true, nil, nil, nil); err == nil {
		t.Fatal("writeAudio accepted an empty buffer")
	}

	assertWireEmpty(t, packets)
}

// TestWriteAudioFrameSizeBoundaries pins the frame-size contract around the
// 60 ms maximum: 10 ms (default) and exactly 60 ms (AudioMaximumFrameSize)
// encode and hit the wire, 60 ms + 1 sample is rejected by the Opus codec
// (libopus only accepts 2.5/5/10/20/40/60 ms frames) and nothing is sent.
// The 60 ms frame needs a raised AudioDataBytes budget: the 40-byte default
// caps ~10 ms frames at the default bitrate.
func TestWriteAudioFrameSizeBoundaries(t *testing.T) {
	if AudioMaximumFrameSize != AudioSampleRate/1000*60 {
		t.Fatalf("AudioMaximumFrameSize = %d, want %d (60 ms at %d Hz)", AudioMaximumFrameSize, AudioSampleRate/1000*60, AudioSampleRate)
	}
	if AudioMaximumFrameSize != 2880 {
		t.Fatalf("AudioMaximumFrameSize = %d, want 2880 samples (60 ms at 48 kHz)", AudioMaximumFrameSize)
	}

	cases := []struct {
		name      string
		samples   int
		dataBytes int
		ok        bool
	}{
		{"10 ms", AudioDefaultFrameSize, AudioDefaultDataBytes, true},
		{"60 ms", AudioMaximumFrameSize, 1024, true},
		{"60 ms plus one sample", AudioMaximumFrameSize + 1, 1024, false},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			client, packets := framingEncoderConn(t)
			client.Config.AudioDataBytes = tt.dataBytes

			pcm := make(AudioBuffer, tt.samples)
			for i := range pcm {
				pcm[i] = int16(9000 * math.Sin(2*math.Pi*440*float64(i)/float64(AudioSampleRate)))
			}
			err := pcm.writeAudio(client, 0, false, nil, nil, nil)

			if !tt.ok {
				if err == nil {
					t.Fatal("writeAudio accepted an over-length frame")
				}
				assertWireEmpty(t, packets)
				return
			}
			if err != nil {
				t.Fatalf("writeAudio: %v", err)
			}

			pType, body := readPacket(t, packets)
			if pType != 1 {
				t.Fatalf("message type = %d, want 1", pType)
			}
			p, err := parseUDPVoicePacket(body)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(p.payload) == 0 {
				t.Fatal("no Opus payload on the wire")
			}
			if len(p.payload) > tt.dataBytes {
				t.Fatalf("payload %d bytes exceeds AudioDataBytes budget %d", len(p.payload), tt.dataBytes)
			}
		})
	}
}

// TestMumbleWireConstants documents the fork constants against the official
// client: Opus is codec type 4, the terminator bit is bit 13 of the length
// varint, the header byte packs 3 bits of codec and 5 bits of target.
func TestMumbleWireConstants(t *testing.T) {
	if audioCodecIDOpus != 4 {
		t.Fatalf("Opus codec id = %d, want 4 (official Message.h)", audioCodecIDOpus)
	}
	const terminatorBit = 0x2000
	if terminatorBit != 1<<13 {
		t.Fatal("terminator bit must be bit 13")
	}
	for codec := byte(0); codec < 8; codec++ {
		for target := byte(0); target < 32; target++ {
			h := codec<<5 | target
			if h>>5 != codec || h&0x1F != target {
				t.Fatalf("header layout broken for codec %d target %d", codec, target)
			}
		}
	}
	if id := VoiceTargetLoopback.ID; id != 31 {
		t.Fatalf("VoiceTargetLoopback.ID = %d, want 31", id)
	}
}

// assertWireEmpty fails if any packet is observed on the wire within a short
// window (the timeout proves nothing was written).
func assertWireEmpty(t *testing.T, packets <-chan rawPacket) {
	t.Helper()
	select {
	case p := <-packets:
		t.Fatalf("expected an empty wire, got type %d body % x", p.pType, p.body)
	case <-time.After(150 * time.Millisecond):
	}
}
