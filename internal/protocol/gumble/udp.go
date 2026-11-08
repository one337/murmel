// Native UDP voice transport. The client speaks the legacy (< 1.5.0) UDP
// wire format it advertises via its protocol version: every datagram payload
// is OCB2-AES128-encrypted by crypt.CryptState (keys from P7's CryptSetup
// handling) and decrypts to a standalone packet whose header byte carries
// the message type in its top three bits — ping = 1 (header byte 0x20) or
// Opus voice = 4 (0x80), byte-identical to the UDPTunnel body framed for TCP
// (MumbleProtocol.h LegacyUDPMessageType, pinned by the T6 golden tests).
//
// Lifecycle: UDP is the primary voice path. Once a full CryptSetup installs
// key material, the transport probes with encrypted pings; the first
// successfully decrypted datagram activates it and outgoing voice routes
// here instead of the TCP tunnel. If no decryptable packet arrives within
// udpHandshakeWindow of start — or within udpGoodWindow while active (the
// official client disconnects there; we degrade instead) — voice falls back
// to the TCP UDPTunnel permanently. Persistent decrypt failures trigger the
// CryptSetup resync exchange implemented in cryptsetup.go.
package gumble

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"io.github/murmel/internal/protocol/crypt"
	"io.github/murmel/internal/protocol/gumble/varint"
)

// Legacy UDP message types (header byte >> 5).
const (
	udpMessageTypePing = 1
	udpMessageTypeOpus = 4
)

// Transport timing, mirroring the official client (ServerHandler.cpp): a
// ~1 s encrypted ping cadence, and trouble after 5 s without a decryptable
// packet. The 3 s handshake window keeps the worst-case fallback inside the
// plan's 5 s bound.
const (
	udpPingInterval    = 1 * time.Second
	udpHandshakeWindow = 3 * time.Second
	udpGoodWindow      = 5 * time.Second
	udpResyncInterval  = 5 * time.Second
)

// udpReadBufferSize exceeds Murmur's maximum UDP packet (1024 bytes) plus
// the 4-byte OCB2 header with headroom.
const udpReadBufferSize = 2048

// UDPVoiceStats is a snapshot of the native UDP voice transport counters.
// All fields are zero when UDP voice is disabled; FallbackAt stays zero
// until a fallback to the TCP tunnel happened.
type UDPVoiceStats struct {
	PingsSent        uint32
	PingsReceived    uint32
	VoiceSent        uint32
	VoiceReceived    uint32
	DecryptFailed    uint32
	ResyncsRequested uint32
	Fallbacks        uint32
	FallbackAt       time.Time
	Crypt            crypt.Stats
}

// udpTransport owns the UDP socket of one Client. The socket is a net.Conn
// (a connected UDP socket) so tests can inject mock or blackhole
// transports; packet semantics are preserved because every Write is one
// datagram.
type udpTransport struct {
	client *Client
	conn   net.Conn

	// timing knobs; package defaults, overridable in-package for tests
	// (the QA-bound tests keep the real values).
	pingEvery       time.Duration
	handshakeWindow time.Duration
	goodWindow      time.Duration
	resyncEvery     time.Duration

	readDeadline time.Duration

	mu               sync.Mutex
	active           bool
	fellBack         bool
	lastGood         time.Time
	lastResync       time.Time
	startedAt        time.Time
	stats            UDPVoiceStats
	startOnce        sync.Once
	closed           chan struct{}
	closeOnce        sync.Once
	connWritesFailed uint32
}

func newUDPTransport(client *Client, conn net.Conn) *udpTransport {
	return &udpTransport{
		client:          client,
		conn:            conn,
		pingEvery:       udpPingInterval,
		handshakeWindow: udpHandshakeWindow,
		goodWindow:      udpGoodWindow,
		resyncEvery:     udpResyncInterval,
		readDeadline:    udpPingInterval,
		closed:          make(chan struct{}),
	}
}

// cryptReady starts handshake probing. Called from the TCP read loop (via
// handleCryptSetup) once key material is installed; a re-key does not
// restart anything — the running transport simply encrypts with the new
// state.
func (t *udpTransport) cryptReady() {
	t.startOnce.Do(func() {
		t.mu.Lock()
		t.startedAt = time.Now()
		t.mu.Unlock()
		go t.pingLoop()
		go t.readLoop()
	})
}

func (t *udpTransport) isActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active
}

func (t *udpTransport) statsSnapshot() UDPVoiceStats {
	t.mu.Lock()
	s := t.stats
	t.mu.Unlock()
	s.Crypt = t.client.cryptStats()
	return s
}

// sendVoicePacket encrypts and sends one framed voice packet over UDP. It
// reports false when the transport did not take the packet — inactive,
// already fallen back, or the socket just died — in which case the caller
// must send the packet through the TCP tunnel instead; voice is never
// dropped by transport switching.
func (t *udpTransport) sendVoicePacket(packet []byte) bool {
	t.mu.Lock()
	if !t.active || t.fellBack {
		t.mu.Unlock()
		return false
	}
	t.mu.Unlock()

	datagram, err := t.client.cryptEncrypt(packet)
	if err != nil {
		return false
	}
	if _, err := t.conn.Write(datagram); err != nil {
		atomic.AddUint32(&t.connWritesFailed, 1)
		t.fallback()
		return false
	}

	t.mu.Lock()
	t.stats.VoiceSent++
	t.mu.Unlock()
	return true
}

