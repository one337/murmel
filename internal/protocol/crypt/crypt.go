// Package crypt implements Mumble's UDP voice encryption layer: OCB2-AES128
// as spoken by the official Mumble client and Murmur (CryptStateOCB2).
//
// The official C++ implementation is the specification for this port:
//
//	https://github.com/mumble-voip/mumble/blob/master/src/crypto/CryptStateOCB2.cpp
//	https://github.com/mumble-voip/mumble/blob/master/src/crypto/CryptState.h
//	https://github.com/mumble-voip/mumble/blob/master/src/tests/TestCrypt/TestCrypt.cpp
//
// Mumble uses OCB2 (Rogaway) with AES-128 — NOT OCB3/RFC 7253; the two
// constructions are wire-incompatible.
//
// Encrypted UDP packet layout (CryptStateOCB2::encrypt):
//
//	byte 0     first byte of the sender's current encrypt IV
//	bytes 1-3  first three bytes of the 16-byte OCB2 authentication tag
//	bytes 4-   OCB2 ciphertext
//
// CryptSetup wiring (MainWindow::msgCryptSetup / Murmur Messages.cpp): the
// server sends {key, client_nonce, server_nonce}; the client installs it as
// SetKey(key, clientNonce, serverNonce) — client_nonce is the IV for packets
// TO the server, server_nonce the IV for packets FROM the server.
package crypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

// Key and IV sizes (AES-128, 16-byte blocks).
const (
	KeySize = 16
	IVSize  = 16

	// ResyncThreshold is the number of consecutive failed decrypts after
	// which ResyncNeeded reports true. The official client instead triggers
	// on wall-clock time (no good packet for 5 s, at most one request per
	// 5 s — ServerHandler.cpp); a failure count is equivalent for the pure
	// state machine and deterministic in tests. The UDP transport may layer the official
	// timing on top.
	ResyncThreshold = 8
)

// Errors returned by this package.
var (
	// ErrNotInitialized is returned when key material has not been set.
	ErrNotInitialized = errors.New("crypt: state not initialized")
	// ErrDecryptFailed is returned for every rejected packet: tag mismatch,
	// replay, reordering outside the recovery window, or malformed input.
	ErrDecryptFailed = errors.New("crypt: decrypt failed")
	// ErrEncryptFailed is returned when the XEX* countermeasure rejects a
	// plaintext; unreachable via Encrypt, which modifies instead.
	ErrEncryptFailed = errors.New("crypt: encrypt failed")
	// ErrBadKeyMaterial is returned by SetResync for malformed input.
	ErrBadKeyMaterial = errors.New("crypt: invalid key material")
)

// Stats are the decrypt-side packet counters. Good, Late, Lost and Resync
// mirror CryptStateOCB2's m_statsLocal (PacketStats); LostCount and
// DecryptFailed are additions needed to drive resyncs.
type Stats struct {
	Good          uint32 // packets decrypted successfully
	Late          uint32 // out-of-order packets accepted within the ±30 window
	Lost          uint32 // packets presumed lost; late arrivals decrement it
	LostCount     uint32 // number of decrypts that detected a loss
	Resync        uint32 // nonce resyncs applied via SetResync
	DecryptFailed uint32 // total failed decrypt attempts
}

// CryptState holds the OCB2-AES128 state of one Mumble UDP session,
// mirroring CryptStateOCB2: a raw AES-128 key, the rolling IVs used for the
// two directions, and a 256-entry replay history.
type CryptState struct {
	cipher         cipher.Block
	rawKey         [KeySize]byte
	encryptIV      [IVSize]byte
	decryptIV      [IVSize]byte
	decryptHistory [256]byte
	init           bool

	stats               Stats
	consecutiveFailures int
}

// New returns a fresh, uninitialized CryptState.
func New() *CryptState {
	return &CryptState{}
}

// IsValid reports whether key material has been set (C++ bInit).
func (cs *CryptState) IsValid() bool {
	return cs.init
}

// GenerateKey fills the raw key and both IVs from crypto/rand and marks the
// state valid. Port of CryptStateOCB2::genKey(); used by tests and by a
// peer in the server role that initiates a full re-key.
func (cs *CryptState) GenerateKey() error {
	key := make([]byte, KeySize+2*IVSize)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	copy(cs.rawKey[:], key[:KeySize])
	copy(cs.encryptIV[:], key[KeySize:KeySize+IVSize])
	copy(cs.decryptIV[:], key[KeySize+IVSize:])
	if !cs.setCipher() {
		return ErrBadKeyMaterial
	}
	return nil
}

// SetKey installs a full set of key material, mirroring
// CryptStateOCB2::setKey(rkey, eiv, div). For a CryptSetup message from the
// server the client passes SetKey(msg.Key, msg.ClientNonce, msg.ServerNonce).
// It returns false — changing nothing — on wrong lengths.
func (cs *CryptState) SetKey(rawKey, encryptIV, decryptIV []byte) bool {
	if len(rawKey) != KeySize || len(encryptIV) != IVSize || len(decryptIV) != IVSize {
		return false
	}
	copy(cs.rawKey[:], rawKey)
	copy(cs.encryptIV[:], encryptIV)
	copy(cs.decryptIV[:], decryptIV)
	return cs.setCipher()
}

