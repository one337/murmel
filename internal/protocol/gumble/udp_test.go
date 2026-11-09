// Tests for the native UDP voice transport (udp.go). The two QA-bound
// tests — TestUDPCryptResync and TestUDPFallback — keep the production
// timing constants (1 s ping cadence, 3 s handshake window, 5 s good
// window) so they also prove the plan's bounds: recovery after resync
// and fallback within 5 s of a blackholed socket.
package gumble

import (
	"bytes"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"io.github/murmel/internal/protocol/crypt"
	"io.github/murmel/internal/protocol/gumble/MumbleProto"
)

// fakeMurmurUDP is a loopback stand-in for Murmur's UDP endpoint. It owns
// the server-side CryptState (encrypts with server_nonce, decrypts with
// client_nonce — the mirror of cryptSetupMaterial's client installation)
// and, while replyPings is set, echoes every decryptable ping so the
// client transport activates. Decrypted Opus datagrams are published on
// voice for byte-identity checks.
type fakeMurmurUDP struct {
	t    *testing.T
	conn net.PacketConn
	cs   *crypt.CryptState

	replyPings atomic.Bool
	voice      chan []byte

	lastAddr  atomic.Value // net.Addr of the client's connected socket
	closeOnce sync.Once
}

// newFakeMurmurUDP binds the endpoint and installs the given key material
// (the same triple the client receives via CryptSetup).
func newFakeMurmurUDP(t *testing.T, key, clientNonce, serverNonce []byte) *fakeMurmurUDP {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake murmur udp: %v", err)
	}
	f := &fakeMurmurUDP{
		t:     t,
		conn:  conn,
		cs:    crypt.New(),
		voice: make(chan []byte, 16),
	}
	if !f.cs.SetKey(key, serverNonce, clientNonce) {
		t.Fatal("fake server SetKey failed")
	}
	t.Cleanup(func() { f.closeOnce.Do(func() { conn.Close() }) })
	go f.loop()
	return f
}

func (f *fakeMurmurUDP) addr() string { return f.conn.LocalAddr().String() }

func (f *fakeMurmurUDP) loop() {
	buffer := make([]byte, udpReadBufferSize)
	for {
		n, from, err := f.conn.ReadFrom(buffer)
		if err != nil {
			return
		}
		f.lastAddr.Store(from)
		plain, err := f.cs.Decrypt(buffer[:n])
		if err != nil {
			continue
		}
		if len(plain) == 0 {
			continue
		}
		switch (plain[0] >> 5) & 0x7 {
		case udpMessageTypePing:
			if f.replyPings.Load() {
				reply, err := f.cs.Encrypt(plain)
				if err == nil {
					_, _ = f.conn.WriteTo(reply, from)
				}
			}
		case udpMessageTypeOpus:
			packet := make([]byte, len(plain))
			copy(packet, plain)
			select {
			case f.voice <- packet:
			default:
			}
		}
	}
}

