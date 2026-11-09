package crypt

import (
	"bytes"
	"math/rand"
	"testing"
)

// newSession builds a connected pair like the official tests do
// (TestCrypt::ivrecovery): enc plays the sender, dec the receiver, and the
// sender's encrypt IV is forced to 0x55*16 for determinism.
func newSession(t *testing.T) (enc, dec *CryptState) {
	t.Helper()
	enc = New()
	if err := enc.GenerateKey(); err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if !enc.SetEncryptIV(bytes.Repeat([]byte{0x55}, IVSize)) {
		t.Fatal("SetEncryptIV failed")
	}
	dec = New()
	if !dec.SetKey(enc.RawKey(), dec2iv(enc), enc.EncryptIV()) {
		t.Fatal("SetKey failed")
	}
	return enc, dec
}

// dec2iv returns the IV the receiver must use for its own outgoing packets:
// the sender's decrypt IV (C++: dec.setKey(key, enc.getDecryptIV(),
// enc.getEncryptIV())).
func dec2iv(enc *CryptState) []byte {
	return enc.DecryptIV()
}

// secret10 is the 10-byte payload of the official tests
// (unsigned char secret[10] = "abcdefghi", i.e. 9 chars + NUL).
var secret10 = append([]byte("abcdefghi"), 0)

