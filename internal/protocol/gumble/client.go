package gumble

import (
	"crypto/tls"
	"errors"
	"fmt"
	"math"
	"net"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"
	"io.github/murmel/internal/protocol/gumble/MumbleProto"
)

// State is the current state of the client's connection to the server.
type State int

const (
	// StateDisconnected means the client is no longer connected to the server.
	StateDisconnected State = iota

	// StateConnected means the client is connected to the server and is
	// syncing initial information. This is an internal state that will
	// never be returned by Client.State().
	StateConnected

	// StateSynced means the client is connected to a server and has been sent
	// the server state.
	StateSynced
)

// ClientVersion is the protocol version that Client announces.
//
// Murmel patch: 1.4.0 instead of upstream's 1.3.0. Servers warn clients
// older than 1.4 that ChannelListener is unsupported; 1.4 clients tolerate
// listener state they do not display. 1.5.0 must not be announced — murmur
// then switches the client to the protobuf UDP audio format, which this
// fork does not implement.
const ClientVersion = 1<<16 | 4<<8 | 0

// Client is the type used to create a connection to a server.
type Client struct {
	// The User associated with the client.
	Self *User
	// The client's configuration.
	Config *Config
	// The underlying Conn to the server.
	Conn *Conn

	// The users currently connected to the server.
	Users Users
	// The connected server's channels.
	Channels    Channels
	permissions map[uint32]*Permission
	tmpACL      *ACL

	// Ping stats
	tcpPacketsReceived uint32
	tcpPingTimes       [12]float32
	tcpPingAvg         uint32
	tcpPingVar         uint32

	// A collection containing the server's context actions.
	ContextActions ContextActions

	// The audio encoder used when sending audio to the server.
	AudioEncoder AudioEncoder
	audioCodec   AudioCodec
	// audioQueue feeds the per-connection audio decode goroutine
	// (audioDispatchRoutine); audioDropped counts voice packets dropped
	// because the queue was full.
	audioQueue   chan *audioJob
	audioDropped uint32
	// To whom transmitted audio will be sent. The VoiceTarget must have already
	// been sent to the server for targeting to work correctly. Setting to nil
	// will disable voice targeting (i.e. switch back to regular speaking).
	VoiceTarget *VoiceTarget

	state uint32
	crypt lockedCrypt

	// udpVoice is the native UDP voice transport, created once by
	// DialWithDialer when Config.UDPVoice is set (nil otherwise — voice
	// always takes the TCP tunnel). It starts its handshake once key
	// material arrives via CryptSetup.
	udpVoice *udpTransport

	// volatile is held by the client when the internal data structures are being
	// modified.
	volatile rpwMutex

	connect         chan *RejectError
	end             chan struct{}
	disconnectEvent DisconnectEvent

	// Murmel patch (P12): server diagnostics.
	serverVersion atomic.Value // ServerVersion
	maxBandwidth  atomic.Int64
	serverCrypt   struct{ good, late, lost atomic.Uint32 }
}

// Dial is an alias of DialWithDialer(new(net.Dialer), addr, config, nil).
func Dial(addr string, config *Config) (*Client, error) {
	return DialWithDialer(new(net.Dialer), addr, config, nil)
}