// dialUDP returns a connected UDP socket to the fake server, the same
// shape DialWithDialer produces for the real transport.
func (f *fakeMurmurUDP) dialUDP(t *testing.T) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("udp", f.addr(), time.Second)
	if err != nil {
		t.Fatalf("dial fake murmur udp: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// sendRaw writes a datagram to the client without encrypting it
// (simulates corruption / a desynchronized server stream).
func (f *fakeMurmurUDP) sendRaw(t *testing.T, payload []byte) {
	t.Helper()
	if _, err := f.conn.WriteTo(payload, f.clientAddr(t)); err != nil {
		t.Fatalf("fake server raw write: %v", err)
	}
}

// sendEncrypted encrypts payload with the server state and sends it to
// the client's connected socket address.
func (f *fakeMurmurUDP) sendEncrypted(t *testing.T, payload []byte) {
	t.Helper()
	datagram, err := f.cs.Encrypt(payload)
	if err != nil {
		t.Fatalf("fake server encrypt: %v", err)
	}
	if _, err := f.conn.WriteTo(datagram, f.clientAddr(t)); err != nil {
		t.Fatalf("fake server write: %v", err)
	}
}

// clientAddr waits for the client's first datagram (the transport pings
// immediately after cryptReady, so this is prompt) and returns its source
// address.
func (f *fakeMurmurUDP) clientAddr(t *testing.T) net.Addr {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a := f.lastAddr.Load(); a != nil {
			return a.(net.Addr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the client's first datagram")
	return nil
}

// udpVoiceClient is framingConn plus a UDP transport wired to a fake
// Murmur endpoint, with key material installed the way the server would
// (which also starts the transport: handleCryptSetup → cryptReady).
func udpVoiceClient(t *testing.T, replyPings bool) (*Client, <-chan rawPacket, *fakeMurmurUDP) {
	t.Helper()
	client, packets := framingConn(t)
	key, clientNonce, serverNonce, _ := cryptSetupMaterial(t)
	fake := newFakeMurmurUDP(t, key, clientNonce, serverNonce)
	fake.replyPings.Store(replyPings)
	client.udpVoice = newUDPTransport(client, fake.dialUDP(t))
	t.Cleanup(client.udpVoice.close)
	if err := deliverCryptSetup(t, client, &MumbleProto.CryptSetup{
		Key:         key,
		ClientNonce: clientNonce,
		ServerNonce: serverNonce,
	}); err != nil {
		t.Fatalf("initial CryptSetup: %v", err)
	}
	return client, packets, fake
}

func waitTransport(t *testing.T, client *Client, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if client.VoiceTransport() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("voice transport still %q after %v, want %q", client.VoiceTransport(), within, want)
}

// waitPingsReceived blocks until the transport counted want pings.
func waitPingsReceived(t *testing.T, client *Client, want uint32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if client.UDPVoiceStats().PingsReceived == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("PingsReceived = %d, want %d", client.UDPVoiceStats().PingsReceived, want)
}

// TestUDPPingPacketCodec pins the connectivity ping format both ways:
// header byte 0x20 (type 1 << 5) plus the varint timestamp, exact
// roundtrip for timestamps across the varint ranges, and rejection of
// everything that is not a well-formed ping (wrong type, truncated
// varint, trailing bytes).
func TestUDPPingPacketCodec(t *testing.T) {
	if got := buildPingPacket(0); !bytes.Equal(got, []byte{0x20, 0x00}) {
		t.Fatalf("buildPingPacket(0) = % x, want 20 00", got)
	}

	for _, ts := range []uint64{0, 1, 0x7F, 0x80, 0x3FFF, 0x4000, 1 << 32, 1 << 47, 1 << 55} {
		packet := buildPingPacket(ts)
		if packet[0] != 0x20 {
			t.Fatalf("timestamp %d: header byte = %#x, want 0x20", ts, packet[0])
		}
		got, ok := parsePingPacket(packet)
		if !ok {
			t.Fatalf("timestamp %d: parse rejected its own encoding % x", ts, packet)
		}
		if uint64(got) != ts {
			t.Fatalf("timestamp %d: roundtrip = %d", ts, got)
		}
	}

	for name, packet := range map[string][]byte{
		"empty":          {},
		"header only":    {0x20},
		"voice type":     {0x80, 0x2A},
		"truncated":      {0x20, 0x80},       // varint continuation cut off
		"trailing bytes": {0x20, 0x2A, 0x00}, // varint ends early
	} {
		if _, ok := parsePingPacket(packet); ok {
			t.Fatalf("%s: parse accepted malformed ping % x", name, packet)
		}
	}
}

// TestUDPCryptResync drives the corruption→resync→recovery cycle over a
// real loopback UDP pair with production timing: after activation, eight
// undecryptable datagrams (crypt.ResyncThreshold consecutive failures)
// make the transport request a nonce resync via an empty CryptSetup on
// the TCP control channel; the server's {server_nonce} answer realigns
// the decrypt stream and the very next server packet decrypts again —
// the plan's "stream recovers in ≤ 2 packets after resync".
func TestUDPCryptResync(t *testing.T) {
	client, wire, fake := udpVoiceClient(t, true)
	waitTransport(t, client, "udp", 2*time.Second)
	pingsAfterActivation := client.UDPVoiceStats().PingsReceived

	// The server goes silent (no more ping replies) and its stream turns
	// to garbage from the client's perspective: every datagram fails to
	// decrypt. ResyncThreshold failures arm the resync trigger.
	fake.replyPings.Store(false)
	garbage := bytes.Repeat([]byte{0xEE}, 32)
	for i := 0; i < crypt.ResyncThreshold; i++ {
		fake.sendRaw(t, garbage)
	}

	// The transport must ask for new key material: an entirely empty
	// CryptSetup on the control channel.
	pType, body := readPacket(t, wire)
	if pType != 15 {
		t.Fatalf("resync request message type = %d, want 15 (CryptSetup)", pType)
	}
	var request MumbleProto.CryptSetup
	if err := proto.Unmarshal(body, &request); err != nil {
		t.Fatalf("unmarshal resync request: %v", err)
	}
	if request.Key != nil || request.ClientNonce != nil || request.ServerNonce != nil {
		t.Fatalf("resync request not empty: %s", request.String())
	}

	// The server answers with its current encrypt IV.
	if err := deliverCryptSetup(t, client, &MumbleProto.CryptSetup{
		ServerNonce: fake.cs.EncryptIV(),
	}); err != nil {
		t.Fatalf("resync answer CryptSetup: %v", err)
	}

	// First server packet after the resync must decrypt (recovery within
	// one packet); a second one keeps the stream aligned.
	fake.sendEncrypted(t, buildPingPacket(uint64(time.Now().UnixNano())))
	waitPingsReceived(t, client, pingsAfterActivation+1)
	afterRecovery := client.UDPVoiceStats().PingsReceived

	s := client.UDPVoiceStats()
	if s.ResyncsRequested != 1 {
		t.Fatalf("ResyncsRequested = %d, want 1", s.ResyncsRequested)
	}
	if s.DecryptFailed < uint32(crypt.ResyncThreshold) {
		t.Fatalf("DecryptFailed = %d, want >= %d", s.DecryptFailed, crypt.ResyncThreshold)
	}
	if s.Fallbacks != 0 || client.VoiceTransport() != "udp" {
		t.Fatalf("transport fell back during resync: stats=%+v transport=%q", s, client.VoiceTransport())
	}

	fake.sendEncrypted(t, buildPingPacket(uint64(time.Now().UnixNano())))
	waitPingsReceived(t, client, afterRecovery+1)
}

// TestUDPFallback blackholes the UDP socket — a bound sink that reads and
// discards, the loopback equivalent of a firewall drop without ICMP — and
// asserts the production-timing contract: no decryptable datagram within
// the 3 s handshake window → permanent fallback (well inside the plan's
// 5 s bound), after which voice packets take the TCP UDPTunnel with the
// exact golden framing bytes.
func TestUDPFallback(t *testing.T) {
	client, wire := framingConn(t)
	key, clientNonce, serverNonce, _ := cryptSetupMaterial(t)

	// The sink: bound (so no port-unreachable ICMP surfaces as a hard
	// read error) but it never answers a single datagram.
	sink, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen sink: %v", err)
	}
	t.Cleanup(func() { sink.Close() })
	go func() {
		buffer := make([]byte, udpReadBufferSize)
		for {
			if _, _, err := sink.ReadFrom(buffer); err != nil {
				return
			}
		}
	}()
	blackhole, err := net.DialTimeout("udp", sink.LocalAddr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial sink: %v", err)
	}
	t.Cleanup(func() { blackhole.Close() })

	client.udpVoice = newUDPTransport(client, blackhole)
	t.Cleanup(client.udpVoice.close)
	if err := deliverCryptSetup(t, client, &MumbleProto.CryptSetup{
		Key:         key,
		ClientNonce: clientNonce,
		ServerNonce: serverNonce,
	}); err != nil {
		t.Fatalf("initial CryptSetup: %v", err)
	}

	started := time.Now()
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		if client.UDPVoiceStats().Fallbacks == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	elapsed := time.Since(started)
	s := client.UDPVoiceStats()
	if s.Fallbacks != 1 {
		t.Fatalf("no fallback after %v (stats=%+v)", elapsed, s)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("fallback took %v, want < 5 s (handshake window 3 s + 1 s read cadence)", elapsed)
	}
	if s.FallbackAt.IsZero() || client.VoiceTransport() != "tcp" {
		t.Fatalf("fallback state inconsistent: stats=%+v transport=%q", s, client.VoiceTransport())
	}

	// Voice continues over the TCP tunnel with the golden framing bytes for
	// (Opus, target 0, seq 7, final, 3-byte payload): header 0x80,
	// varint 7, length 3|0x2000 → 0xA0 0x03, payload.
	frame := []byte{0xAA, 0xBB, 0xCC}
	if err := client.writeAudioPacket(4, 0, 7, true, frame, nil, nil, nil); err != nil {
		t.Fatalf("writeAudioPacket after fallback: %v", err)
	}
	pType, body := readPacket(t, wire)
	if pType != 1 {
		t.Fatalf("post-fallback voice message type = %d, want 1 (UDPTunnel)", pType)
	}
	golden := []byte{0x80, 0x07, 0xA0, 0x03, 0xAA, 0xBB, 0xCC}
	if !bytes.Equal(body, golden) {
		t.Fatalf("TCP tunnel body = % x, want golden % x", body, golden)
	}

	if got := client.UDPVoiceStats().Fallbacks; got != 1 {
		t.Fatalf("Fallbacks = %d after repeated use, want 1", got)
	}
}

// TestUDPVoiceDatagramMatchesTCPTunnel pins the framing contract on the wire:
// while UDP is active, the same writeAudioPacket input produces the exact
// same standalone packet bytes inside the encrypted datagram as the TCP
// UDPTunnel body carries after fallback — one framing function, two
// transports, byte-identical output.
func TestUDPVoiceDatagramMatchesTCPTunnel(t *testing.T) {
	client, wire, fake := udpVoiceClient(t, true)
	waitTransport(t, client, "udp", 2*time.Second)

	frame := []byte{0xAA, 0xBB, 0xCC}
	if err := client.writeAudioPacket(4, 0, 7, true, frame, nil, nil, nil); err != nil {
		t.Fatalf("writeAudioPacket over UDP: %v", err)
	}

	select {
	case plain := <-fake.voice:
		golden := []byte{0x80, 0x07, 0xA0, 0x03, 0xAA, 0xBB, 0xCC}
		if !bytes.Equal(plain, golden) {
			t.Fatalf("UDP datagram plaintext = % x, want golden % x", plain, golden)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the voice datagram on the fake server")
	}

	// Nothing leaked onto the TCP tunnel while UDP was healthy.
	select {
	case p := <-wire:
		t.Fatalf("packet %d unexpectedly took the TCP tunnel while UDP was active", p.pType)
	default:
	}

	if s := client.UDPVoiceStats(); s.VoiceSent != 1 {
		t.Fatalf("VoiceSent = %d, want 1", s.VoiceSent)
	}
}
