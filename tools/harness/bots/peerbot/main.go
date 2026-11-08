// Command peerbot is a scriptable Mumble peer for the Murmel QA harness.
//
// It connects, joins a channel, and can send text, speak a synthetic 440 Hz
// Opus sine, listen for voice, and assert expectations about what it received.
// It prints a JSON summary to stdout and exits 0 only if every expectation
// (--expect-text, --expect-nothing, --expect-voice-from) was met.
package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"layeh.com/gumble/gumble"
	"layeh.com/gumble/gumbleutil"
	_ "layeh.com/gumble/opus" // registers the Opus codec (gopus, embedded Opus 1.1.2)
)

const (
	sampleRate = 48000
	frameSize  = 480 // 10 ms at 48 kHz
	sineHz     = 440.0
	sineAmp    = 9000.0
)

type receivedText struct {
	Sender  string `json:"sender"`
	Message string `json:"message"`
}

type summary struct {
	OK               bool           `json:"ok"`
	Name             string         `json:"name"`
	Channel          string         `json:"channel"`
	Joined           bool           `json:"joined"`
	SendText         string         `json:"send_text,omitempty"`
	SpeakSeconds     int            `json:"speak_seconds"`
	BuffersSent      int            `json:"buffers_sent"`
	ListenSeconds    int            `json:"listen_seconds"`
	DecodedPackets   int            `json:"decoded_packets"`
	DecodedSamples   int            `json:"decoded_samples"`
	DecodedByUser    map[string]int `json:"decoded_by_user"`
	TextsReceived    []receivedText `json:"texts_received"`
	ExpectText       string         `json:"expect_text,omitempty"`
	ExpectTextMet    *bool          `json:"expect_text_met,omitempty"`
	ExpectNothing    bool           `json:"expect_nothing,omitempty"`
	ExpectNothingMet *bool          `json:"expect_nothing_met,omitempty"`
	ExpectVoiceFrom  string         `json:"expect_voice_from,omitempty"`
	ExpectVoiceMet   *bool          `json:"expect_voice_met,omitempty"`
	PM               string         `json:"pm,omitempty"`
	Error            string         `json:"error,omitempty"`

	col *collector
}

// collector gathers events from gumble listeners. Handlers run on the network
// read loop, so they only take a mutex and append — no blocking, no I/O.
type collector struct {
	mu             sync.Mutex
	decodedPackets int
	decodedSamples int
	byUser         map[string]int
	texts          []receivedText
	disconnected   string
	disconnectCh   chan struct{}
	once           sync.Once
}

func newCollector() *collector {
	return &collector{
		byUser:       make(map[string]int),
		disconnectCh: make(chan struct{}),
	}
}