// SetRawKey replaces the AES key without touching validity (C++ setRawKey
// does not set bInit either). On an already-valid state the cipher is
// rebuilt so the new key takes effect immediately, as in C++.
func (cs *CryptState) SetRawKey(rawKey []byte) bool {
	if len(rawKey) != KeySize {
		return false
	}
	copy(cs.rawKey[:], rawKey)
	if cs.init {
		return cs.setCipher()
	}
	return true
}

// SetEncryptIV replaces the IV used for outgoing packets.
func (cs *CryptState) SetEncryptIV(iv []byte) bool {
	if len(iv) != IVSize {
		return false
	}
	copy(cs.encryptIV[:], iv)
	return true
}

// SetDecryptIV replaces the IV used for incoming packets.
func (cs *CryptState) SetDecryptIV(iv []byte) bool {
	if len(iv) != IVSize {
		return false
	}
	copy(cs.decryptIV[:], iv)
	return true
}

// RawKey returns a copy of the AES-128 key.
func (cs *CryptState) RawKey() []byte {
	out := make([]byte, KeySize)
	copy(out, cs.rawKey[:])
	return out
}

// EncryptIV returns a copy of the current outgoing IV.
func (cs *CryptState) EncryptIV() []byte {
	out := make([]byte, IVSize)
	copy(out, cs.encryptIV[:])
	return out
}

// DecryptIV returns a copy of the current incoming IV.
func (cs *CryptState) DecryptIV() []byte {
	out := make([]byte, IVSize)
	copy(out, cs.decryptIV[:])
	return out
}

// Stats returns a snapshot of the decrypt-side counters.
func (cs *CryptState) Stats() Stats {
	return cs.stats
}

func (cs *CryptState) setCipher() bool {
	c, err := aes.NewCipher(cs.rawKey[:])
	if err != nil {
		return false
	}
	cs.cipher = c
	cs.init = true
	return true
}

// Encrypt encrypts a voice/ping payload into a wire-ready UDP packet:
// 4-byte header (IV byte + 3 tag bytes) followed by the OCB2 ciphertext,
// exactly as CryptStateOCB2::encrypt does.
func (cs *CryptState) Encrypt(plain []byte) ([]byte, error) {
	if !cs.init {
		return nil, ErrNotInitialized
	}

	// First, increase our IV (carry propagates from byte 0 upwards).
	for i := 0; i < IVSize; i++ {
		cs.encryptIV[i]++
		if cs.encryptIV[i] != 0 {
			break
		}
	}

	ciphertext, tag, ok := cs.ocbEncrypt(plain, cs.encryptIV[:], true)
	if !ok {
		return nil, ErrEncryptFailed
	}
	packet := make([]byte, 4+len(plain))
	copy(packet[4:], ciphertext)
	packet[0] = cs.encryptIV[0]
	copy(packet[1:4], tag[:3])
	return packet, nil
}

