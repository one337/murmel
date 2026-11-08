package device

import (
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gen2brain/malgo"

	"io.github/murmel/internal/audio/pipeline"
	"io.github/murmel/internal/audio/ring"
)

// newRingDevice wires a Device to fresh rings without touching malgo;
// unit tests drive the data callback by hand, standing in for the
// audio thread.
func newRingDevice() *Device {
	return &Device{
		capRing:  ring.New(frameBytes, captureSlots),
		playRing: ring.New(frameBytes, playbackSlots),
		errCh:    make(chan error, 1),
	}
}

func TestDuplexConfig(t *testing.T) {
	cfg := duplexConfig(nil, nil)
	if cfg.DeviceType != malgo.Duplex {
		t.Errorf("DeviceType = %v, want Duplex", cfg.DeviceType)
	}
	if cfg.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", cfg.SampleRate)
	}
	if cfg.PeriodSizeInFrames != 480 {
		t.Errorf("PeriodSizeInFrames = %d, want 480", cfg.PeriodSizeInFrames)
	}
	if cfg.Periods != 2 {
		t.Errorf("Periods = %d, want 2", cfg.Periods)
	}
	if cfg.PerformanceProfile != malgo.LowLatency {
		t.Errorf("PerformanceProfile = %v, want LowLatency", cfg.PerformanceProfile)
	}
	if cfg.Capture.Format != malgo.FormatS16 || cfg.Playback.Format != malgo.FormatS16 {
		t.Errorf("formats = %v/%v, want S16/S16", cfg.Capture.Format, cfg.Playback.Format)
	}
	if cfg.Capture.Channels != 1 || cfg.Playback.Channels != 1 {
		t.Errorf("channels = %d/%d, want 1/1", cfg.Capture.Channels, cfg.Playback.Channels)
	}
	if cfg.Capture.DeviceID != nil || cfg.Playback.DeviceID != nil {
		t.Error("nil IDs must select the default devices")
	}
	if sampleRate != pipeline.SampleRate || periodFrames != pipeline.FrameSize || frameBytes != 2*pipeline.FrameSize {
		t.Fatal("stream constants drifted from the pipeline contract")
	}
}

func TestCallbackCaptureToRead(t *testing.T) {
	d := newRingDevice()
	in := make([]byte, frameBytes)
	for i := range in {
		in[i] = byte(i * 7)
	}
	out := make([]byte, frameBytes)

	d.data(out, in, periodFrames)

	if got := d.capRing.Len(); got != 1 {
		t.Fatalf("capture ring len = %d, want 1", got)
	}
	if d.Underruns() != 1 {
		t.Fatalf("underruns = %d, want 1 (playback ring empty)", d.Underruns())
	}
	for i, b := range out {
		if b != 0 {
			t.Fatalf("underrun period not silenced: out[%d]=%d", i, b)
		}
	}
	frame, err := d.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(frame) != periodFrames {
		t.Fatalf("frame len = %d, want %d", len(frame), periodFrames)
	}
	want := int16(uint16(in[0]) | uint16(in[1])<<8)
	if frame[0] != want || frame[1] != int16(uint16(in[2])|uint16(in[3])<<8) {
		t.Fatalf("LE conversion broken: frame[0]=%d frame[1]=%d, want %d", frame[0], frame[1], want)
	}
}

func TestCallbackWriteToPlayback(t *testing.T) {
	d := newRingDevice()
	var frame [periodFrames]int16
	for i := range frame {
		frame[i] = int16(1000 + i)
	}
	if err := d.Write(frame[:]); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := d.playRing.Len(); got != 1 {
		t.Fatalf("playback ring len = %d, want 1", got)
	}
	out := make([]byte, frameBytes)
	d.data(out, nil, periodFrames)
	for i := range frame {
		got := int16(uint16(out[2*i]) | uint16(out[2*i+1])<<8)
		if got != frame[i] {
			t.Fatalf("sample %d: got %d, want %d", i, got, frame[i])
		}
	}
	if d.Underruns() != 0 {
		t.Fatalf("underruns = %d, want 0", d.Underruns())
	}
}

