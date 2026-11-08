package gumble

import (
	"encoding/binary"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"io.github/murmel/internal/protocol/gumble/MumbleProto"
	"io.github/murmel/internal/protocol/gumble/varint"
)

// audioRecorder is an AudioListener that drains every stream it is offered
// and records the packets, mirroring how real consumers must read
// AudioStreamEvent.C continuously.
type audioRecorder struct {
	mu      sync.Mutex
	packets []*AudioPacket
	closed  chan struct{}
	once    sync.Once
}

func newAudioRecorder() *audioRecorder {
	return &audioRecorder{closed: make(chan struct{})}
}

func (r *audioRecorder) OnAudioStream(e *AudioStreamEvent) {
	go func() {
		for pkt := range e.C {
			r.mu.Lock()
			r.packets = append(r.packets, pkt)
			r.mu.Unlock()
		}
		r.once.Do(func() { close(r.closed) })
	}()
}

func (r *audioRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.packets)
}

func (r *audioRecorder) packet(i int) *AudioPacket {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.packets[i]
}

// newDispatchTestClient builds a manually wired Client (no network) with the
// project Opus codec registered, a single user, and an attached audio
// recorder — the minimum needed to drive handleUDPTunnel end to end.
func newDispatchTestClient(t *testing.T) (client *Client, user *User, rec *audioRecorder) {
	t.Helper()

	RegisterMurmelOpusCodec()

	client = &Client{
		Config:     NewConfig(),
		Users:      make(Users),
		Channels:   make(Channels),
		audioCodec: getAudioCodec(audioCodecIDOpus),
		audioQueue: make(chan *audioJob, audioDispatchQueueSize),
	}
	user = &User{Session: 42, Name: "speaker", client: client}
	client.Users[user.Session] = user

	rec = newAudioRecorder()
	client.Config.AudioListeners.Attach(rec)
	return client, user, rec
}

func sineFrame(t *testing.T) []byte {
	t.Helper()
	enc := getAudioCodec(audioCodecIDOpus).NewEncoder()
	if enc == nil {
		t.Fatal("no Opus encoder available")
	}
	pcm := make([]int16, AudioDefaultFrameSize)
	for i := range pcm {
		pcm[i] = int16(12000 * math.Sin(2*math.Pi*440*float64(i)/float64(AudioSampleRate)))
	}
	data, err := enc.Encode(pcm, AudioDefaultFrameSize, 127)
	if err != nil {
		t.Fatalf("encode sine frame: %v", err)
	}
	return data
}

// buildUDPTunnelPacket frames an Opus payload the way the server tunnels it:
// type/target byte, varint session, varint sequence, varint length, payload,
// optional 3x little-endian float32 positional data.
func buildUDPTunnelPacket(session uint32, seq int64, opus []byte, pos *[3]float32) []byte {
	var buf []byte
	buf = append(buf, byte(audioCodecIDOpus)<<5)
	var v [varint.MaxVarintLen]byte
	n := varint.Encode(v[:], int64(session))
	buf = append(buf, v[:n]...)
	n = varint.Encode(v[:], seq)
	buf = append(buf, v[:n]...)
	n = varint.Encode(v[:], int64(len(opus)))
	buf = append(buf, v[:n]...)
	buf = append(buf, opus...)
	if pos != nil {
		for _, f := range pos {
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], math.Float32bits(f))
			buf = append(buf, b[:]...)
		}
	}
	return buf
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestHandleUDPTunnelQueuesAudio verifies that handleUDPTunnel only queues
// the raw packet (no decode, no listener callbacks on the read loop) and
// that the dispatch goroutine then decodes it and delivers it — with
// positional data intact — to the attached audio listener.
func TestHandleUDPTunnelQueuesAudio(t *testing.T) {
	client, user, rec := newDispatchTestClient(t)

	opusData := sineFrame(t)
	pos := [3]float32{1.5, -2.25, 100.125}
	packet := buildUDPTunnelPacket(user.Session, 0, opusData, &pos)

	if err := client.handleUDPTunnel(packet); err != nil {
		t.Fatalf("handleUDPTunnel: %v", err)
	}

	// The dispatch goroutine has not been started: nothing was decoded on
	// the calling (read-loop) goroutine and no listener was touched.
	if user.decoder != nil {
		t.Fatal("handleUDPTunnel decoded audio on the read loop")
	}
	if got := rec.count(); got != 0 {
		t.Fatalf("listener received %d packets before dispatch", got)
	}

	// Payload copy must be owned by the job: clobbering the read buffer
	// after handleUDPTunnel returns must not corrupt the queued bytes.
	for i := range packet {
		packet[i] = 0xAA
	}

	go client.audioDispatchRoutine()

	waitFor(t, func() bool { return rec.count() == 1 })

	pkt := rec.packet(0)
	if pkt.Sender != user {
		t.Fatalf("packet sender = %v, want user %v", pkt.Sender, user)
	}
	if len(pkt.AudioBuffer) != AudioDefaultFrameSize {
		t.Fatalf("decoded %d samples, want %d", len(pkt.AudioBuffer), AudioDefaultFrameSize)
	}
	if !pkt.HasPosition {
		t.Fatal("positional data not delivered")
	}
	if pkt.X != pos[0] || pkt.Y != pos[1] || pkt.Z != pos[2] {
		t.Fatalf("position = (%v, %v, %v), want %v", pkt.X, pkt.Y, pkt.Z, pos)
	}
	if user.decoder == nil {
		t.Fatal("dispatch did not install a per-user decoder")
	}
}

