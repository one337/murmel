package varint

import (
	"math"
	"math/rand"
	"testing"
)

// The golden bytes below were cross-checked against the official Mumble
// client's PacketDataStream (src/PacketDataStream.h, mumble-voip/mumble):
//
//	0xxxxxxx                                 7-bit positive
//	10xxxxxx + 1 byte                       14-bit positive
//	110xxxxx + 2 bytes                      21-bit positive
//	1110xxxx + 3 bytes                      28-bit positive
//	111100__ + 4 bytes                      32-bit positive
//	111101__ + 8 bytes                      64-bit
//	111110__ + varint                       negative recursive
//	111111xx                                byte-inverted negative two-bit (~xx)
//
// Documented deviations of this vendored implementation from the official
// client (behavior pinned as-is; both are self-consistent on the wire and
// none of the fields Mumble actually puts in varints — sequence, length,
// session — is ever negative or > 2^31 on this client's paths):
//
//   - values in [2^31, 2^32) use the 9-byte 64-bit form instead of the
//     official 5-byte 32-bit form (gumble compares against MaxInt32, the
//     official client against 2^32-1);
//   - the negative recursive form encodes -value (two's complement
//     negation) where the official client encodes ~value (one's complement);
//     the encodings agree only for the -1..-4 short form, which is the only
//     negative range reachable from real protocol fields.