func TestWritePadsAndTruncates(t *testing.T) {
	d := newRingDevice()
	short := []int16{7}
	if err := d.Write(short); err != nil {
		t.Fatalf("Write(short): %v", err)
	}
	out := make([]byte, frameBytes)
	d.data(out, nil, periodFrames)
	if got := int16(uint16(out[0]) | uint16(out[1])<<8); got != 7 {
		t.Fatalf("first sample = %d, want 7", got)
	}
	tail := out[2:frameBytes]
	for i, b := range tail {
		if b != 0 {
			t.Fatalf("short frame not zero-padded at byte %d: %d", i, b)
		}
	}

	long := make([]int16, periodFrames+50)
	long[periodFrames-1] = 42
	long[periodFrames] = 99 // must be truncated away
	if err := d.Write(long); err != nil {
		t.Fatalf("Write(long): %v", err)
	}
	d.data(out, nil, periodFrames)
	if got := int16(uint16(out[2*(periodFrames-1)]) | uint16(out[2*(periodFrames-1)+1])<<8); got != 42 {
		t.Fatalf("last in-range sample = %d, want 42", got)
	}
}

func TestCallbackCaptureDropOldestKeepsNewest(t *testing.T) {
	d := newRingDevice()
	in := make([]byte, frameBytes)
	for gen := 0; gen < captureSlots+3; gen++ {
		in[0] = byte(gen)
		in[1] = 0
		d.data(nil, in, periodFrames)
	}
	if got := d.CaptureDrops(); got != 3 {
		t.Fatalf("capture drops = %d, want 3", got)
	}
	for want := 3; want < captureSlots+3; want++ {
		frame, err := d.Read()
		if err != nil {
			t.Fatalf("Read(gen %d): %v", want, err)
		}
		if int(frame[0]) != want {
			t.Fatalf("got gen %d, want %d (newest frames must survive)", frame[0], want)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := d.Read(); err != io.EOF {
		t.Fatalf("Read after drain+close = %v, want io.EOF", err)
	}
}

func TestReadBlocksUntilFrameArrives(t *testing.T) {
	d := newRingDevice()
	type result struct {
		frame []int16
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		f, err := d.Read()
		ch <- result{f, err}
	}()
	select {
	case <-ch:
		t.Fatal("Read returned with an empty ring")
	case <-time.After(60 * time.Millisecond):
	}
	in := make([]byte, frameBytes)
	in[0], in[1] = 9, 0
	d.data(nil, in, periodFrames)
	select {
	case r := <-ch:
		if r.err != nil || r.frame[0] != 9 {
			t.Fatalf("Read result = (%d, %v), want (9, nil)", r.frame[0], r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not wake after the callback landed a frame")
	}
}

func TestCloseUnblocksReadAndSealsWrite(t *testing.T) {
	d := newRingDevice()
	go func() {
		time.Sleep(30 * time.Millisecond)
		d.Close()
	}()
	if _, err := d.Read(); err != io.EOF {
		t.Fatalf("blocked Read after Close = %v, want io.EOF", err)
	}
	if err := d.Write(make([]int16, periodFrames)); err != ErrClosed {
		t.Fatalf("Write after Close = %v, want ErrClosed", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil (idempotent)", err)
	}
}

func TestDeviceGoneDrainsThenEOF(t *testing.T) {
	d := newRingDevice()
	in := make([]byte, frameBytes)
	in[0] = 42
	d.data(nil, in, periodFrames)

	d.stopped()

	select {
	case err := <-d.Errors():
		if !errors.Is(err, ErrDeviceGone) {
			t.Fatalf("error = %v, want ErrDeviceGone", err)
		}
	default:
		t.Fatal("no device-gone error delivered")
	}
	frame, err := d.Read()
	if err != nil || frame[0] != 42 {
		t.Fatalf("drain Read = (%d, %v), want (42, nil)", frame[0], err)
	}
	if _, err := d.Read(); err != io.EOF {
		t.Fatalf("post-drain Read = %v, want io.EOF", err)
	}
}

func TestPlannedStopSignalsNothing(t *testing.T) {
	d := newRingDevice()
	d.Close()
	d.stopped()
	select {
	case err := <-d.Errors():
		t.Fatalf("planned stop must not signal, got %v", err)
	default:
	}
}

// Conservation law under concurrency: delivered == produced − drops.
// Exercises the exact Write/data and data/Read pairings the live
// device uses, across two goroutines, under the race detector.
func TestConcurrentCaptureConservation(t *testing.T) {
	const produced = 3000
	d := newRingDevice()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		in := make([]byte, frameBytes)
		for i := 0; i < produced; i++ {
			in[0] = byte(i)
			in[1] = byte(i >> 8)
			d.data(nil, in, periodFrames)
		}
		d.stopped() // stand-in for device end-of-life: unblocks Read
	}()

	delivered := 0
	last := -1
	for {
		frame, err := d.Read()
		if err != nil {
			break
		}
		v := int(frame[0])
		if v < last {
			t.Fatalf("out of order: gen %d after %d", v, last)
		}
		last = v
		delivered++
	}
	wg.Wait()

	if delivered == 0 {
		t.Fatal("nothing delivered")
	}
	if want := produced - int(d.CaptureDrops()); delivered != want {
		t.Fatalf("delivered %d, want %d (produced %d, drops %d)", delivered, want, produced, d.CaptureDrops())
	}
}

func TestConcurrentPlaybackConservation(t *testing.T) {
	const produced = 3000
	d := newRingDevice()
	var producerDone bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		frame := make([]int16, periodFrames)
		for i := 1; i <= produced; i++ {
			frame[0] = int16(i)
			if err := d.Write(frame); err != nil {
				t.Errorf("Write(%d): %v", i, err)
				return
			}
		}
		producerDone = true
	}()

	consumed := 0
	last := 0
	out := make([]byte, frameBytes)
	for {
		before := d.Underruns()
		d.data(out, nil, periodFrames)
		if d.Underruns() == before {
			v := int(int16(uint16(out[0]) | uint16(out[1])<<8))
			if v < last {
				t.Fatalf("out of order: gen %d after %d", v, last)
			}
			last = v
			consumed++
		}
		if producerDone && d.playRing.Len() == 0 {
			break
		}
		runtime.Gosched()
	}
	wg.Wait()

	if want := produced - int(d.PlaybackDrops()); consumed != want {
		t.Fatalf("consumed %d, want %d (produced %d, drops %d)", consumed, want, produced, d.PlaybackDrops())
	}
}

// TestInitNoDevice is the headless-safety gate: on a host without a
// sound server (or audio libraries), context init, GetDevices and Open
// must all fail with clean Go errors — no panic, cgo abort or hang. On
// hosts with live audio it skips (real-device verification is T20/F3).
func TestInitNoDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping live backend probe")
	}
	ctx, err := openContext(preferredBackends)
	if err != nil {
		t.Logf("headless environment confirmed: %v", err)
		if _, gerr := GetDevices(); gerr == nil {
			t.Fatal("GetDevices succeeded despite failed backend init")
		} else {
			t.Logf("GetDevices clean error: %v", gerr)
		}
		if _, oerr := Open(Config{}); oerr == nil {
			t.Fatal("Open succeeded despite failed backend init")
		} else {
			t.Logf("Open clean error: %v", oerr)
		}
		return
	}
	ctx.Uninit()
	ctx.Free()
	t.Skip("audio backends available; headless failure path runs in CI (evidence: task-12-headless-init.txt)")
}

func TestGetDevicesListsOrFails(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping live enumeration")
	}
	l, err := GetDevices()
	if err != nil {
		t.Skipf("no audio environment: %v", err)
	}
	t.Logf("capture=%d playback=%d devices", len(l.Capture), len(l.Playback))
	for _, info := range l.Capture {
		t.Logf("  capture: %q default=%v", info.Name, info.IsDefault)
	}
	for _, info := range l.Playback {
		t.Logf("  playback: %q default=%v", info.Name, info.IsDefault)
	}
}

