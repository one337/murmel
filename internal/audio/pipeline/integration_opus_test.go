package pipeline

import (
	"context"
	"io"
	"math"
	"testing"
	"time"

	"io.github/murmel/internal/audio/opus"
)

// opusEncode/Decode adapt the T8 wrapper (internal/audio/opus) to the
// pipeline callbacks without touching that package.
func opusEncode(t *testing.T) (EncodeFunc, *opus.Encoder) {
	t.Helper()
	enc, err := opus.NewEncoder()
	if err != nil {
		t.Skipf("libopus unavailable: %v", err)
	}
	return func(pcm []int16) ([]byte, error) {
		return enc.Encode(pcm, opus.FrameSize, opus.MaxDataBytes)
	}, enc
}

func opusDecode(t *testing.T) (DecodeFunc, *opus.Decoder) {
	t.Helper()
	dec, err := opus.NewDecoder()
	if err != nil {
		t.Skipf("libopus unavailable: %v", err)
	}
	return func(pkt []byte) ([]int16, error) {
		return dec.Decode(pkt, opus.FrameSize)
	}, dec
}

func TestHeadlessVoiceLoop(t *testing.T) {
	const nFrames = 200 // 2 s of audio
	src := NewSinSource(440, 10000)
	fed := pacedSource{src: src, n: nFrames}
	pipe := newPktPipe()
	sink := NewRecordingSink()
	encF, _ := opusEncode(t)
	decF, _ := opusDecode(t)

	p := New(Config{
		Source:    &fed,
		Encode:    encF,
		PacketOut: pipe,
		PacketIn:  pipe,
		Decode:    decF,
		Sink:      sink,
	})
	p.Start(context.Background())
	p.Wait()

	st := p.Snapshot()
	if st.Encoded != nFrames || st.Played != nFrames {
		t.Fatalf("frames: encoded %d played %d, want %d", st.Encoded, st.Played, nFrames)
	}
	if st.DroppedFrames != 0 || st.DecodeErrors != 0 {
		t.Fatalf("drops %d decodeErrors %d", st.DroppedFrames, st.DecodeErrors)
	}

	// energy preservation through encode/decode (RMS within ±3 dB)
	samples := sink.Samples()
	if len(samples) != nFrames*FrameSize {
		t.Fatalf("captured %d samples", len(samples))
	}
	srcRMS := 10000 / math.Sqrt2
	gotRMS := RMS(samples)
	if gotRMS < srcRMS*0.7 || gotRMS > srcRMS*1.41 {
		t.Fatalf("RMS drift: got %.0f, source %.0f (±3 dB band)", gotRMS, srcRMS)
	}

	// spectral sanity: zero crossings track 440 Hz within 10 %
	crossings := 0
	for i := 1; i < len(samples); i++ {
		if samples[i-1] <= 0 && samples[i] > 0 {
			crossings++
		}
	}
	wantCross := 440 * float64(len(samples)) / float64(SampleRate)
	if float64(crossings) < wantCross*0.9 || float64(crossings) > wantCross*1.1 {
		t.Fatalf("zero crossings %d, want ≈%.0f (tone not preserved)", crossings, wantCross)
	}
	t.Logf("headless loop: %d frames, RMS %.0f (src %.0f), crossings %d ≈ %.0f, drops %d",
		st.Played, gotRMS, srcRMS, crossings, wantCross, st.DroppedFrames)
}

// TestHeadlessVoiceLoopWithLoss models packet loss on the mock network:
// every 10th packet is delivered as an empty payload, which the decoder
// must paper over with PLC (T8 Decode(nil) → concealment), so playback
// keeps a full frame cadence.
func TestHeadlessVoiceLoopWithLoss(t *testing.T) {
	const nFrames = 100
	src := NewSinSource(330, 8000)
	fed := pacedSource{src: src, n: nFrames}

	lossy := &lossyPacketPipe{dropEvery: 10}
	lossy.inner = newPktPipe()
	sink := NewRecordingSink()
	encF, _ := opusEncode(t)
	decF, _ := opusDecode(t)

	p := New(Config{
		Source:    &fed,
		Encode:    encF,
		PacketOut: lossy,
		PacketIn:  lossy,
		Decode:    decF,
		Sink:      sink,
	})
	p.Start(context.Background())
	p.Wait()

	st := p.Snapshot()
	if st.Played != nFrames {
		t.Fatalf("played %d, want %d (PLC must fill lost frames)", st.Played, nFrames)
	}
	if lossy.lost != nFrames/10 {
		t.Fatalf("loss injection broken: %d lost", lossy.lost)
	}
	if sink.RMS() < 100 {
		t.Fatalf("output collapsed under loss: RMS %.0f", sink.RMS())
	}
	t.Logf("loss loop: %d frames, %d lost → PLC-filled, RMS %.0f",
		st.Played, lossy.lost, sink.RMS())
}

// lossyPacketPipe forwards packets, replacing every dropEvery-th with an
// empty payload (the UDP-loss analogue that triggers PLC).
type lossyPacketPipe struct {
	inner     *pktPipe
	dropEvery int
	n, lost   int
}

func (l *lossyPacketPipe) WritePacket(pkt []byte) error {
	l.n++
	if l.n%l.dropEvery == 0 {
		l.lost++
		return l.inner.WritePacket(nil)
	}
	return l.inner.WritePacket(pkt)
}

func (l *lossyPacketPipe) ReadPacket() ([]byte, error) { return l.inner.ReadPacket() }
func (l *lossyPacketPipe) Close() error                { return l.inner.Close() }

// pacedSource wraps an infinite Source with a frame budget and a small
// per-frame delay (device-like cadence: the consumer goroutines are
// awake before the burst, so the bounded queues never overflow).
type pacedSource struct {
	src Source
	n   int
}

func (p *pacedSource) Read() ([]int16, error) {
	if p.n <= 0 {
		return nil, io.EOF
	}
	p.n--
	time.Sleep(100 * time.Microsecond)
	return p.src.Read()
}

// BenchmarkEncodeOpus and BenchmarkDecodeOpus measure the codec side of
// the ≤3 ms encode budget (libopus via cgo). Allocation here comes from
// the T8 wrapper's output buffers, not the pipeline; recorded in the
// task-11 evidence.
func BenchmarkEncodeOpus(b *testing.B) {
	enc, err := opus.NewEncoder()
	if err != nil {
		b.Skipf("libopus unavailable: %v", err)
	}
	frame := make([]int16, opus.FrameSize)
	for i := range frame {
		frame[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/float64(opus.SampleRate)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := enc.Encode(frame, opus.FrameSize, opus.MaxDataBytes); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeOpus(b *testing.B) {
	enc, err := opus.NewEncoder()
	if err != nil {
		b.Skipf("libopus unavailable: %v", err)
	}
	dec, err := opus.NewDecoder()
	if err != nil {
		b.Skipf("libopus unavailable: %v", err)
	}
	frame := make([]int16, opus.FrameSize)
	for i := range frame {
		frame[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/float64(opus.SampleRate)))
	}
	pkt, err := enc.Encode(frame, opus.FrameSize, opus.MaxDataBytes)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := dec.Decode(pkt, opus.FrameSize); err != nil {
			b.Fatal(err)
		}
	}
}
