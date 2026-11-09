package gumble

import (
	"bytes"
	"testing"

	"github.com/golang/protobuf/proto"
	"io.github/murmel/internal/protocol/crypt"
	"io.github/murmel/internal/protocol/gumble/MumbleProto"
)

// cryptSetupMaterial returns a fresh key/nonce triple plus a reference
// "server" CryptState configured to match what the client installs from it
// (the server encrypts with server_nonce and decrypts client_nonce).
func cryptSetupMaterial(t *testing.T) (key, clientNonce, serverNonce []byte, server *crypt.CryptState) {
	t.Helper()
	key = bytes.Repeat([]byte{0x11}, crypt.KeySize)
	clientNonce = bytes.Repeat([]byte{0x22}, crypt.IVSize)
	serverNonce = bytes.Repeat([]byte{0x33}, crypt.IVSize)
	server = crypt.New()
	if !server.SetKey(key, serverNonce, clientNonce) {
		t.Fatal("reference server SetKey failed")
	}
	return key, clientNonce, serverNonce, server
}

// deliverCryptSetup serializes and dispatches a CryptSetup message the way
// readRoutine would.
func deliverCryptSetup(t *testing.T, client *Client, msg *MumbleProto.CryptSetup) error {
	t.Helper()
	buffer, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal CryptSetup: %v", err)
	}
	return client.handleCryptSetup(buffer)
}