// TestRoundtripRandomPayloads roundtrips 1000 seeded random payloads of
// varying lengths and checks the stats stay clean.
func TestRoundtripRandomPayloads(t *testing.T) {
	enc := New()
	if err := enc.GenerateKey(); err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	dec := New()
	if !dec.SetKey(enc.RawKey(), enc.DecryptIV(), enc.EncryptIV()) {
		t.Fatal("SetKey failed")
	}

	rng := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic test data
	for i := 0; i < 1000; i++ {
		plain := make([]byte, rng.Intn(1000))
		rng.Read(plain)
		packet, err := enc.Encrypt(plain)
		if err != nil {
			t.Fatalf("packet %d: Encrypt: %v", i, err)
		}
		if len(packet) != 4+len(plain) {
			t.Fatalf("packet %d: wrong packet length %d", i, len(packet))
		}
		got, err := dec.Decrypt(packet)
		if err != nil {
			t.Fatalf("packet %d: Decrypt: %v", i, err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("packet %d: plaintext mismatch", i)
		}
	}

	st := dec.Stats()
	if st.Good != 1000 || st.Late != 0 || st.Lost != 0 || st.LostCount != 0 || st.DecryptFailed != 0 {
		t.Errorf("stats after clean roundtrip = %+v, want Good=1000 and everything else 0", st)
	}
	if dec.ResyncNeeded() {
		t.Error("ResyncNeeded must be false after a clean roundtrip")
	}
}

// TestTamperBitFlips reproduces TestCrypt::tamper and extends it to every
// byte of the packet: each single-bit flip must fail cleanly (no panic, no
// state corruption) and bump the failure counter.
func TestTamperBitFlips(t *testing.T) {
	cs := New()
	if !cs.SetKey(rawKeyKAT, nonceKAT, nonceKAT) {
		t.Fatal("SetKey failed")
	}
	msg := append([]byte("It was a funky funky town!"), 0) // C++ sizeof includes the NUL
	packet, err := cs.Encrypt(msg)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	failedBefore := cs.Stats().DecryptFailed
	for i := 0; i < len(packet)*8; i++ {
		packet[i/8] ^= 1 << (i % 8)
		if _, err := cs.Decrypt(packet); err == nil {
			t.Fatalf("bit %d: flipped packet decrypted without error", i)
		}
		packet[i/8] ^= 1 << (i % 8)
	}
	if got := cs.Stats().DecryptFailed - failedBefore; got != uint32(len(packet)*8) {
		t.Errorf("DecryptFailed incremented %d times, want %d", got, len(packet)*8)
	}

	// The stream must be unharmed: the untouched packet still decrypts.
	got, err := cs.Decrypt(packet)
	if err != nil {
		t.Fatalf("untouched packet no longer decrypts: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Error("untouched packet decrypted to wrong plaintext")
	}
}

// TestDecryptGarbageNoPanic feeds short and random packets; Decrypt must
// return an error instead of panicking.
func TestDecryptGarbageNoPanic(t *testing.T) {
	cs := New()
	if !cs.SetKey(rawKeyKAT, nonceKAT, nonceKAT) {
		t.Fatal("SetKey failed")
	}
	for i := 0; i < 4; i++ {
		if _, err := cs.Decrypt(bytes.Repeat([]byte{0xA5}, i)); err == nil {
			t.Errorf("packet of length %d accepted", i)
		}
	}
	rng := rand.New(rand.NewSource(7)) //nolint:gosec // deterministic test data
	for i := 0; i < 1000; i++ {
		garbage := make([]byte, rng.Intn(64))
		rng.Read(garbage)
		if _, err := cs.Decrypt(garbage); err == nil {
			t.Errorf("garbage packet %d accepted", i)
		}
	}
}

// TestIVRecovery reproduces TestCrypt::ivrecovery: IV reuse rejection,
// recovery from lost packets, wraparound accounting, resync after the
// recovery window is exceeded.
func TestIVRecovery(t *testing.T) {
	enc, dec := newSession(t)

	packet, err := enc.Encrypt(secret10)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := dec.Decrypt(packet)
	if err != nil {
		t.Fatalf("first Decrypt: %v", err)
	}
	if !bytes.Equal(got, secret10) {
		t.Error("first decrypt: plaintext mismatch")
	}

	// Refuse to reuse the same IV.
	if _, err := dec.Decrypt(packet); err == nil {
		t.Error("replayed packet accepted")
	}

	// Recover from 16 lost packets; the last of them is delivered, so 15
	// count as lost (packets 2..16 of the gap).
	for i := 0; i < 16; i++ {
		packet, err = enc.Encrypt(secret10)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
	}
	if _, err := dec.Decrypt(packet); err != nil {
		t.Fatalf("Decrypt after 16 lost packets: %v", err)
	}
	if st := dec.Stats(); st.Lost != 15 {
		t.Errorf("Lost = %d after 16 lost packets, want 15", st.Lost)
	}

	// Wraparound: 15 dropped packets per round must count as 14 lost (the
	// C++ test resets the counter each round and expects 14; here the
	// cumulative value is asserted instead).
	for i := 0; i < 128; i++ {
		for j := 0; j < 15; j++ {
			packet, err = enc.Encrypt(secret10)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
		}
		if _, err := dec.Decrypt(packet); err != nil {
			t.Fatalf("round %d: Decrypt: %v", i, err)
		}
		if st := dec.Stats(); st.Lost != 15+14*uint32(i+1) {
			t.Errorf("round %d: Lost = %d, want %d", i, st.Lost, 15+14*(i+1))
		}
	}

	if !bytes.Equal(enc.EncryptIV(), dec.DecryptIV()) {
		t.Error("encrypt IV and decrypt IV diverged after wraparound")
	}

	// Too far ahead: 257 packets exceed the recovery window.
	for i := 0; i < 257; i++ {
		if _, err := enc.Encrypt(secret10); err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
	}
	packet, err = enc.Encrypt(secret10)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := dec.Decrypt(packet); err == nil {
		t.Error("packet beyond recovery window accepted")
	}

	// Sync it, exactly like the official test.
	if !dec.SetDecryptIV(enc.EncryptIV()) {
		t.Fatal("SetDecryptIV failed")
	}
	packet, err = enc.Encrypt(secret10)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := dec.Decrypt(packet); err != nil {
		t.Fatalf("Decrypt after manual sync: %v", err)
	}
}

// TestOutOfOrderAndReplay reproduces TestCrypt::reverserecovery: out-of-
// order delivery recovers up to 30 packets but not beyond, and replays are
// always rejected. Expected stats are derived from the C++ algorithm:
//
//   - crypted[127] arrives first: "lost a few" case, lost = 127
//   - crypted[126..98] are 29 late packets: late = 29, lost = 127-29 = 98
//   - crypted[97..0] (98 packets) fall outside the ±30 window: rejected
//   - replaying crypted[127..98] hits the replay history: rejected
//   - 512 fresh packets decrypt in order (no further late/lost), then all
//     512 replays are rejected on tag mismatch
func TestOutOfOrderAndReplay(t *testing.T) {
	enc, dec := newSession(t)

	crypted := make([][]byte, 128)
	for i := range crypted {
		packet, err := enc.Encrypt(secret10)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		crypted[i] = packet
	}

	for i := 0; i < 30; i++ {
		if _, err := dec.Decrypt(crypted[127-i]); err != nil {
			t.Fatalf("OOO packet 127-%d rejected: %v", i, err)
		}
	}
	for i := 30; i < 128; i++ {
		if _, err := dec.Decrypt(crypted[127-i]); err == nil {
			t.Fatalf("packet 127-%d outside recovery window accepted", i)
		}
	}
	for i := 0; i < 30; i++ {
		if _, err := dec.Decrypt(crypted[127-i]); err == nil {
			t.Fatalf("replayed packet 127-%d accepted", i)
		}
	}

	st := dec.Stats()
	if st.Good != 30 || st.Late != 29 || st.Lost != 98 || st.LostCount != 1 {
		t.Errorf("stats after OOO phase = %+v, want Good=30 Late=29 Lost=98 LostCount=1", st)
	}
	if st.DecryptFailed != 128 {
		t.Errorf("DecryptFailed = %d after OOO phase, want 128", st.DecryptFailed)
	}

	// Extensive replay attack test: 512 fresh packets, all decrypt in
	// order, then all replays fail.
	crypted512 := make([][]byte, 512)
	for i := range crypted512 {
		packet, err := enc.Encrypt(secret10)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		crypted512[i] = packet
	}
	for i, packet := range crypted512 {
		if _, err := dec.Decrypt(packet); err != nil {
			t.Fatalf("in-order packet %d rejected: %v", i, err)
		}
	}
	for i, packet := range crypted512 {
		if _, err := dec.Decrypt(packet); err == nil {
			t.Fatalf("replayed in-order packet %d accepted", i)
		}
	}

	st = dec.Stats()
	if st.Good != 542 || st.Late != 29 || st.Lost != 98 {
		t.Errorf("stats after replay phase = %+v, want Good=542 Late=29 Lost=98", st)
	}
	if st.DecryptFailed != 128+512 {
		t.Errorf("DecryptFailed = %d after replay phase, want %d", st.DecryptFailed, 128+512)
	}
}

// TestLossAndReorderStats delivers a fixed drop/reorder pattern and asserts
// the exact late/lost accounting derived from CryptStateOCB2::decrypt
// (IV bytes: pkt n has ivbyte 0x56+n; decrypt IV byte 0 tracks the newest
// in-order/lost packet, late packets restore it). Note the late adjustment
// uses a strict comparison (C++: lost > abs(lost)), so a Lost count of 1 is
// never decremented to 0:
//
//	pkt 0  in order                  good=1
//	pkt 2  gap of 2                  lost=1
//	pkt 1  late                      late=1, lost stays 1 (1 > 1 is false)
//	pkt 5  gap of 3                  lost=3
//	pkt 4  late                      late=2, lost=2
//	pkt 3  late                      late=3, lost=1
//	(drop 6..10)
//	pkt 11 gap of 6                  lost=6
//	pkt 12 in order                  good=8
//	pkt 7  late (within ±30 window)  late=4, lost=5
func TestLossAndReorderStats(t *testing.T) {
	enc, dec := newSession(t)

	// byte(n)+1 so no payload starts with an all-zero block: such
	// plaintexts trip the XEX* countermeasure, which flips one bit of the
	// first block in the encrypted domain (official behavior).
	payload := func(n int) []byte {
		return bytes.Repeat([]byte{byte(n) + 1}, 20)
	}
	packets := make([][]byte, 14)
	for i := range packets {
		packet, err := enc.Encrypt(payload(i))
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		packets[i] = packet
	}

	order := []int{0, 2, 1, 5, 4, 3, 11, 12, 7}
	for _, n := range order {
		got, err := dec.Decrypt(packets[n])
		if err != nil {
			t.Fatalf("packet %d rejected: %v", n, err)
		}
		if !bytes.Equal(got, payload(n)) {
			t.Errorf("packet %d decrypted to wrong plaintext", n)
		}
	}

	st := dec.Stats()
	if st.Good != 9 || st.Late != 4 || st.Lost != 5 || st.LostCount != 3 || st.DecryptFailed != 0 {
		t.Errorf("stats = %+v, want Good=9 Late=4 Lost=5 LostCount=3 DecryptFailed=0", st)
	}

	// No stream corruption: the next in-order packet still decrypts.
	packet, err := enc.Encrypt(payload(13))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := dec.Decrypt(packet); err != nil {
		t.Fatalf("stream corrupted after reorder: %v", err)
	}
	if st := dec.Stats(); st.Good != 10 {
		t.Errorf("Good = %d after follow-up packet, want 10", st.Good)
	}
}

// TestResyncRecoveryClientInitiated forces a desync beyond the recovery
// window and walks the official client-initiated resync flow (Murmur
// Messages.cpp msgCryptSetup + MainWindow::msgCryptSetup): persistent
// decrypt failures → GenResync (empty CryptSetup on the wire) → server
// answers with its current encrypt IV → SetResync applies it → the next
// packet decrypts.
func TestResyncRecoveryClientInitiated(t *testing.T) {
	enc, dec := newSession(t)
	secret := []byte("resync-payload")

	// Desync: 300 undelivered packets exceed both the ±30 reorder window
	// and the byte-0 wraparound handling (byte 1 carried once).
	var packet []byte
	var err error
	for i := 0; i < 300; i++ {
		packet, err = enc.Encrypt(secret)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
	}
	for i := 0; i < ResyncThreshold; i++ {
		if _, err := dec.Decrypt(packet); err == nil {
			t.Fatal("desynced packet unexpectedly accepted")
		}
	}
	if !dec.ResyncNeeded() {
		t.Error("ResyncNeeded must be true after persistent failures")
	}

	// Client requests; server answers with its current encrypt IV.
	dec.GenResync()
	if dec.ResyncNeeded() {
		t.Error("ResyncNeeded must be false right after GenResync")
	}
	if err := dec.SetResync(nil, nil, enc.EncryptIV()); err != nil {
		t.Fatalf("SetResync: %v", err)
	}

	// Recovery within ≤ 2 packets: the very next one decrypts.
	packet, err = enc.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := dec.Decrypt(packet)
	if err != nil {
		t.Fatalf("Decrypt after resync: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Error("plaintext mismatch after resync")
	}
	if st := dec.Stats(); st.Resync != 1 {
		t.Errorf("Resync counter = %d, want 1", st.Resync)
	}
}

// TestResyncRecoveryServerInitiated walks the server-initiated direction:
// the server fails to decrypt our packets, sends an empty CryptSetup, the
// client answers with its current encrypt IV as client_nonce, and the
// server applies it via SetResync(nil, nonce, nil).
func TestResyncRecoveryServerInitiated(t *testing.T) {
	enc, dec := newSession(t)
	secret := []byte("server-initiated")

	var packet []byte
	var err error
	for i := 0; i < 300; i++ {
		packet, err = enc.Encrypt(secret)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
	}
	for i := 0; i < ResyncThreshold; i++ {
		if _, err := dec.Decrypt(packet); err == nil {
			t.Fatal("desynced packet unexpectedly accepted")
		}
	}
	if !dec.ResyncNeeded() {
		t.Error("ResyncNeeded must be true")
	}

	dec.GenResync()
	// Client answers the empty CryptSetup with its current encrypt IV.
	if err := dec.SetResync(nil, enc.EncryptIV(), nil); err != nil {
		t.Fatalf("SetResync: %v", err)
	}

	packet, err = enc.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := dec.Decrypt(packet); err != nil {
		t.Fatalf("Decrypt after server-initiated resync: %v", err)
	}
	if st := dec.Stats(); st.Resync != 1 {
		t.Errorf("Resync counter = %d, want 1", st.Resync)
	}
}

// TestResyncFullReKey covers the full CryptSetup variant of SetResync
// (key + client nonce + server nonce), as sent on initial connect or full
// re-key: both directions must work afterwards.
func TestResyncFullReKey(t *testing.T) {
	enc, dec := newSession(t)
	secret := []byte("full-rekey")

	// Desync dec beyond recovery.
	for i := 0; i < 300; i++ {
		if _, err := enc.Encrypt(secret); err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
	}

	// Server generates fresh material and sends the full triple.
	fresh := New()
	if err := fresh.GenerateKey(); err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if err := dec.SetResync(fresh.RawKey(), fresh.EncryptIV(), fresh.DecryptIV()); err != nil {
		t.Fatalf("SetResync full: %v", err)
	}
	// The sender must switch to the new material too (server would send
	// CryptSetup to both roles in a real session; here enc adopts the key
	// and the decrypt-side IV of the fresh state).
	if !enc.SetKey(fresh.RawKey(), fresh.DecryptIV(), fresh.EncryptIV()) {
		t.Fatal("SetKey failed")
	}

	packet, err := enc.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := dec.Decrypt(packet)
	if err != nil {
		t.Fatalf("Decrypt after full re-key: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Error("plaintext mismatch after full re-key")
	}
	// A full re-key is not counted as a nonce resync (MainWindow::msgCryptSetup).
	if st := dec.Stats(); st.Resync != 0 {
		t.Errorf("Resync counter = %d after full re-key, want 0", st.Resync)
	}
}

// TestSetResyncRejectsBadMaterial checks the malformed-input paths.
func TestSetResyncRejectsBadMaterial(t *testing.T) {
	cs := New()
	if err := cs.GenerateKey(); err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if err := cs.SetResync(nil, nil, nil); err == nil {
		t.Error("empty SetResync accepted")
	}
	if err := cs.SetResync(nil, nil, []byte{1, 2, 3}); err == nil {
		t.Error("short serverNonce accepted")
	}
	if err := cs.SetResync(nil, []byte{1, 2, 3}, nil); err == nil {
		t.Error("short clientNonce accepted")
	}
	if err := cs.SetResync([]byte{1}, []byte{2}, []byte{3}); err == nil {
		t.Error("short full material accepted")
	}
	if st := cs.Stats(); st.Resync != 0 {
		t.Errorf("failed SetResync attempts must not count, got %d", st.Resync)
	}
}

// TestUninitializedErrors checks the guard rails.
func TestUninitializedErrors(t *testing.T) {
	cs := New()
	if cs.IsValid() {
		t.Error("fresh state must be invalid")
	}
	if _, err := cs.Encrypt(nil); err != ErrNotInitialized {
		t.Errorf("Encrypt on fresh state: err = %v, want ErrNotInitialized", err)
	}
	if _, err := cs.Decrypt(nil); err != ErrNotInitialized {
		t.Errorf("Decrypt on fresh state: err = %v, want ErrNotInitialized", err)
	}
	if cs.ResyncNeeded() {
		t.Error("fresh state must not need resync")
	}
}