// DialWithDialer connects to the Mumble server at the given address.
//
// The function returns after the connection has been established, the initial
// server information has been synced, and the OnConnect handlers have been
// called.
//
// nil and an error is returned if server synchronization does not complete by
// min(time.Now() + dialer.Timeout, dialer.Deadline), or if the server rejects
// the client.
func DialWithDialer(dialer *net.Dialer, addr string, config *Config, tlsConfig *tls.Config) (*Client, error) {
	start := time.Now()

	conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	if err != nil {
		return nil, err
	}

	client := &Client{
		Conn:     NewConn(conn),
		Config:   config,
		Users:    make(Users),
		Channels: make(Channels),

		permissions: make(map[uint32]*Permission),

		state: uint32(StateConnected),

		audioQueue: make(chan *audioJob, audioDispatchQueueSize),

		connect: make(chan *RejectError),
		end:     make(chan struct{}),
	}

	if config.UDPVoice {
		// A UDP socket that cannot even be dialed is a permanent
		// fallback: voice keeps flowing through the TCP tunnel.
		if udpConn, err := net.DialTimeout("udp", addr, 5*time.Second); err == nil {
			client.udpVoice = newUDPTransport(client, udpConn)
		}
	}

	go client.readRoutine()
	go client.audioDispatchRoutine()

	// Initial packets
	versionPacket := MumbleProto.Version{
		Version:   proto.Uint32(ClientVersion),
		Release:   proto.String("Murmel"),
		Os:        proto.String(runtime.GOOS),
		OsVersion: proto.String(runtime.GOARCH),
	}
	authenticationPacket := MumbleProto.Authenticate{
		Username: &client.Config.Username,
		Password: &client.Config.Password,
		Opus:     proto.Bool(getAudioCodec(audioCodecIDOpus) != nil),
		Tokens:   client.Config.Tokens,
	}
	client.Conn.WriteProto(&versionPacket)
	client.Conn.WriteProto(&authenticationPacket)

	go client.pingRoutine()

	var timeout <-chan time.Time
	{
		var deadline time.Time
		if !dialer.Deadline.IsZero() {
			deadline = dialer.Deadline
		}
		if dialer.Timeout > 0 {
			diff := start.Add(dialer.Timeout)
			if deadline.IsZero() || diff.Before(deadline) {
				deadline = diff
			}
		}
		if !deadline.IsZero() {
			timer := time.NewTimer(deadline.Sub(start))
			defer timer.Stop()
			timeout = timer.C
		}
	}

	select {
	case <-timeout:
		client.Conn.Close()
		return nil, errors.New("gumble: synchronization timeout")
	case <-client.end:
		// Murmel patch: the server closed the connection before ServerSync
		// or Reject; without this case Dial blocked forever.
		return nil, errors.New("gumble: connection closed by server during synchronization")
	case err := <-client.connect:
		if err != nil {
			client.Conn.Close()
			return nil, err
		}

		return client, nil
	}
}

// State returns the current state of the client.
func (c *Client) State() State {
	return State(atomic.LoadUint32(&c.state))
}

// AudioOutgoing creates a new channel that outgoing audio data can be written
// to. The channel must be closed after the audio stream is completed. Only
// a single channel should be open at any given time (i.e. close the channel
// before opening another).
func (c *Client) AudioOutgoing() chan<- AudioBuffer {
	return c.AudioOutgoingPositional(nil, nil, nil)
}

// writeAudioPacket routes one framed voice packet: over the encrypted UDP
// transport while it is active, otherwise (and after any fallback) through
// the TCP UDPTunnel. Both paths carry the same standalone packet bytes.
func (c *Client) writeAudioPacket(format, target byte, sequence int64, final bool, data []byte, X, Y, Z *float32) error {
	if t := c.udpVoice; t != nil {
		if packet, err := buildVoicePacket(format, target, sequence, final, data, X, Y, Z); err != nil {
			return err
		} else if t.sendVoicePacket(packet) {
			return nil
		}
	}
	return c.Conn.WriteAudio(format, target, sequence, final, data, X, Y, Z)
}

// VoiceTransport reports the transport outgoing voice packets currently
// take: "udp" once the encrypted UDP handshake succeeded, "tcp" before
// activation and after any fallback.
func (c *Client) VoiceTransport() string {
	if t := c.udpVoice; t != nil && t.isActive() {
		return "udp"
	}
	return "tcp"
}

// UDPVoiceStats returns a snapshot of the native UDP voice transport
// counters (zero-valued when UDP voice is not enabled).
func (c *Client) UDPVoiceStats() UDPVoiceStats {
	if c.udpVoice == nil {
		return UDPVoiceStats{}
	}
	return c.udpVoice.statsSnapshot()
}

// AudioOutgoingPositional is AudioOutgoing with positional coordinates: the
// X, Y and Z floats (little-endian, meters) are appended to every outgoing
// voice packet when X is non-nil. Coordinates are captured once per stream;
// nil X disables positional audio.
func (c *Client) AudioOutgoingPositional(X, Y, Z *float32) chan<- AudioBuffer {
	ch := make(chan AudioBuffer)
	go func() {
		var seq int64
		previous := <-ch
		for p := range ch {
			previous.writeAudio(c, seq, false, X, Y, Z)
			previous = p
			seq = (seq + 1) % math.MaxInt32
		}
		if previous != nil {
			previous.writeAudio(c, seq, true, X, Y, Z)
		}
	}()
	return ch
}

