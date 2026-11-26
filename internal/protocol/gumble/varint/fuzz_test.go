package varint

import (
	"math"
	"testing"
)

// FuzzDecode checks that Decode never reads past its input and that every
// value it accepts encodes back to the same value.
func FuzzDecode(f *testing.F) {
	f.Add([]byte{0x7f})
	f.Add([]byte{0xbf, 0xff})
	f.Add([]byte{0xf0, 0x7f, 0xff, 0xff, 0xff})
	f.Add([]byte{0xf4, 0x80, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{0xf8, 0xf8, 0xfd})
	f.Add([]byte{0xfe})
	f.Fuzz(func(t *testing.T, b []byte) {
		v, n := Decode(b)
		if n < 0 || n > len(b) {
			t.Fatalf("Decode(%x) read %d bytes", b, n)
		}
		if n == 0 {
			return
		}
		var buf [MaxVarintLen]byte
		m := Encode(buf[:], v)
		if w, k := Decode(buf[:m]); w != v || k != m {
			t.Fatalf("Decode(%x) = %d, which encodes to %x and decodes to %d (%d of %d bytes)", b, v, buf[:m], w, k, m)
		}
	})
}

// FuzzEncode checks that every value survives Encode and Decode and that
// Decode consumes exactly the bytes Encode wrote.
func FuzzEncode(f *testing.F) {
	for _, v := range []int64{0, -1, -4, -5, 0x7f, 0x80, 0x3fff, 0x4000, 0x1fffff, 0xfffffff, math.MaxInt32, math.MaxInt32 + 1, math.MaxInt64, math.MinInt64} {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, v int64) {
		var buf [MaxVarintLen]byte
		m := Encode(buf[:], v)
		if m < 1 || m > MaxVarintLen {
			t.Fatalf("Encode(%d) wrote %d bytes", v, m)
		}
		if w, k := Decode(buf[:m]); w != v || k != m {
			t.Fatalf("Encode(%d) = %x decodes to %d (%d of %d bytes)", v, buf[:m], w, k, m)
		}
	})
}