// OnAudioStream implements gumble.AudioListener. The per-user channel is
// unbuffered and fed by the network read loop, so it must be drained from a
// separate goroutine or the whole client stalls.
func (c *collector) OnAudioStream(e *gumble.AudioStreamEvent) {
	go func() {
		for pkt := range e.C {
			c.mu.Lock()
			c.decodedPackets++
			c.decodedSamples += len(pkt.AudioBuffer)
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

func (c *collector) onDisconnect(e *gumble.DisconnectEvent) {
	if e.Type != gumble.DisconnectUser {
		reason := e.String
		if reason == "" {
			reason = fmt.Sprintf("type %d", int(e.Type))
		}
		c.mu.Lock()
		c.disconnected = reason
		c.mu.Unlock()
	}
	c.once.Do(func() { close(c.disconnectCh) })
}

func (c *collector) snapshot() (packets, samples int, byUser map[string]int, texts []receivedText, disconnected string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	byUser = make(map[string]int, len(c.byUser))
	for k, v := range c.byUser {
		byUser[k] = v
	}
	return c.decodedPackets, c.decodedSamples, byUser, c.texts, c.disconnected
}

func main() {
	addr := flag.String("addr", "localhost:64738", "server address (host:port)")
	name := flag.String("name", fmt.Sprintf("peerbot-%d", os.Getpid()), "username")
	channel := flag.String("channel", "Lobby", "channel name to join")
	speakSeconds := flag.Int("speak-seconds", 0, "send a synthetic 440 Hz Opus sine for N seconds")
	listenSeconds := flag.Int("listen-seconds", 0, "listen for voice/text for N seconds")
	sendText := flag.String("send-text", "", "send this text to the channel after joining")
	pm := flag.String("pm", "", "send --send-text as a private message to this user instead of the channel")
	expectText := flag.String("expect-text", "", "fail unless a received text contains this substring")
	expectNothing := flag.Bool("expect-nothing", false, "fail if any text message is received")
	expectVoiceFrom := flag.String("expect-voice-from", "", "fail unless voice packets are decoded from this user")
	certFile := flag.String("cert", "", "client certificate PEM file (optional)")
	keyFile := flag.String("key", "", "client key PEM file (defaults to --cert)")
	timeout := flag.Duration("timeout", 15*time.Second, "connect timeout")
	flag.Parse()

	if *expectText != "" && *listenSeconds <= 0 {
		fmt.Fprintln(os.Stderr, "peerbot: --expect-text requires --listen-seconds > 0")
		os.Exit(2)
	}
	if *expectNothing && *listenSeconds <= 0 {
		fmt.Fprintln(os.Stderr, "peerbot: --expect-nothing requires --listen-seconds > 0")
		os.Exit(2)
	}
	if *expectVoiceFrom != "" && *listenSeconds <= 0 {
		fmt.Fprintln(os.Stderr, "peerbot: --expect-voice-from requires --listen-seconds > 0")
		os.Exit(2)
	}
	if *pm != "" && *sendText == "" {
		fmt.Fprintln(os.Stderr, "peerbot: --pm requires --send-text")
		os.Exit(2)
	}

	s := summary{
		Name:            *name,
		Channel:         *channel,
		SendText:        *sendText,
		PM:              *pm,
		SpeakSeconds:    *speakSeconds,
		ListenSeconds:   *listenSeconds,
		ExpectText:      *expectText,
		ExpectNothing:   *expectNothing,
		ExpectVoiceFrom: *expectVoiceFrom,
		DecodedByUser:   map[string]int{},
	}

	if err := run(*addr, *name, *channel, *pm, *certFile, *keyFile, *timeout, &s); err != nil {
		s.Error = err.Error()
	}

	packets, samples, byUser, texts, disconnected := s.col.snapshot()
	s.DecodedPackets, s.DecodedSamples, s.DecodedByUser, s.TextsReceived = packets, samples, byUser, texts
	if s.Error == "" && disconnected != "" {
		s.Error = "disconnected: " + disconnected
	}

	s.OK = s.Error == ""
	if *expectText != "" {
		met := false
		for _, t := range texts {
			if strings.Contains(t.Message, *expectText) {
				met = true
				break
			}
		}
		s.ExpectTextMet = &met
		s.OK = s.OK && met
	}
	if *expectNothing {
		met := len(texts) == 0
		s.ExpectNothingMet = &met
		s.OK = s.OK && met
	}
	if *expectVoiceFrom != "" {
		_, _, byUser, _, _ := s.col.snapshot()
		met := byUser[*expectVoiceFrom] > 0
		s.ExpectVoiceMet = &met
		s.OK = s.OK && met
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		fmt.Fprintf(os.Stderr, "peerbot: json encode: %v\n", err)
	}
	if !s.OK {
		fmt.Fprintf(os.Stderr, "peerbot: expectations not met: %s\n", s.Error)
		os.Exit(1)
	}
}

func run(addr, name, channelName, pmUser, certFile, keyFile string, timeout time.Duration, s *summary) error {
	s.col = newCollector()

	config := gumble.NewConfig()
	config.Username = name
	config.Attach(gumbleutil.Listener{
		TextMessage: s.col.onTextMessage,
		Disconnect:  s.col.onDisconnect,
	})
	config.AttachAudio(s.col)

	tlsConfig := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // QA server uses a self-signed certificate
	if certFile != "" {
		if keyFile == "" {
			keyFile = certFile
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return fmt.Errorf("load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	client, err := gumble.DialWithDialer(&net.Dialer{Timeout: timeout}, addr, config, tlsConfig)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect() //nolint:errcheck // best-effort; may already be gone

	if err := joinChannel(client, channelName); err != nil {
		return err
	}
	s.Joined = true

	if s.SendText != "" {
		if pmUser != "" {
			user := findUser(client, pmUser)
			if user == nil {
				return fmt.Errorf("user %q not found for --pm", pmUser)
			}
			user.Send(s.SendText)
		} else if ch := findChannel(client, channelName); ch != nil {
			ch.Send(s.SendText, false)
		} else {
			return fmt.Errorf("channel %q vanished before text send", channelName)
		}
	}

	if s.SpeakSeconds > 0 {
		s.BuffersSent = speakSine(client, time.Duration(s.SpeakSeconds)*time.Second)
	}

	if s.ListenSeconds > 0 {
		listen(client, time.Duration(s.ListenSeconds)*time.Second, s.col.disconnectCh)
	}

	return nil
}

func joinChannel(client *gumble.Client, channelName string) error {
	ch := findChannel(client, channelName)
	if ch == nil {
		return fmt.Errorf("channel %q not found (run setupbot first)", channelName)
	}
	client.Self.Move(ch)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var joined bool
		client.Do(func() {
			joined = client.Self.Channel != nil && client.Self.Channel.ID == ch.ID
		})
		if joined {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out joining channel %q", channelName)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func findChannel(client *gumble.Client, name string) *gumble.Channel {
	var ch *gumble.Channel
	client.Do(func() {
		ch = client.Channels.Find(name)
	})
	return ch
}

func findUser(client *gumble.Client, name string) *gumble.User {
	var u *gumble.User
	client.Do(func() {
		u = client.Users.Find(name)
	})
	return u
}

// speakSine writes 480-sample sine buffers to the outgoing audio channel at
// real-time pace (10 ms per buffer) and closes it when done. Closing flushes
// the final buffer with the voice-terminator flag.
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

func listen(client *gumble.Client, d time.Duration, disconnectCh <-chan struct{}) {
	deadline := time.After(d)
	select {
	case <-deadline:
	case <-disconnectCh:
	}
}