func (t *udpTransport) sendPing() {
	datagram, err := t.client.cryptEncrypt(buildPingPacket(uint64(time.Now().UnixNano())))
	if err != nil {
		return
	}
	if _, err := t.conn.Write(datagram); err != nil {
		atomic.AddUint32(&t.connWritesFailed, 1)
		t.fallback()
		return
	}
	t.mu.Lock()
	t.stats.PingsSent++
	t.mu.Unlock()
}

func (t *udpTransport) pingLoop() {
	t.sendPing() // first probe leaves before the first tick
	ticker := time.NewTicker(t.pingEvery)
	defer ticker.Stop()
	for {
		select {
		case <-t.closed:
			return
		case <-ticker.C:
			t.mu.Lock()
			done := t.fellBack
			t.mu.Unlock()
			if done {
				return
			}
			t.sendPing()
		}
	}
}

func (t *udpTransport) readLoop() {
	buffer := make([]byte, udpReadBufferSize)
	for {
		select {
		case <-t.closed:
			return
		default:
		}

		_ = t.conn.SetReadDeadline(time.Now().Add(t.readDeadline))
		n, err := t.conn.Read(buffer)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				t.checkWindows()
				continue
			}
			// Hard socket error: the socket is gone; voice must continue
			// over TCP.
			t.fallback()
			return
		}
		if n > 0 {
			t.handleDatagram(buffer[:n])
		}
		t.checkWindows()
	}
}

// checkWindows enforces the handshake and liveness bounds. It runs on the
// read loop goroutine only.
func (t *udpTransport) checkWindows() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fellBack {
		return
	}
	now := time.Now()
	if t.active {
		if now.Sub(t.lastGood) > t.goodWindow {
			t.fallbackLocked(now)
		}
		return
	}
	if now.Sub(t.startedAt) > t.handshakeWindow {
		t.fallbackLocked(now)
	}
}

func (t *udpTransport) fallbackLocked(now time.Time) {
	t.fellBack = true
	t.active = false
	t.stats.Fallbacks++
	t.stats.FallbackAt = now
	t.shutdown()
}

func (t *udpTransport) fallback() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fellBack {
		return
	}
	t.fallbackLocked(time.Now())
}

func (t *udpTransport) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.shutdown()
}

// shutdown releases the socket and signals both loops to exit. Called with
// t.mu held (both loops tolerate that: they never take t.mu while blocked
// in Read/Write, and Close only needs to wake them).
func (t *udpTransport) shutdown() {
	t.closeOnce.Do(func() {
		close(t.closed)
		_ = t.conn.Close()
	})
}

func (t *udpTransport) handleDatagram(datagram []byte) {
	plain, err := t.client.cryptDecrypt(datagram)
	if err != nil {
		t.mu.Lock()
		t.stats.DecryptFailed++
		t.mu.Unlock()
		if t.client.cryptResyncNeeded() {
			t.requestResync()
		}
		return
	}
	if len(plain) == 0 {
		return
	}

	t.mu.Lock()
	t.lastGood = time.Now()
	if !t.active {
		t.active = true
	}
	voice := (plain[0]>>5)&0x7 == udpMessageTypeOpus
	ping := (plain[0]>>5)&0x7 == udpMessageTypePing
	if ping {
		t.stats.PingsReceived++
	}
	if voice {
		t.stats.VoiceReceived++
	}
	t.mu.Unlock()

	if voice {
		// Same standalone bytes as a UDPTunnel body (T6 contract).
		_ = t.client.handleVoicePacket(plain)
	}
}

// requestResync asks the server for a nonce resync after persistent decrypt
// failures. Rate-limited to one request per resyncEvery, on top of the
// failure-count limit inside crypt.CryptState (official client: at most one
// request per 5 s).
func (t *udpTransport) requestResync() {
	t.mu.Lock()
	now := time.Now()
	if now.Sub(t.lastResync) < t.resyncEvery {
		t.mu.Unlock()
		return
	}
	t.lastResync = now
	t.stats.ResyncsRequested++
	t.mu.Unlock()

	t.client.requestCryptResync()
}

// buildPingPacket frames a connectivity ping: header byte (ping << 5)
// followed by the varint timestamp, as UDPPingEncoder::encodePingPacket_legacy
// and the official client's ServerHandler do.
func buildPingPacket(timestamp uint64) []byte {
	packet := make([]byte, 0, 1+varint.MaxVarintLen)
	packet = append(packet, udpMessageTypePing<<5)
	var v [varint.MaxVarintLen]byte
	n := varint.Encode(v[:], int64(timestamp))
	return append(packet, v[:n]...)
}

// parsePingPacket decodes a ping packet produced by buildPingPacket (the
// server echoes the same format). ok is false unless the header byte names
// the ping type and the varint consumes the packet exactly.
func parsePingPacket(packet []byte) (timestamp int64, ok bool) {
	if len(packet) < 2 || (packet[0]>>5)&0x7 != udpMessageTypePing {
		return 0, false
	}
	value, n := varint.Decode(packet[1:])
	if n <= 0 || n != len(packet)-1 {
		return 0, false
	}
	return value, true
}
