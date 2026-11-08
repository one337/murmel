package crypt

import (
	"bytes"
	"testing"
)

// rawKeyKAT is the key from TestCrypt::testvectors (draft-krovetz-ocb-00.txt):
// { 0x00, 0x01, ..., 0x0f }.
var rawKeyKAT = []byte{
	0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
	0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
}

// nonceKAT is the nonce from TestCrypt::authcrypt / xexstarAttack / tamper:
// { 0xff, 0xee, ..., 0x00 }.
var nonceKAT = []byte{
	0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88,
	0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00,
}

func newKATState(t *testing.T) *CryptState {
	t.Helper()
	cs := New()
	if !cs.SetKey(rawKeyKAT, rawKeyKAT, rawKeyKAT) {
		t.Fatal("SetKey with official vector lengths failed")
	}
	return cs
}

// TestOCB2Vectors reproduces the known-answer vectors the official suite
// runs in TestCrypt::testvectors against CryptStateOCB2::ocb_encrypt.
func TestOCB2Vectors(t *testing.T) {
	cs := newKATState(t)

	// Empty plaintext: tag must equal blanktag.
	_, tag, ok := cs.ocbEncrypt(nil, rawKeyKAT, true)
	if !ok {
		t.Fatal("ocb_encrypt of empty plaintext failed")
	}
	wantBlankTag := []byte{
		0xBF, 0x31, 0x08, 0x13, 0x07, 0x73, 0xAD, 0x5E,
		0xC7, 0x0E, 0xC6, 0x9E, 0x78, 0x75, 0xA7, 0xB0,
	}
	if !bytes.Equal(tag, wantBlankTag) {
		t.Errorf("empty-plaintext tag mismatch:\n got %x\nwant %x", tag, wantBlankTag)
	}

	// 40-byte plaintext {0..39}: ciphertext and tag must match exactly.
	source := make([]byte, 40)
	for i := range source {
		source[i] = byte(i)
	}
	crypted, tag, ok := cs.ocbEncrypt(source, rawKeyKAT, true)
	if !ok {
		t.Fatal("ocb_encrypt of 40-byte plaintext failed")
	}
	wantCrypted := []byte{
		0xF7, 0x5D, 0x6B, 0xC8, 0xB4, 0xDC, 0x8D, 0x66, 0xB8, 0x36,
		0xA2, 0xB0, 0x8B, 0x32, 0xA6, 0x36, 0x9F, 0x1C, 0xD3, 0xC5,
		0x22, 0x8D, 0x79, 0xFD, 0x6C, 0x26, 0x7F, 0x5F, 0x6A, 0xA7,
		0xB2, 0x31, 0xC7, 0xDF, 0xB9, 0xD5, 0x99, 0x51, 0xAE, 0x9C,
	}
	wantLongTag := []byte{
		0x9D, 0xB0, 0xCD, 0xF8, 0x80, 0xF7, 0x3E, 0x3E,
		0x10, 0xD4, 0xEB, 0x32, 0x17, 0x76, 0x66, 0x88,
	}
	if !bytes.Equal(crypted, wantCrypted) {
		t.Errorf("40-byte ciphertext mismatch:\n got %x\nwant %x", crypted, wantCrypted)
	}
	if !bytes.Equal(tag, wantLongTag) {
		t.Errorf("40-byte tag mismatch:\n got %x\nwant %x", tag, wantLongTag)
	}

	// Decrypting the official ciphertext must yield the official plaintext
	// and tag (inverse-direction check on the same vectors).
	plain, decTag, ok := cs.ocbDecrypt(wantCrypted, rawKeyKAT)
	if !ok {
		t.Fatal("ocb_decrypt of official ciphertext failed")
	}
	if !bytes.Equal(plain, source) {
		t.Errorf("decrypted plaintext mismatch:\n got %x\nwant %x", plain, source)
	}
	if !bytes.Equal(decTag, wantLongTag) {
		t.Errorf("decrypted tag mismatch:\n got %x\nwant %x", decTag, wantLongTag)
	}
}