// TestEncodeEdgeTable pins the exact wire bytes for every range boundary of
// the varint encoding.
func TestEncodeEdgeTable(t *testing.T) {
	tests := []struct {
		name  string
		value int64
		want  []byte
	}{
		{"zero", 0, []byte{0x00}},
		{"one", 1, []byte{0x01}},
		{"max 7-bit", 127, []byte{0x7F}},
		{"min 14-bit", 128, []byte{0x80, 0x80}},
		{"max 14-bit", 16383, []byte{0xBF, 0xFF}},
		{"min 21-bit", 16384, []byte{0xC0, 0x40, 0x00}},
		{"max 21-bit", 2097151, []byte{0xDF, 0xFF, 0xFF}},
		{"min 28-bit", 2097152, []byte{0xE0, 0x20, 0x00, 0x00}},
		{"max 28-bit", 268435455, []byte{0xEF, 0xFF, 0xFF, 0xFF}},
		{"min 32-bit form", 268435456, []byte{0xF0, 0x10, 0x00, 0x00, 0x00}},
		{"max 32-bit form / MaxInt32", math.MaxInt32, []byte{0xF0, 0x7F, 0xFF, 0xFF, 0xFF}},
		// 2^31: first value beyond int32; this implementation switches to
		// the 64-bit form here (official client would keep the 32-bit form
		// up to 2^32-1 — see file comment).
		{"2^31 via 64-bit form", 1 << 31, []byte{0xF4, 0x00, 0x00, 0x00, 0x00, 0x80, 0x00, 0x00, 0x00}},
		{"~2^32 boundary", 1 << 32, []byte{0xF4, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00}},
		{"MaxInt64", math.MaxInt64, []byte{0xF4, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
		// Negative short form — byte-identical to the official client.
		{"minus one", -1, []byte{0xFC}},
		{"minus two", -2, []byte{0xFD}},
		{"minus three", -3, []byte{0xFE}},
		{"minus four", -4, []byte{0xFF}},
		// Negative recursive form (official client emits ~value, i.e. one
		// less — see file comment; pinned as-is).
		{"minus five", -5, []byte{0xF8, 0x05}},
		{"minus 127", -127, []byte{0xF8, 0x7F}},
		{"minus 128", -128, []byte{0xF8, 0x80, 0x80}},
		{"minus 16384", -16384, []byte{0xF8, 0xC0, 0x40, 0x00}},
		{"minus 2097152", -2097152, []byte{0xF8, 0xE0, 0x20, 0x00, 0x00}},
		// P6: MinInt64 used to recurse forever (-MinInt64 overflows back to
		// MinInt64) and panic with index-out-of-range once it walked past
		// the caller's buffer. It now terminates in exactly MaxVarintLen
		// bytes: 0xF8 + the 64-bit form of 2^63.
		{"MinInt64", math.MinInt64, []byte{0xF8, 0xF4, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
		// MinInt64+1 == -MaxInt64: the largest magnitude that still
		// negates cleanly, right next to the P6 special case.
		{"MinInt64 plus one (-MaxInt64)", math.MinInt64 + 1, []byte{0xF8, 0xF4, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf [MaxVarintLen]byte
			n := Encode(buf[:], tt.value)
			if n != len(tt.want) {
				t.Fatalf("Encode(%d) length = %d, want %d", tt.value, n, len(tt.want))
			}
			got := buf[:n]
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("Encode(%d) = % x, want % x", tt.value, got, tt.want)
				}
			}
			if n > MaxVarintLen {
				t.Fatalf("Encode(%d) = %d bytes, exceeds MaxVarintLen %d", tt.value, n, MaxVarintLen)
			}
		})
	}
}

// TestDecodeEdgeTable pins Decode's reading of every form, including values
// Decode accepts but Encode never produces (canonical-form checks).
func TestDecodeEdgeTable(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  int64
		bytes int
	}{
		{"zero", []byte{0x00}, 0, 1},
		{"seven bit", []byte{0x7F}, 127, 1},
		{"fourteen bit", []byte{0x80, 0x80}, 128, 2},
		{"fourteen bit max", []byte{0xBF, 0xFF}, 16383, 2},
		{"twentyone bit min", []byte{0xC0, 0x40, 0x00}, 16384, 3},
		{"twentyone bit max", []byte{0xDF, 0xFF, 0xFF}, 2097151, 3},
		{"twentyeight bit min", []byte{0xE0, 0x20, 0x00, 0x00}, 2097152, 4},
		{"thirtytwo bit form", []byte{0xF0, 0x7F, 0xFF, 0xFF, 0xFF}, math.MaxInt32, 5},
		{"sixtyfour bit form", []byte{0xF4, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00}, 1 << 32, 9},
		{"sixtyfour bit max", []byte{0xF4, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, math.MaxInt64, 9},
		{"negative two-bit", []byte{0xFC}, -1, 1},
		{"negative two-bit min", []byte{0xFF}, -4, 1},
		{"negative recursive", []byte{0xF8, 0x05}, -5, 2},
		{"negative recursive large", []byte{0xF8, 0xC0, 0x40, 0x00}, -16384, 4},
		{"double negative cancels", []byte{0xF8, 0xF8, 0x01}, 1, 3},
		// The 64-bit form holds an unsigned 64-bit payload; payloads above
		// MaxInt64 cannot be represented in the int64 return value and wrap
		// around (documented behavior, not an error).
		{"sixtyfour bit wraps unsigned", []byte{0xF4, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, -1, 9},
		{"sixtyfour bit wraps 2^63", []byte{0xF4, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, math.MinInt64, 9},
		{"P6 MinInt64 encoding", []byte{0xF8, 0xF4, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, math.MinInt64, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, n := Decode(tt.input)
			if n != tt.bytes {
				t.Fatalf("Decode(% x) read %d bytes, want %d", tt.input, n, tt.bytes)
			}
			if got != tt.want {
				t.Fatalf("Decode(% x) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// TestDecodeRejectsTruncated pins the rejection contract: every multi-byte
// form reports (0, 0) unless the full declared width is present. This is the
// only rejection path — every possible first byte belongs to some form, so
// there is no "invalid prefix" case, and no 64-bit magnitude is too large
// (those wrap instead, see TestDecodeEdgeTable).
func TestDecodeRejectsTruncated(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
	}{
		{"empty", nil},
		{"14-bit missing byte", []byte{0x80}},
		{"21-bit missing 2 bytes", []byte{0xC0, 0x40}},
		{"28-bit missing 3 bytes", []byte{0xE0, 0x20, 0x00}},
		{"32-bit form missing 1 byte", []byte{0xF0, 0x7F, 0xFF, 0xFF}},
		{"64-bit form missing 1 byte", []byte{0xF4, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{"negative prefix alone", []byte{0xF8}},
		{"negative inner truncated", []byte{0xF8, 0x80}},
		{"double negative inner truncated", []byte{0xF8, 0xF8, 0xC0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, n := Decode(tt.input)
			if n != 0 || v != 0 {
				t.Fatalf("Decode(% x) = (%d, %d), want (0, 0)", tt.input, v, n)
			}
		})
	}
}

// TestRoundtripProperty is a seeded, deterministic property test: for 10,000
// pseudo-random int64 values drawn from several distributions, Encode followed
// by Decode must reproduce the value and consume exactly the encoded bytes.
// Seed 1; re-runs are reproducible.
func TestRoundtripProperty(t *testing.T) {
	const iterations = 10000

	r := rand.New(rand.NewSource(1))
	randValue := func() int64 {
		switch r.Intn(4) {
		case 0: // uniform non-negative full range
			return r.Int63()
		case 1: // full signed range via random sign
			v := r.Int63()
			if r.Intn(2) == 0 {
				return -v
			}
			return v
		case 2: // small magnitudes around the range boundaries
			v := int64(r.Intn(300) - 150)
			for _, base := range []int64{127, 128, 16383, 16384, 2097151, 2097152} {
				if r.Intn(2) == 0 {
					v = base + int64(r.Intn(3)-1)
					break
				}
			}
			if r.Intn(2) == 0 {
				return -v
			}
			return v
		default: // powers of two +/- 1, either sign
			v := int64(1) << uint(r.Intn(63))
			switch r.Intn(3) {
			case 0:
				v--
			case 1:
				v++
			}
			if r.Intn(2) == 0 {
				return -v
			}
			return v
		}
	}

	for i := 0; i < iterations; i++ {
		value := randValue()

		var buf [MaxVarintLen]byte
		n := Encode(buf[:], value)
		if n == 0 {
			t.Fatalf("iteration %d: Encode(%d) returned 0", i, value)
		}
		if n > MaxVarintLen {
			t.Fatalf("iteration %d: Encode(%d) used %d bytes > MaxVarintLen", i, value, n)
		}

		decoded, m := Decode(buf[:n])
		if m != n {
			t.Fatalf("iteration %d: Decode of Encode(%d) consumed %d bytes, want %d (% x)", i, value, m, n, buf[:n])
		}
		if decoded != value {
			t.Fatalf("iteration %d: roundtrip of %d gave %d (% x)", i, value, decoded, buf[:n])
		}
	}
}

// TestEncodeMinInt64Terminates is the P6 regression test: Encode(MinInt64)
// used to recurse infinitely (negating MinInt64 overflows back to MinInt64)
// and died with an index-out-of-range panic once it ran off the end of the
// caller's buffer.
func TestEncodeMinInt64Terminates(t *testing.T) {
	for _, value := range []int64{math.MinInt64, math.MinInt64 + 1} {
		var buf [MaxVarintLen]byte
		n := Encode(buf[:], value)
		if n != MaxVarintLen {
			t.Fatalf("Encode(%d) = %d bytes, want %d", value, n, MaxVarintLen)
		}
		v, m := Decode(buf[:n])
		if m != n || v != value {
			t.Fatalf("roundtrip of %d = (%d, %d bytes), want (%d, %d bytes)", value, v, m, value, n)
		}
	}
}
