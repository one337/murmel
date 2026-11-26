package crypt

import (
	"bytes"
	"testing"
)

// FuzzDecrypt feeds arbitrary packets to a receiver; each is either
// rejected or yields a plaintext four bytes shorter than the packet.
func FuzzDecrypt(f *testing.F) {
	f.Add([]byte{0x56, 1, 2, 3, 4, 5}, []byte{0x55, 0, 1})
	f.Add([]byte{0, 0, 0}, []byte{})
	f.Fuzz(func(t *testing.T, packet, more []byte) {
		cs := New()
		if !cs.SetKey(rawKeyKAT, nonceKAT, bytes.Repeat([]byte{0x55}, IVSize)) {
			t.Fatal("SetKey failed")
		}
		// The second packet runs against the state the first one left.
		for _, p := range [][]byte{packet, more, packet} {
			plain, err := cs.Decrypt(p)
			if err != nil {
				continue
			}
			if len(plain) != len(p)-4 {
				t.Fatalf("Decrypt(%x) returned %d bytes", p, len(plain))
			}
		}
	})
}

// FuzzEncryptDecrypt sends a plaintext from one CryptState to its peer;
// the peer recovers it, except for the one bit Encrypt flips to defeat
// the XEX* attack.
func FuzzEncryptDecrypt(f *testing.F) {
	f.Add([]byte("abcdefghi\x00"), byte(0))
	f.Add(make([]byte, 2*blockSize), byte(0xff))
	f.Add(append(make([]byte, blockSize-1), 1, 2, 3), byte(0xfe))
	f.Fuzz(func(t *testing.T, plain []byte, ivStart byte) {
		sendIV := bytes.Repeat([]byte{ivStart}, IVSize)
		recvIV := bytes.Repeat([]byte{0x33}, IVSize)
		sender, receiver := New(), New()
		if !sender.SetKey(rawKeyKAT, sendIV, recvIV) || !receiver.SetKey(rawKeyKAT, recvIV, sendIV) {
			t.Fatal("SetKey failed")
		}
		packet, err := sender.Encrypt(plain)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		got, err := receiver.Decrypt(packet)
		if err != nil {
			t.Fatalf("Decrypt of a genuine packet: %v", err)
		}
		if want := xexStarAdjusted(plain); !bytes.Equal(got, want) {
			t.Fatalf("plaintext %x came back as %x", plain, got)
		}
		if s := receiver.Stats(); s.Good != 1 || s.DecryptFailed != 0 {
			t.Fatalf("stats after one genuine packet: %+v", s)
		}
	})
}

// xexStarAdjusted returns plain as the receiver sees it: ocbEncrypt flips
// the lowest bit of the second-to-last full block when its first 15 bytes
// are zero.
func xexStarAdjusted(plain []byte) []byte {
	out := bytes.Clone(plain)
	if len(out) <= blockSize {
		return out
	}
	last := (len(out) - 1) / blockSize * blockSize // start of the final block
	prev := out[last-blockSize : last]
	if bytes.Equal(prev[:blockSize-1], make([]byte, blockSize-1)) {
		prev[0] ^= 1
	}
	return out
}