// Decrypt verifies and decrypts an incoming UDP packet, returning the
// plaintext or ErrDecryptFailed. It is a direct port of
// CryptStateOCB2::decrypt, including the salt-byte based handling of
// out-of-order and lost packets and the 256-entry replay history.
func (cs *CryptState) Decrypt(packet []byte) ([]byte, error) {
	if !cs.init {
		return nil, ErrNotInitialized
	}
	if len(packet) < 4 {
		cs.recordFailure()
		return nil, ErrDecryptFailed
	}

	var saveiv [IVSize]byte
	copy(saveiv[:], cs.decryptIV[:])

	ivbyte := packet[0]
	late, lost := 0, 0
	restore := false

	if (cs.decryptIV[0]+1)&0xFF == ivbyte {
		// In order as expected.
		switch {
		case ivbyte > cs.decryptIV[0]:
			cs.decryptIV[0] = ivbyte
		case ivbyte < cs.decryptIV[0]:
			// Byte 0 wrapped around on the sender: carry into the rest.
			cs.decryptIV[0] = ivbyte
			for i := 1; i < IVSize; i++ {
				cs.decryptIV[i]++
				if cs.decryptIV[i] != 0 {
					break
				}
			}
		default:
			cs.recordFailure()
			return nil, ErrDecryptFailed
		}
	} else {
		// This is either out of order or a repeat.
		diff := int(ivbyte) - int(cs.decryptIV[0])
		if diff > 128 {
			diff -= 256
		} else if diff < -128 {
			diff += 256
		}

		switch {
		case ivbyte < cs.decryptIV[0] && diff > -30 && diff < 0:
			// Late packet, but no wraparound.
			late, lost = 1, -1
			cs.decryptIV[0] = ivbyte
			restore = true
		case ivbyte > cs.decryptIV[0] && diff > -30 && diff < 0:
			// Last was 0x02, here comes 0xff from last round.
			late, lost = 1, -1
			cs.decryptIV[0] = ivbyte
			for i := 1; i < IVSize; i++ {
				old := cs.decryptIV[i]
				cs.decryptIV[i]--
				if old != 0 {
					break
				}
			}
			restore = true
		case ivbyte > cs.decryptIV[0] && diff > 0:
			// Lost a few packets, but beyond that we're good.
			lost = int(ivbyte) - int(cs.decryptIV[0]) - 1
			cs.decryptIV[0] = ivbyte
		case ivbyte < cs.decryptIV[0] && diff > 0:
			// Lost a few packets, and wrapped around.
			lost = 256 - int(cs.decryptIV[0]) + int(ivbyte) - 1
			cs.decryptIV[0] = ivbyte
			for i := 1; i < IVSize; i++ {
				cs.decryptIV[i]++
				if cs.decryptIV[i] != 0 {
					break
				}
			}
		default:
			cs.recordFailure()
			return nil, ErrDecryptFailed
		}

		// Replay detection: we already accepted a packet with this IV byte
		// and the same second IV byte.
		if cs.decryptHistory[cs.decryptIV[0]] == cs.decryptIV[1] {
			copy(cs.decryptIV[:], saveiv[:])
			cs.recordFailure()
			return nil, ErrDecryptFailed
		}
	}

	plain, tag, ok := cs.ocbDecrypt(packet[4:], cs.decryptIV[:])
	if !ok || !bytes.Equal(tag[:3], packet[1:4]) {
		copy(cs.decryptIV[:], saveiv[:])
		cs.recordFailure()
		return nil, ErrDecryptFailed
	}
	cs.decryptHistory[cs.decryptIV[0]] = cs.decryptIV[1]

	if restore {
		// Late packets must not advance the main stream position.
		copy(cs.decryptIV[:], saveiv[:])
	}

	// Stats as in CryptStateOCB2::decrypt: a late packet that was counted as
	// lost earlier decrements Lost again.
	cs.stats.Good++
	if late > 0 {
		cs.stats.Late++
	}
	if lost > 0 {
		cs.stats.Lost += uint32(lost)
		cs.stats.LostCount++
	} else if lost < 0 && int32(cs.stats.Lost) > int32(-lost) {
		cs.stats.Lost--
	}
	cs.consecutiveFailures = 0
	return plain, nil
}

// ResyncNeeded reports whether the decrypt stream has desynchronized badly
// enough that new key material should be requested from the peer: at least
// ResyncThreshold consecutive decrypts have failed.
func (cs *CryptState) ResyncNeeded() bool {
	return cs.init && cs.consecutiveFailures >= ResyncThreshold
}

// GenResync prepares a client-initiated resync request.
//
// In the official protocol (Murmur Messages.cpp msgCryptSetup) the client
// sends an EMPTY CryptSetup message; the server answers with its current
// encrypt IV, which the client applies via SetResync. GenResync performs
// the CryptState's part of that exchange: it resets the consecutive-failure
// count, rate-limiting requests to one per ResyncThreshold failures (the
// official client uses a 5-second tLastRequest timer instead). Sending the
// empty CryptSetup over the control channel is the UDP transport's job.
func (cs *CryptState) GenResync() {
	cs.consecutiveFailures = 0
}

// SetResync applies key material received from the peer in a CryptSetup
// message, recovering the decrypt stream. It mirrors
// MainWindow::msgCryptSetup:
//
//   - key, clientNonce and serverNonce all present: full re-key via SetKey
//     (client role: SetResync(msg.Key, msg.ClientNonce, msg.ServerNonce)).
//   - serverNonce only: nonce resync — the answer to a client-initiated
//     request; counts one resync and installs the nonce as decrypt IV.
//   - clientNonce only: nonce resync in the other direction — a server that
//     failed to decrypt OUR packets sends an empty CryptSetup, we answer
//     with our current encrypt IV (EncryptIV()), and the server applies it
//     via SetResync(nil, nonce, nil). Provided for symmetry so tests and a
//     server-role peer can use the same package.
//
// An entirely empty call is a protocol error: the official client answers
// such a message by sending back its encrypt IV, which is the caller's job.
func (cs *CryptState) SetResync(key, clientNonce, serverNonce []byte) error {
	switch {
	case len(key) > 0 && len(clientNonce) > 0 && len(serverNonce) > 0:
		if !cs.SetKey(key, clientNonce, serverNonce) {
			return ErrBadKeyMaterial
		}
		return nil
	case len(serverNonce) > 0:
		if len(serverNonce) != IVSize {
			return ErrBadKeyMaterial
		}
		cs.stats.Resync++
		copy(cs.decryptIV[:], serverNonce)
		return nil
	case len(clientNonce) > 0:
		if len(clientNonce) != IVSize {
			return ErrBadKeyMaterial
		}
		cs.stats.Resync++
		copy(cs.decryptIV[:], clientNonce)
		return nil
	}
	return ErrBadKeyMaterial
}

func (cs *CryptState) recordFailure() {
	cs.stats.DecryptFailed++
	cs.consecutiveFailures++
}