// TestCryptSetupFullKeyExchangeInstallsKeyMaterial pins the initial exchange:
// {key, client_nonce, server_nonce} installs key material exactly as
// MainWindow::msgCryptSetup does — SetKey(key, client_nonce, server_nonce) —
// making the state valid (client_nonce encrypts, server_nonce decrypts).
func TestCryptSetupFullKeyExchangeInstallsKeyMaterial(t *testing.T) {
	client, _ := framingConn(t)
	key, clientNonce, serverNonce, server := cryptSetupMaterial(t)

	msg := &MumbleProto.CryptSetup{
		Key:         key,
		ClientNonce: clientNonce,
		ServerNonce: serverNonce,
	}
	if err := deliverCryptSetup(t, client, msg); err != nil {
		t.Fatalf("handleCryptSetup(full): %v", err)
	}

	client.crypt.mu.Lock()
	defer client.crypt.mu.Unlock()
	cs := client.crypt.cs
	if !cs.IsValid() {
		t.Fatal("crypt state not valid after full CryptSetup")
	}
	if got := cs.RawKey(); !bytes.Equal(got, key) {
		t.Fatalf("raw key = % x, want % x", got, key)
	}
	if got := cs.EncryptIV(); !bytes.Equal(got, clientNonce) {
		t.Fatalf("encrypt IV = % x, want client_nonce % x", got, clientNonce)
	}
	if got := cs.DecryptIV(); !bytes.Equal(got, serverNonce) {
		t.Fatalf("decrypt IV = % x, want server_nonce % x", got, serverNonce)
	}

	// Interop sanity: a packet the reference server encrypts must decrypt.
	plain := []byte{0x20, 0x2A} // ping header + varint payload
	packet, err := server.Encrypt(plain)
	if err != nil {
		t.Fatalf("server encrypt: %v", err)
	}
	got, err := cs.Decrypt(packet)
	if err != nil {
		t.Fatalf("client decrypt of server packet: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("decrypted = % x, want % x", got, plain)
	}
}

// TestCryptSetupServerNonceResyncRecoversDecryptStream pins the reply to a
// client-initiated resync request: a CryptSetup carrying only server_nonce
// replaces the decrypt IV (SetResync(nil, nil, nonce)), after which the
// server's stream decrypts again and the resync counter incremented.
func TestCryptSetupServerNonceResyncRecoversDecryptStream(t *testing.T) {
	client, _ := framingConn(t)
	key, clientNonce, serverNonce, server := cryptSetupMaterial(t)

	if err := deliverCryptSetup(t, client, &MumbleProto.CryptSetup{
		Key:         key,
		ClientNonce: clientNonce,
		ServerNonce: serverNonce,
	}); err != nil {
		t.Fatalf("initial CryptSetup: %v", err)
	}

	// Desynchronize the client's receive stream (any cause: corruption,
	// missed wraparound...): point the decrypt IV somewhere the server
	// never was and prove the next packet fails.
	garbage := bytes.Repeat([]byte{0xEE}, crypt.IVSize)
	client.crypt.mu.Lock()
	if !client.crypt.cs.SetDecryptIV(garbage) {
		t.Fatal("SetDecryptIV(garbage) failed")
	}
	packet, err := server.Encrypt([]byte{0x20, 0x01})
	if err != nil {
		t.Fatalf("server encrypt: %v", err)
	}
	if _, err := client.crypt.cs.Decrypt(packet); err == nil {
		t.Fatal("decrypt unexpectedly succeeded with desynchronized IV")
	}
	client.crypt.mu.Unlock()

	// The server answers our (empty) resync request with its current
	// encrypt IV in server_nonce.
	if err := deliverCryptSetup(t, client, &MumbleProto.CryptSetup{
		ServerNonce: server.EncryptIV(),
	}); err != nil {
		t.Fatalf("resync CryptSetup: %v", err)
	}

	client.crypt.mu.Lock()
	defer client.crypt.mu.Unlock()
	packet, err = server.Encrypt([]byte{0x20, 0x02})
	if err != nil {
		t.Fatalf("server encrypt after resync: %v", err)
	}
	got, err := client.crypt.cs.Decrypt(packet)
	if err != nil {
		t.Fatalf("decrypt after resync: %v", err)
	}
	if want := []byte{0x20, 0x02}; !bytes.Equal(got, want) {
		t.Fatalf("decrypted = % x, want % x", got, want)
	}
	if s := client.crypt.cs.Stats(); s.Resync != 1 {
		t.Fatalf("stats.Resync = %d, want 1", s.Resync)
	}
}

// TestCryptSetupServerInitiatedResyncRepliesEncryptIV pins server-initiated
// resync (Murmur Server::doSync): the server sends an empty CryptSetup when
// ITS decrypt of our packets fails (it cannot tell us a nonce to use, since
// our stream is the authoritative one). Like the official client's else
// branch, we answer with our current encrypt IV so the server can realign —
// without touching our own state. A client_nonce-only message takes the same
// path (upstream treats both as "no key, no server_nonce").
func TestCryptSetupServerInitiatedResyncRepliesEncryptIV(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  *MumbleProto.CryptSetup
	}{
		{"empty (doSync)", &MumbleProto.CryptSetup{}},
		{"client_nonce only", &MumbleProto.CryptSetup{ClientNonce: bytes.Repeat([]byte{0x44}, crypt.IVSize)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, packets := framingConn(t)
			key, clientNonce, serverNonce, _ := cryptSetupMaterial(t)
			if err := deliverCryptSetup(t, client, &MumbleProto.CryptSetup{
				Key:         key,
				ClientNonce: clientNonce,
				ServerNonce: serverNonce,
			}); err != nil {
				t.Fatalf("initial CryptSetup: %v", err)
			}

			// Advance the encrypt IV so the reply is distinguishable
			// from the initial nonce.
			client.crypt.mu.Lock()
			if _, err := client.crypt.cs.Encrypt([]byte{0x20, 0x01}); err != nil {
				client.crypt.mu.Unlock()
				t.Fatalf("advance encrypt IV: %v", err)
			}
			want := client.crypt.cs.EncryptIV()
			client.crypt.mu.Unlock()

			if err := deliverCryptSetup(t, client, tc.msg); err != nil {
				t.Fatalf("handleCryptSetup(%s): %v", tc.name, err)
			}

			pType, body := readPacket(t, packets)
			if pType != 15 {
				t.Fatalf("reply message type = %d, want 15 (CryptSetup)", pType)
			}
			var reply MumbleProto.CryptSetup
			if err := proto.Unmarshal(body, &reply); err != nil {
				t.Fatalf("unmarshal reply: %v", err)
			}
			if !bytes.Equal(reply.ClientNonce, want) {
				t.Fatalf("reply client_nonce = % x, want encrypt IV % x", reply.ClientNonce, want)
			}
			if reply.Key != nil || reply.ServerNonce != nil {
				t.Fatalf("reply carries unexpected fields: key=%v server_nonce=%v", reply.Key != nil, reply.ServerNonce != nil)
			}

			// Our own state must be untouched.
			client.crypt.mu.Lock()
			defer client.crypt.mu.Unlock()
			if got := client.crypt.cs.EncryptIV(); !bytes.Equal(got, want) {
				t.Fatalf("encrypt IV changed to % x, want % x", got, want)
			}
		})
	}
}