// TestDuplexIntegration is opt-in (MURMEL_AUDIO_INTEGRATION=1): opens
// the real default duplex device, plays a tone and captures whatever
// the mic hears. Structural assertions only — content depends on the
// physical room.
func TestDuplexIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: no live audio")
	}
	if os.Getenv("MURMEL_AUDIO_INTEGRATION") == "" {
		t.Skip("opt-in: set MURMEL_AUDIO_INTEGRATION=1 (real-device verification happens in T20/F3)")
	}
	d, err := Open(Config{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	go func() {
		time.Sleep(5 * time.Second)
		d.Close() // watchdog: force Read EOF if the device stalls
	}()
	defer d.Close()

	var tone [periodFrames]int16
	for i := range tone {
		if i%2 == 0 {
			tone[i] = 8000
		} else {
			tone[i] = -8000
		}
	}
	for i := 0; i < 20; i++ {
		if err := d.Write(tone[:]); err != nil {
			t.Fatalf("Write(%d): %v", i, err)
		}
	}
	got := 0
	deadline := time.Now().Add(3 * time.Second)
	for got < 20 && time.Now().Before(deadline) {
		frame, err := d.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if len(frame) != periodFrames {
			t.Fatalf("frame len = %d, want %d", len(frame), periodFrames)
		}
		got++
	}
	if got == 0 {
		t.Fatalf("no frames captured (device stall?): drops=%d underruns=%d", d.CaptureDrops(), d.Underruns())
	}
	t.Logf("captured=%d frames, captureDrops=%d playbackDrops=%d underruns=%d",
		got, d.CaptureDrops(), d.PlaybackDrops(), d.Underruns())
}

// PipeWire/PulseAudio deliver callback buffers of arbitrary size; capture
// must still produce whole, correctly ordered 10 ms periods.
func TestCallbackRechunksOddCaptureSizes(t *testing.T) {
	d := newRingDevice()
	total := 3 * frameBytes
	src := make([]byte, total)
	for i := range src {
		src[i] = byte(i % 251)
	}
	for off, sizes := 0, []int{300, 1700, 2, 878}; off < total; {
		n := sizes[0]
		sizes = append(sizes[1:], n)
		if off+n > total {
			n = total - off
		}
		d.data(nil, src[off:off+n], uint32(n/2))
		off += n
	}
	if got := d.capRing.Len(); got != 3 {
		t.Fatalf("capture ring len = %d, want 3", got)
	}
	buf := make([]byte, frameBytes)
	for p := 0; p < 3; p++ {
		d.capRing.Pop(buf)
		for i, b := range buf {
			if b != src[p*frameBytes+i] {
				t.Fatalf("period %d byte %d = %d, want %d", p, i, b, src[p*frameBytes+i])
			}
		}
	}
}

// Playback output buffers of arbitrary size are served seamlessly from
// whole periods; only a dry ring produces silence.
func TestCallbackRechunksOddPlaybackSizes(t *testing.T) {
	d := newRingDevice()
	var want []byte
	for p := 0; p < 2; p++ {
		var frame [periodFrames]int16
		for i := range frame {
			frame[i] = int16(p*1000 + i)
		}
		if err := d.Write(frame[:]); err != nil {
			t.Fatal(err)
		}
		for _, s := range frame {
			want = append(want, byte(s), byte(s>>8))
		}
	}
	var got []byte
	for _, n := range []int{700, 1, 1000, 219} {
		out := make([]byte, n)
		d.data(out, nil, uint32(n/2))
		got = append(got, out...)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d bytes, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d = %d, want %d", i, got[i], want[i])
		}
	}
	if d.Underruns() != 0 {
		t.Fatalf("underruns = %d, want 0", d.Underruns())
	}
	out := make([]byte, 64)
	for i := range out {
		out[i] = 0xAA
	}
	d.data(out, nil, 32)
	for i, b := range out {
		if b != 0 {
			t.Fatalf("dry ring not silenced: out[%d]=%d", i, b)
		}
	}
}

