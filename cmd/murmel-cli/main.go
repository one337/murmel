// Command murmel-cli is a thin console client over the vendored gumble
// fork, used as the Task-3 interop gate against the pinned Murmur 1.5 QA
// harness. Every subcommand is a self-contained connection that prints a
// single JSON object to stdout and exits 0 on success.
//
//	murmel-cli connect --addr localhost:64738 --cert tools/harness/testdata/client.pem
//	murmel-cli join Lobby
//	murmel-cli text "interop-hello"
//	murmel-cli voice-in --seconds 3
//	murmel-cli listen --seconds 5
//	murmel-cli stats
package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"sort"
	"sync"
	"time"

	"io.github/murmel/internal/protocol/gumble"
	"io.github/murmel/internal/protocol/gumbleutil"
)

const (
	sampleRate = 48000
	frameSize  = 480 // 10 ms at 48 kHz
	sineHz     = 440.0
	sineAmp    = 9000.0
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "connect":
		err = withFlags(os.Args[2:], cmdConnect)
	case "join":
		err = withFlags(os.Args[2:], cmdJoin)
	case "text":
		err = withFlags(os.Args[2:], cmdText)
	case "voice-in":
		err = withFlags(os.Args[2:], cmdVoiceIn)
	case "listen":
		err = withFlags(os.Args[2:], cmdListen)
	case "stats":
		err = withFlags(os.Args[2:], cmdStats)
	case "help", "-h", "--help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "murmel-cli: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "murmel-cli: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: murmel-cli <subcommand> [flags]

subcommands:
  connect            connect + version/codec negotiation, then disconnect
  join <channel>     join a channel and print the channel tree
  text <message>     send a text message to the joined channel
  voice-in           send a synthetic 440 Hz Opus sine (--seconds)
  listen             count decoded voice/text for --seconds
  stats              RTT, server version, codec, drop counter

common flags: -addr, -name, -channel, -cert, -seconds, -timeout, -udp
`)
}

// commonFlags are accepted by every subcommand.
type commonFlags struct {
	addr    string
	name    string
	channel string
	cert    string
	seconds int
	timeout time.Duration
	udp     bool
}

func declareCommon(fs *flag.FlagSet) *commonFlags {
	c := &commonFlags{}
	fs.StringVar(&c.addr, "addr", "localhost:64738", "server address (host:port)")
	fs.StringVar(&c.name, "name", "client", "username")
	fs.StringVar(&c.channel, "channel", "Lobby", "channel to join after connect")
	fs.StringVar(&c.cert, "cert", "", "client certificate PEM (cert+key combined); optional")
	fs.IntVar(&c.seconds, "seconds", 0, "duration in seconds (voice-in/listen)")
	fs.DurationVar(&c.timeout, "timeout", 15*time.Second, "connect timeout")
	fs.BoolVar(&c.udp, "udp", false, "native UDP voice with automatic TCP-tunnel fallback")
	return c
}

func withFlags(args []string, run func(c *commonFlags, args []string) error) error {
	fs := flag.NewFlagSet("", flag.ExitOnError)
	c := declareCommon(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	return run(c, fs.Args())
}

func emit(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "murmel-cli: json encode: %v\n", err)
	}
}

// dial establishes one synchronized connection with the project Opus codec
// registered: Murmur 1.5 is Opus-only, and registration must precede Dial so
// the Authenticate packet advertises Opus support.
func dial(c *commonFlags) (*gumble.Client, error) {
	gumble.RegisterMurmelOpusCodec()

	config := gumble.NewConfig()
	config.Username = c.name
	config.UDPVoice = c.udp

	tlsConfig := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // QA server uses a self-signed certificate
	if c.cert != "" {
		cert, err := tls.LoadX509KeyPair(c.cert, c.cert)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	client, err := gumble.DialWithDialer(&net.Dialer{Timeout: c.timeout}, c.addr, config, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return client, nil
}

func disconnect(client *gumble.Client) {
	_ = client.Disconnect() // best-effort; may already be gone
}

// joinChannel moves Self into the named channel and waits for the state to
// settle, mirroring the harness bots.
func joinChannel(client *gumble.Client, name string) (*gumble.Channel, error) {
	var ch *gumble.Channel
	client.Do(func() { ch = client.Channels.Find(name) })
	if ch == nil {
		return nil, fmt.Errorf("channel %q not found (run setupbot first)", name)
	}
	client.Self.Move(ch)

	deadline := time.Now().Add(5 * time.Second)
	for {
		var joined bool
		client.Do(func() {
			joined = client.Self.Channel != nil && client.Self.Channel.ID == ch.ID
		})
		if joined {
			return ch, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out joining channel %q", name)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func cmdConnect(c *commonFlags, _ []string) error {
	client, err := dial(c)
	if err != nil {
		return err
	}
	defer disconnect(client)

	root := "unknown"
	var userCount, channelCount int
	client.Do(func() {
		if r := client.Channels[0]; r != nil {
			root = r.Name
		}
		userCount = len(client.Users)
		channelCount = len(client.Channels)
	})

	emit(map[string]any{
		"connected":       true,
		"state":           "synced",
		"username":        client.Self.Name,
		"server":          root,
		"users":           userCount,
		"channels":        channelCount,
		"client_version":  fmt.Sprintf("%x", gumble.ClientVersion),
		"opus_negotiated": client.AudioEncoder != nil,
	})
	return nil
}

func cmdJoin(c *commonFlags, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("join: exactly one channel name expected")
	}
	channel := args[0]

	client, err := dial(c)
	if err != nil {
		return err
	}
	defer disconnect(client)

	ch, err := joinChannel(client, channel)
	if err != nil {
		return err
	}

	var tree []string
	var walk func(ch *gumble.Channel, indent string)
	walk = func(ch *gumble.Channel, indent string) {
		tree = append(tree, indent+ch.Name)
		var names []string
		kids := make(map[string]*gumble.Channel)
		for _, sub := range ch.Children {
			names = append(names, sub.Name)
			kids[sub.Name] = sub
		}
		sort.Strings(names)
		for _, name := range names {
			walk(kids[name], indent+"  ")
		}
	}
	client.Do(func() {
		if root := client.Channels[0]; root != nil {
			walk(root, "")
		}
	})

	emit(map[string]any{
		"joined":          true,
		"channel":         ch.Name,
		"current_channel": client.Self.Channel.Name,
		"tree":            tree,
	})
	return nil
}

func cmdText(c *commonFlags, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("text: exactly one message expected (quote it)")
	}
	message := args[0]

	client, err := dial(c)
	if err != nil {
		return err
	}
	defer disconnect(client)

	ch, err := joinChannel(client, c.channel)
	if err != nil {
		return err
	}

	// channel.Send has no server ack; a short settle delay keeps the
	// disconnect from racing the message through the server.
	ch.Send(message, false)
	time.Sleep(500 * time.Millisecond)

	emit(map[string]any{
		"sent":    true,
		"channel": ch.Name,
		"message": message,
	})
	return nil
}

func cmdVoiceIn(c *commonFlags, _ []string) error {
	if c.seconds <= 0 {
		return fmt.Errorf("voice-in: --seconds must be > 0")
	}
	client, err := dial(c)
	if err != nil {
		return err
	}
	defer disconnect(client)

	if client.AudioEncoder == nil {
		return fmt.Errorf("Opus was not negotiated (no audio encoder)")
	}
	if _, err := joinChannel(client, c.channel); err != nil {
		return err
	}

	// Let the UDP handshake settle before speaking so the reported
	// transport reflects the path voice actually took (activation is
	// immediate when UDP works; after the handshake window the
	// transport has fallen back to TCP for good).
	if c.udp {
		waitVoiceTransport(client, 4*time.Second)
	}

	sent := speakSine(client, time.Duration(c.seconds)*time.Second)

	stats := client.UDPVoiceStats()
	emit(map[string]any{
		"spoke":            true,
		"seconds":          c.seconds,
		"buffers_sent":     sent,
		"transport":        client.VoiceTransport(),
		"udp_requested":    c.udp,
		"udp_voice_sent":   stats.VoiceSent,
		"udp_fallbacks":    stats.Fallbacks,
		"opus_negotiated":  client.AudioEncoder != nil,
		"audio_dropped":    client.AudioDropped(),
	})
	return nil
}

// waitVoiceTransport blocks until the UDP voice transport activates or d
// elapses (handshake window 3 s + slack): after that the client has either
// switched to UDP or permanently fallen back to the TCP tunnel.
func waitVoiceTransport(client *gumble.Client, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if client.VoiceTransport() == "udp" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// speakSine writes 480-sample sine buffers to the outgoing audio channel at
// real-time pace; closing the channel flushes the final buffer with the
// voice-terminator flag.
func speakSine(client *gumble.Client, d time.Duration) int {
	out := client.AudioOutgoing()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	phase := 0.0
	sent := 0
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		<-ticker.C
		buf := make(gumble.AudioBuffer, frameSize)
		for i := range buf {
			phase += 2 * math.Pi * sineHz / sampleRate
			buf[i] = int16(sineAmp * math.Sin(phase))
		}
		out <- buf
		sent++
	}
	close(out)
	return sent
}

func cmdListen(c *commonFlags, _ []string) error {
	seconds := c.seconds
	if seconds <= 0 {
		seconds = 5
	}
	client, err := dial(c)
	if err != nil {
		return err
	}
	defer disconnect(client)

	if _, err := joinChannel(client, c.channel); err != nil {
		return err
	}

	col := newCollector()
	client.Config.AttachAudio(col)
	client.Config.Attach(gumbleutil.Listener{TextMessage: col.onTextMessage})

	time.Sleep(time.Duration(seconds) * time.Second)

	snap := col.snapshot()
	ustats := client.UDPVoiceStats()
	emit(map[string]any{
		"listened_seconds":  seconds,
		"channel":           c.channel,
		"transport":         client.VoiceTransport(),
		"udp_requested":     c.udp,
		"udp_voice_recv":    ustats.VoiceReceived,
		"udp_pings_recv":    ustats.PingsReceived,
		"udp_fallbacks":     ustats.Fallbacks,
		"decoded_packets":   snap.packets,
		"decoded_samples":   snap.samples,
		"decoded_by_user":   snap.byUser,
		"texts_received":    snap.texts,
	})
	return nil
}

func cmdStats(c *commonFlags, _ []string) error {
	client, err := dial(c)
	if err != nil {
		return err
	}
	defer disconnect(client)

	stats := map[string]any{
		"connected":       true,
		"opus_negotiated": client.AudioEncoder != nil,
		"audio_dropped":   client.AudioDropped(),
	}
	// RTT + server version via the fork's UDP ping (independent of the TCP
	// session); codec state comes from the live connection.
	if ping, err := gumble.Ping(c.addr, 0, 3*time.Second); err != nil {
		stats["ping_error"] = err.Error()
	} else {
		major, minor, patch := ping.Version.SemanticVersion()
		stats["rtt_ms"] = float64(ping.Ping.Microseconds()) / 1000
		stats["server_version"] = fmt.Sprintf("%d.%d.%d", major, minor, patch)
		stats["server_version_raw"] = ping.Version.Version
		stats["users"] = ping.ConnectedUsers
		stats["max_bitrate"] = ping.MaximumBitrate
	}
	emit(stats)
	return nil
}

// receivedText is one text message observed by the listen subcommand.
type receivedText struct {
	Sender  string `json:"sender"`
	Message string `json:"message"`
}

// collector gathers audio and text events for the listen subcommand.
// Handlers only take a mutex and append; audio streams are drained from
// their own goroutines.
type collector struct {
	mu      sync.Mutex
	packets int
	samples int
	byUser  map[string]int
	texts   []receivedText
}

func newCollector() *collector {
	return &collector{byUser: make(map[string]int)}
}

// OnAudioStream implements gumble.AudioListener.
func (c *collector) OnAudioStream(e *gumble.AudioStreamEvent) {
	go func() {
		for pkt := range e.C {
			c.mu.Lock()
			c.packets++
			c.samples += len(pkt.AudioBuffer)
			c.byUser[e.User.Name]++
			c.mu.Unlock()
		}
	}()
}

func (c *collector) onTextMessage(e *gumble.TextMessageEvent) {
	sender := ""
	if e.Sender != nil {
		sender = e.Sender.Name
	}
	c.mu.Lock()
	c.texts = append(c.texts, receivedText{Sender: sender, Message: e.Message})
	c.mu.Unlock()
}

type collectorSnapshot struct {
	packets int
	samples int
	byUser  map[string]int
	texts   []receivedText
}

func (c *collector) snapshot() collectorSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	byUser := make(map[string]int, len(c.byUser))
	for k, v := range c.byUser {
		byUser[k] = v
	}
	texts := make([]receivedText, len(c.texts))
	copy(texts, c.texts)
	return collectorSnapshot{packets: c.packets, samples: c.samples, byUser: byUser, texts: texts}
}