// TestOCB2AuthCrypt reproduces TestCrypt::authcrypt: for every length 0-127
// the OCB2 roundtrip must restore the plaintext and produce identical tags
// in both directions.
func TestOCB2AuthCrypt(t *testing.T) {
	cs := New()
	if !cs.SetKey(rawKeyKAT, nonceKAT, nonceKAT) {
		t.Fatal("SetKey failed")
	}

	for length := 0; length < 128; length++ {
		src := make([]byte, length)
		for i := range src {
			src[i] = byte(i + 1)
		}
		encrypted, encTag, ok := cs.ocbEncrypt(src, nonceKAT, true)
		if !ok {
			t.Fatalf("length %d: ocb_encrypt failed", length)
		}
		decrypted, decTag, ok := cs.ocbDecrypt(encrypted, nonceKAT)
		if !ok {
			t.Fatalf("length %d: ocb_decrypt failed", length)
		}
		if !bytes.Equal(encTag, decTag) {
			t.Fatalf("length %d: tags differ: %x vs %x", length, encTag, decTag)
		}
		if !bytes.Equal(src, decrypted) {
			t.Fatalf("length %d: roundtrip mismatch:\n got %x\nwant %x", length, decrypted, src)
		}
	}
}

// TestOCB2XEXStarAttack reproduces TestCrypt::xexstarAttack (prevention of
// the attack from section 4.1 of https://eprint.iacr.org/2019/311).
func TestOCB2XEXStarAttack(t *testing.T) {
	cs := New()
	if !cs.SetKey(rawKeyKAT, nonceKAT, nonceKAT) {
		t.Fatal("SetKey failed")
	}

	src := make([]byte, 2*blockSize)
	// First block: all zero except the last byte, set to len(second block).
	src[blockSize-1] = blockSize * 8
	// Second block: arbitrary value.
	for i := blockSize; i < 2*blockSize; i++ {
		src[i] = 42
	}

	// With modifyPlain disabled the critical plaintext must be rejected.
	encrypted, encTag, ok := cs.ocbEncrypt(src, nonceKAT, false)
	if ok {
		t.Fatal("ocb_encrypt accepted XEX* attack plaintext with modifyPlain=false")
	}

	// Perform the attack: flip the length byte and forge the tag from the
	// known second block.
	encrypted[blockSize-1] ^= blockSize * 8
	for i := 0; i < blockSize; i++ {
		encTag[i] = src[blockSize+i] ^ encrypted[blockSize+i]
	}

	// The forged tag matches, but ocb_decrypt must detect the attack.
	decrypted, decTag, ok := cs.ocbDecrypt(encrypted[:blockSize], nonceKAT)
	if ok {
		t.Fatal("ocb_decrypt failed to detect XEX* attack")
	}
	if !bytes.Equal(encTag, decTag) {
		t.Fatalf("forged tag does not match decrypted tag: %x vs %x", encTag, decTag)
	}
	if len(decrypted) != blockSize {
		t.Fatalf("unexpected plaintext length %d", len(decrypted))
	}

	// With modifyPlain enabled (the default used by Encrypt), the same
	// plaintext goes through with one bit flipped in the encrypted domain.
	encrypted, encTag, ok = cs.ocbEncrypt(src, nonceKAT, true)
	if !ok {
		t.Fatal("ocb_encrypt with modifyPlain=true failed")
	}
	decrypted, decTag, ok = cs.ocbDecrypt(encrypted, nonceKAT)
	if !ok {
		t.Fatal("ocb_decrypt of modified plaintext failed")
	}
	if !bytes.Equal(encTag, decTag) {
		t.Fatalf("tags differ after modification: %x vs %x", encTag, decTag)
	}
	if src[0] != 0 {
		t.Fatalf("source must not be modified, src[0] = %d", src[0])
	}
	if decrypted[0] != 1 {
		t.Fatalf("decrypted[0] must have the flipped bit, got %d", decrypted[0])
	}
}
