// CryptSetup (control message 15) drives Mumble's UDP crypto: the server
// hands the client an OCB2-AES128 key plus two rolling nonces right after
// authentication, and the same message type later carries nonce resyncs in
// both directions. The state machine mirrors the official client's
// MainWindow::msgCryptSetup and interops with Murmur's
// Server::msgCryptSetup / Server::doSync:
//
//	full {key, client_nonce, server_nonce} → SetKey(key, client_nonce,
//	    server_nonce): client_nonce encrypts packets to the server,
//	    server_nonce decrypts packets from it.
//	{server_nonce} only → answer to our (empty) resync request: adopt the
//	    nonce as the decrypt IV (CryptState.SetResync).
//	empty or {client_nonce} only → server-initiated resync (Murmur doSync,
//	    sent when the server fails to decrypt OUR packets): our stream is
//	    authoritative, so we reply with our current encrypt IV and change
//	    nothing locally; the server realigns its decrypt IV from the reply.
package gumble

import (
	"sync"

	"github.com/golang/protobuf/proto"
	"io.github/murmel/internal/protocol/crypt"
	"io.github/murmel/internal/protocol/gumble/MumbleProto"
)

// lockedCrypt is a mutex-guarded crypt.CryptState. The rolling OCB2 nonces
// make the state mutable from several goroutines at once — the TCP read
// loop (handleCryptSetup), the UDP receive loop (Decrypt), and the voice /
// ping writers (Encrypt) — so every access must hold mu. crypt.CryptState
// itself is not concurrency-safe.
type lockedCrypt struct {
	mu sync.Mutex
	cs *crypt.CryptState
}

// state lazily creates the underlying CryptState; it stays uninitialized
// (IsValid false) until a full CryptSetup arrives.
func (lc *lockedCrypt) state() *crypt.CryptState {
	if lc.cs == nil {
		lc.cs = crypt.New()
	}
	return lc.cs
}

func (c *Client) cryptSetKey(rawKey, encryptIV, decryptIV []byte) bool {
	c.crypt.mu.Lock()
	defer c.crypt.mu.Unlock()
	return c.crypt.state().SetKey(rawKey, encryptIV, decryptIV)
}

func (c *Client) cryptApplyResync(key, clientNonce, serverNonce []byte) error {
	c.crypt.mu.Lock()
	defer c.crypt.mu.Unlock()
	return c.crypt.state().SetResync(key, clientNonce, serverNonce)
}

func (c *Client) cryptEncryptIV() []byte {
	c.crypt.mu.Lock()
	defer c.crypt.mu.Unlock()
	return c.crypt.state().EncryptIV()
}

func (c *Client) cryptEncrypt(plain []byte) ([]byte, error) {
	c.crypt.mu.Lock()
	defer c.crypt.mu.Unlock()
	return c.crypt.state().Encrypt(plain)
}

func (c *Client) cryptDecrypt(packet []byte) ([]byte, error) {
	c.crypt.mu.Lock()
	defer c.crypt.mu.Unlock()
	return c.crypt.state().Decrypt(packet)
}

func (c *Client) cryptResyncNeeded() bool {
	c.crypt.mu.Lock()
	defer c.crypt.mu.Unlock()
	return c.crypt.state().ResyncNeeded()
}

func (c *Client) cryptGenResync() {
	c.crypt.mu.Lock()
	defer c.crypt.mu.Unlock()
	c.crypt.state().GenResync()
}

func (c *Client) cryptStats() crypt.Stats {
	c.crypt.mu.Lock()
	defer c.crypt.mu.Unlock()
	if c.crypt.cs == nil {
		return crypt.Stats{}
	}
	return c.crypt.cs.Stats()
}

func (c *Client) handleCryptSetup(buffer []byte) error {
	var packet MumbleProto.CryptSetup
	if err := proto.Unmarshal(buffer, &packet); err != nil {
		return err
	}

	switch {
	case packet.Key != nil && packet.ClientNonce != nil && packet.ServerNonce != nil:
		if !c.cryptSetKey(packet.Key, packet.ClientNonce, packet.ServerNonce) {
			return errInvalidProtobuf
		}
		if t := c.udpVoice; t != nil {
			t.cryptReady()
		}
	case packet.ServerNonce != nil:
		if err := c.cryptApplyResync(nil, nil, packet.ServerNonce); err != nil {
			return err
		}
	default:
		reply := MumbleProto.CryptSetup{
			ClientNonce: c.cryptEncryptIV(),
		}
		if err := c.Conn.WriteProto(&reply); err != nil {
			return err
		}
	}
	return nil
}

// requestCryptResync asks the server to re-send its current nonce: an empty
// CryptSetup on the control channel. Murmur answers with {server_nonce},
// handled above. GenResync resets the consecutive-decrypt-failure count, so
// the next request needs crypt.ResyncThreshold fresh failures.
func (c *Client) requestCryptResync() {
	c.cryptGenResync()
	_ = c.Conn.WriteProto(&MumbleProto.CryptSetup{})
}