// TestHandleUDPTunnelDropsWhenQueueFull verifies the bounded-queue contract:
// with the dispatch goroutine wedged, exactly the packets beyond the queue
// capacity are dropped and the caller never blocks.
func TestHandleUDPTunnelDropsWhenQueueFull(t *testing.T) {
	client, _, _ := newDispatchTestClient(t)

	opusData := sineFrame(t)
	packet := buildUDPTunnelPacket(42, 0, opusData, nil)

	// No dispatch goroutine: fill the queue completely, then push one more.
	for i := 0; i < audioDispatchQueueSize; i++ {
		if err := client.handleUDPTunnel(packet); err != nil {
			t.Fatalf("handleUDPTunnel #%d: %v", i, err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- client.handleUDPTunnel(packet) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handleUDPTunnel beyond capacity: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handleUDPTunnel blocked with a full queue")
	}
	if got := client.AudioDropped(); got != 1 {
		t.Fatalf("AudioDropped() = %d, want 1", got)
	}
}

// TestHandleUserRemoveQueuesGoneJob is the P5 companion: after a data job
// has been dispatched, a UserRemove for the same session must close the
// listener's stream channel and release the per-user decoder.
func TestHandleUserRemoveQueuesGoneJob(t *testing.T) {
	client, user, rec := newDispatchTestClient(t)

	packet := buildUDPTunnelPacket(user.Session, 0, sineFrame(t), nil)
	if err := client.handleUDPTunnel(packet); err != nil {
		t.Fatalf("handleUDPTunnel: %v", err)
	}
	go client.audioDispatchRoutine()
	waitFor(t, func() bool { return rec.count() == 1 && user.decoder != nil })

	remove, err := proto.Marshal(&MumbleProto.UserRemove{Session: proto.Uint32(user.Session)})
	if err != nil {
		t.Fatalf("marshal UserRemove: %v", err)
	}
	if err := client.handleUserRemove(remove); err != nil {
		t.Fatalf("handleUserRemove: %v", err)
	}

	select {
	case <-rec.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("stream channel was not closed after user disconnect")
	}
	if user.decoder != nil {
		t.Fatal("per-user decoder was not released on disconnect")
	}
}

// TestDispatchSweepClosesStreamsOnDisconnect covers the connection-teardown
// half of P5: when the audio queue is closed (readRoutine exit), every
// still-open per-user stream must be closed so listeners terminate even if
// no UserRemove was seen or its gone job was dropped on a full queue.
func TestDispatchSweepClosesStreamsOnDisconnect(t *testing.T) {
	client, user, rec := newDispatchTestClient(t)

	packet := buildUDPTunnelPacket(user.Session, 0, sineFrame(t), nil)
	if err := client.handleUDPTunnel(packet); err != nil {
		t.Fatalf("handleUDPTunnel: %v", err)
	}
	dispatchDone := make(chan struct{})
	go func() {
		client.audioDispatchRoutine()
		close(dispatchDone)
	}()
	waitFor(t, func() bool { return rec.count() == 1 })

	close(client.audioQueue)

	select {
	case <-rec.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("stream channel was not closed by the disconnect sweep")
	}
	select {
	case <-dispatchDone:
	case <-time.After(2 * time.Second):
		t.Fatal("audio dispatch goroutine did not exit")
	}
	if user.decoder == nil {
		t.Fatal("sweep must not run before the decoder was created by dispatch")
	}
}