func TestStreamConfigsSplitDuplex(t *testing.T) {
	p := streamConfigs(nil, nil, 0)
	if p.capture.DeviceType != malgo.Capture || p.playback.DeviceType != malgo.Playback {
		t.Fatalf("device types = %v/%v, want Capture/Playback", p.capture.DeviceType, p.playback.DeviceType)
	}
	for _, c := range []malgo.DeviceConfig{p.capture, p.playback} {
		if c.SampleRate != 48000 || c.Capture.Channels != 1 || c.Playback.Channels != 1 {
			t.Fatalf("stream format drifted: %+v", c)
		}
	}
}

func TestPlaybackPeriodSizing(t *testing.T) {
	if got := streamConfigs(nil, nil, 0).playback.PeriodSizeInFrames; got != 480 {
		t.Fatalf("default playback period = %d frames, want 480", got)
	}
	p := streamConfigs(nil, nil, 50*time.Millisecond)
	if p.playback.PeriodSizeInFrames != 2400 || p.capture.PeriodSizeInFrames != 480 {
		t.Fatalf("periods = capture %d / playback %d, want 480 / 2400",
			p.capture.PeriodSizeInFrames, p.playback.PeriodSizeInFrames)
	}
	if slots := playbackRingSlots(100 * time.Millisecond); slots < 20 {
		t.Fatalf("ring slots for 100 ms = %d, want ≥ 2 periods (20)", slots)
	}
}