// pingRoutine sends ping packets to the server at regular intervals.
func (c *Client) pingRoutine() {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	var timestamp uint64
	var tcpPingAvg float32
	var tcpPingVar float32
	packet := MumbleProto.Ping{
		Timestamp:  &timestamp,
		TcpPackets: &c.tcpPacketsReceived,
		TcpPingAvg: &tcpPingAvg,
		TcpPingVar: &tcpPingVar,
	}

	t := time.Now()
	for {
		timestamp = uint64(t.UnixNano())
		tcpPingAvg = math.Float32frombits(atomic.LoadUint32(&c.tcpPingAvg))
		tcpPingVar = math.Float32frombits(atomic.LoadUint32(&c.tcpPingVar))
		c.Conn.WriteProto(&packet)

		select {
		case <-c.end:
			return
		case t = <-ticker.C:
			// continue to top of loop
		}
	}
}

// readRoutine reads protocol buffer messages from the server.
func (c *Client) readRoutine() {
	c.disconnectEvent = DisconnectEvent{
		Client: c,
		Type:   DisconnectError,
	}

	for {
		pType, data, err := c.Conn.ReadPacket()
		if err != nil {
			break
		}
		if int(pType) < len(handlers) {
			handlers[pType](c, data)
		}
	}

	wasSynced := c.State() == StateSynced
	atomic.StoreUint32(&c.state, uint32(StateDisconnected))
	close(c.end)
	close(c.audioQueue) // drains and exits the audio dispatch goroutine
	if c.udpVoice != nil {
		c.udpVoice.close()
	}
	if wasSynced {
		c.Config.Listeners.onDisconnect(&c.disconnectEvent)
	}
}

// RequestUserList requests that the server's registered user list be sent to
// the client.
func (c *Client) RequestUserList() {
	packet := MumbleProto.UserList{}
	c.Conn.WriteProto(&packet)
}

// RequestBanList requests that the server's ban list be sent to the client.
func (c *Client) RequestBanList() {
	packet := MumbleProto.BanList{
		Query: proto.Bool(true),
	}
	c.Conn.WriteProto(&packet)
}

// Disconnect disconnects the client from the server.
func (c *Client) Disconnect() error {
	if c.State() == StateDisconnected {
		return errors.New("gumble: client is already disconnected")
	}
	c.disconnectEvent.Type = DisconnectUser
	c.Conn.Close()
	return nil
}

// Do executes f in a thread-safe manner. It ensures that Client and its
// associated data will not be changed during the lifetime of the function
// call.
func (c *Client) Do(f func()) {
	c.volatile.RLock()
	defer c.volatile.RUnlock()

	f()
}

// Send will send a Message to the server.
func (c *Client) Send(message Message) {
	message.writeMessage(c)
}

// ServerVersion is the version the server announced.
type ServerVersion struct {
	Version   uint32 // legacy encoding: major<<16 | minor<<8 | patch
	Release   string
	OS        string
	OSVersion string
}

// String prefers the release string (the legacy numeric field caps the
// patch level at 255), followed by the server's OS.
func (v ServerVersion) String() string {
	ver := v.Release
	if ver == "" {
		ver = fmt.Sprintf("%d.%d.%d", v.Version>>16, (v.Version>>8)&0xff, v.Version&0xff)
	}
	if v.OS == "" {
		return ver
	}
	return fmt.Sprintf("%s (%s %s)", ver, v.OS, v.OSVersion)
}

// ServerVersion returns the server's announced version (zero before the
// server's Version message arrived).
func (c *Client) ServerVersion() ServerVersion {
	v, _ := c.serverVersion.Load().(ServerVersion)
	return v
}

// MaxBandwidth is the server's per-user bandwidth limit in bit/s from
// ServerSync (0 if the server sent none).
func (c *Client) MaxBandwidth() int { return int(c.maxBandwidth.Load()) }

// ServerCryptStats returns the server's counters for the UDP datagrams it
// received from this client, as reported in its ping replies: decrypted
// (good), out of order (late) and missing (lost).
func (c *Client) ServerCryptStats() (good, late, lost uint32) {
	return c.serverCrypt.good.Load(), c.serverCrypt.late.Load(), c.serverCrypt.lost.Load()
}

// TCPPing returns the average TCP control-channel round-trip time measured
// from the client's ping packets (0 before the first reply).
func (c *Client) TCPPing() time.Duration {
	ms := math.Float32frombits(atomic.LoadUint32(&c.tcpPingAvg))
	return time.Duration(float64(ms) * float64(time.Millisecond))
}