// TestCryptSetupRejectsBadMaterial pins error handling: wrong-length key or
// nonce fields are rejected without corrupting the installed state.
func TestCryptSetupRejectsBadMaterial(t *testing.T) {
	client, _ := framingConn(t)
	key, clientNonce, serverNonce, _ := cryptSetupMaterial(t)
	if err := deliverCryptSetup(t, client, &MumbleProto.CryptSetup{
		Key:         key,
		ClientNonce: clientNonce,
		ServerNonce: serverNonce,
	}); err != nil {
		t.Fatalf("initial CryptSetup: %v", err)
	}

	if err := deliverCryptSetup(t, client, &MumbleProto.CryptSetup{
		Key:         []byte{0x01}, // wrong length
		ClientNonce: clientNonce,
		ServerNonce: serverNonce,
	}); err == nil {
		t.Fatal("handleCryptSetup accepted a malformed key")
	}

	shortNonce := &MumbleProto.CryptSetup{ServerNonce: []byte{0x01, 0x02}}
	if err := deliverCryptSetup(t, client, shortNonce); err == nil {
		t.Fatal("handleCryptSetup accepted a malformed resync nonce")
	}

	client.crypt.mu.Lock()
	defer client.crypt.mu.Unlock()
	if got := client.crypt.cs.RawKey(); !bytes.Equal(got, key) {
		t.Fatal("crypt state corrupted by rejected messages")
	}
	if !client.crypt.cs.IsValid() {
		t.Fatal("crypt state invalidated by rejected messages")
	}
}

// TestRequestCryptResyncSendsEmptyMessage pins the client side of a resync
// request: an entirely empty CryptSetup message (all fields nil) on the
// control channel, which Murmur answers with its current encrypt IV in
// server_nonce. GenResync resets the consecutive-failure count, so a second
// request needs ResyncThreshold fresh failures.
func TestRequestCryptResyncSendsEmptyMessage(t *testing.T) {
	client, packets := framingConn(t)
	key, clientNonce, serverNonce, _ := cryptSetupMaterial(t)
	if err := deliverCryptSetup(t, client, &MumbleProto.CryptSetup{
		Key:         key,
		ClientNonce: clientNonce,
		ServerNonce: serverNonce,
	}); err != nil {
		t.Fatalf("initial CryptSetup: %v", err)
	}

	// Burn ResyncThreshold decrypts with garbage to arm the trigger.
	for i := 0; i < crypt.ResyncThreshold; i++ {
		if _, err := client.cryptDecrypt([]byte{0x00, 0x01, 0x02, 0x03}); err == nil {
			t.Fatalf("garbage packet %d decrypted unexpectedly", i)
		}
	}
	if !client.cryptResyncNeeded() {
		t.Fatal("ResyncNeeded false after threshold failures")
	}

	client.requestCryptResync()

	pType, body := readPacket(t, packets)
	if pType != 15 {
		t.Fatalf("message type = %d, want 15 (CryptSetup)", pType)
	}
	var msg MumbleProto.CryptSetup
	if err := proto.Unmarshal(body, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Key != nil || msg.ClientNonce != nil || msg.ServerNonce != nil {
		t.Fatalf("resync request not empty: %+v", msg.String())
	}
	if client.cryptResyncNeeded() {
		t.Fatal("ResyncNeeded still true after request (GenResync should reset)")
	}
}
