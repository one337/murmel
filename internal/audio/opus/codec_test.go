package opus

import (
	"math"
	"math/rand"
	"testing"

	gumble "layeh.com/gumble/gumble"
)

// Compile-time interface conformance: the wrapper must satisfy gumble's
// codec interfaces exactly (the vendored fork keeps them verbatim).
var (
	_ gumble.AudioEncoder = (*Encoder)(nil)
	_ gumble.AudioDecoder = (*Decoder)(nil)

	_ AudioEncoder = (*Encoder)(nil)
	_ AudioDecoder = (*Decoder)(nil)
	_ AudioCodec   = generator{}
)

// --- test helpers -----------------------------------------------------------

const testAmplitude = 12000 // of int16 max 32767

// sineFrame returns frameSize samples (mono 48 kHz) of a sine wave at freq Hz,
// starting at sample offset `phaseSamples`.
func sineFrame(freq float64, frameSize, phaseSamples int) []int16 {
	pcm := make([]int16, frameSize)
	for i := 0; i < frameSize; i++ {
		t := float64(phaseSamples+i) / float64(SampleRate)
		pcm[i] = int16(testAmplitude * math.Sin(2*math.Pi*freq*t))
	}
	return pcm
}

func rms(pcm []int16) float64 {
	if len(pcm) == 0 {
		return 0
	}
	var sum float64
	for _, s := range pcm {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(pcm)))
}

// risingZeroCrossings counts positive-going zero crossings.
func risingZeroCrossings(pcm []int16) int {
	n := 0
	for i := 1; i < len(pcm); i++ {
		if pcm[i-1] <= 0 && pcm[i] > 0 {
			n++
		}
	}
	return n
}

// encodeSine encodes n frames of `freq` Hz sine via enc and returns the
// encoded packets.
func encodeSine(t *testing.T, enc *Encoder, freq float64, n, maxDataBytes int) [][]byte {
	t.Helper()
	packets := make([][]byte, 0, n)
	for f := 0; f < n; f++ {
		pcm := sineFrame(freq, FrameSize, f*FrameSize)
		packet, err := enc.Encode(pcm, FrameSize, maxDataBytes)
		if err != nil {
			t.Fatalf("frame %d: encode: %v", f, err)
		}
		packets = append(packets, packet)
	}
	return packets
}

// --- interface / configuration ----------------------------------------------

func TestNewDefaults(t *testing.T) {
	enc, err := NewEncoder()
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if got := enc.ID(); got != CodecID {
		t.Errorf("encoder ID = %d, want %d (Opus)", got, CodecID)
	}
	if got := enc.Bitrate(); got != BitrateDefault {
		t.Errorf("default bitrate = %d, want %d", got, BitrateDefault)
	}

	dec, err := NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if got := dec.ID(); got != CodecID {
		t.Errorf("decoder ID = %d, want %d (Opus)", got, CodecID)
	}
}

func TestSetBitrateRangeEnforced(t *testing.T) {
	enc, err := NewEncoder()
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for _, b := range []int{BitrateMin, 16000, BitrateDefault, BitrateMax} {
		if err := enc.SetBitrate(b); err != nil {
			t.Errorf("SetBitrate(%d): unexpected error: %v", b, err)
		}
		if got := enc.Bitrate(); got != b {
			t.Errorf("Bitrate() = %d after SetBitrate(%d)", got, b)
		}
	}
	for _, b := range []int{-1, 0, 7999, BitrateMin - 1, BitrateMax + 1, 1000000} {
		if err := enc.SetBitrate(b); err == nil {
			t.Errorf("SetBitrate(%d): expected range error, got nil", b)
		}
	}
}

// --- roundtrip --------------------------------------------------------------

func TestRoundtripSine(t *testing.T) {
	enc, err := NewEncoder()
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	const frames = 100
	packets := encodeSine(t, enc, 440, frames, 127)

	// Skip the first two frames in quality metrics: libopus has ~6.5 ms of
	// encoder lookahead, so decoded audio lags the input slightly and the
	// very first decoded frames ramp up.
	var (
		rmsSum    float64
		crossings int
		samples   int
	)
	for f, packet := range packets {
		pcm, err := dec.Decode(packet, FrameSize)
		if err != nil {
			t.Fatalf("frame %d: decode: %v", f, err)
		}
		if len(pcm) != FrameSize {
			t.Fatalf("frame %d: decoded %d samples, want %d", f, len(pcm), FrameSize)
		}
		if f >= 2 {
			rmsSum += rms(pcm)
			crossings += risingZeroCrossings(pcm)
			samples += len(pcm)
		}
	}

	inRMS := rms(sineFrame(440, FrameSize, 0))
	// RMS: theoretical for a sine of this amplitude is amplitude/sqrt(2),
	// but measure from the generated PCM to avoid rounding drift.
	wantRMS := testAmplitude / math.Sqrt2
	if math.Abs(wantRMS-inRMS) > 100 {
		t.Fatalf("test self-check: input RMS %v != theoretical %v", inRMS, wantRMS)
	}
	gotRMS := rmsSum / float64(frames-2)
	if gotRMS < 0.8*wantRMS || gotRMS > 1.2*wantRMS {
		t.Errorf("roundtrip RMS = %.1f, want within 20%% of %.1f", gotRMS, wantRMS)
	}

	// Zero crossings: 440 Hz over (frames-2)*10 ms of audio.
	seconds := float64(samples) / float64(SampleRate)
	wantCross := 440 * seconds
	tol := 0.25 * wantCross
	if float64(crossings) < wantCross-tol || float64(crossings) > wantCross+tol {
		t.Errorf("rising zero crossings = %d, want %f (+/- %.1f)", crossings, wantCross, tol)
	}
	t.Logf("roundtrip: RMS in %.1f out %.1f (%.1f%%), crossings %d (want ~%.0f)",
		wantRMS, gotRMS, 100*gotRMS/wantRMS, crossings, wantCross)
}

func TestEncodeRejectsBadArgs(t *testing.T) {
	enc, err := NewEncoder()
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	pcm := sineFrame(440, FrameSize, 0)
	if _, err := enc.Encode(nil, FrameSize, 127); err == nil {
		t.Error("Encode(nil pcm): expected error")
	}
	if _, err := enc.Encode(pcm, FrameSize, 0); err == nil {
		t.Error("Encode(maxDataBytes=0): expected error")
	}
	if _, err := enc.Encode(pcm, 0, 127); err == nil {
		t.Error("Encode(frameSize=0): expected error")
	}
	// pcm length must be a whole number of frames
	if _, err := enc.Encode(pcm[:100], FrameSize, 127); err == nil {
		t.Error("Encode(pcm shorter than frameSize): expected error")
	}
	// two frames in one call must work (20 ms packet)
	two := append(sineFrame(440, FrameSize, 0), sineFrame(440, FrameSize, FrameSize)...)
	if _, err := enc.Encode(two, FrameSize, 127); err != nil {
		t.Errorf("Encode(2 frames): unexpected error: %v", err)
	}
}

// --- bitrate ----------------------------------------------------------------

func TestBitrateRespected(t *testing.T) {
	meanBytes := func(bps int) float64 {
		t.Helper()
		enc, err := NewEncoder()
		if err != nil {
			t.Fatalf("NewEncoder: %v", err)
		}
		if err := enc.SetBitrate(bps); err != nil {
			t.Fatalf("SetBitrate(%d): %v", bps, err)
		}
		const frames = 200
		packets := encodeSine(t, enc, 440, frames, 127)
		var sum int
		max := 0
		for i, p := range packets {
			if len(p) == 0 {
				t.Fatalf("frame %d: empty packet", i)
			}
			if len(p) > 127 {
				t.Fatalf("frame %d: %d bytes > maxDataBytes 127", i, len(p))
			}
			sum += len(p)
			if len(p) > max {
				max = len(p)
			}
		}
		mean := float64(sum) / float64(frames)
		t.Logf("bitrate %d bps: mean %.1f bytes/10ms (=%.0f bps), max %d",
			bps, mean, mean*8/0.01, max)
		return mean
	}

	mean32k := meanBytes(32000)
	mean16k := meanBytes(16000)
	mean64k := meanBytes(64000)

	// 32 kbps over 10 ms frames = 40 bytes budget. Opus is VBR, so allow
	// headroom, but the average must stay near the target.
	if mean32k > 48 {
		t.Errorf("32 kbps: mean %.1f bytes/frame exceeds 48-byte budget", mean32k)
	}
	if mean32k < 28 {
		t.Errorf("32 kbps: mean %.1f bytes/frame suspiciously low", mean32k)
	}
	// Configured bitrate must actually bite: lower target => fewer bytes.
	if mean16k >= mean32k {
		t.Errorf("16 kbps mean %.1f not below 32 kbps mean %.1f", mean16k, mean32k)
	}
	if mean64k <= mean32k {
		t.Errorf("64 kbps mean %.1f not above 32 kbps mean %.1f", mean64k, mean32k)
	}
}

// --- packet loss concealment -------------------------------------------------

func TestPLCOnEmptyInput(t *testing.T) {
	enc, err := NewEncoder()
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	packets := encodeSine(t, enc, 440, 16, 127)
	for _, p := range packets[:4] {
		if _, err := dec.Decode(p, FrameSize); err != nil {
			t.Fatalf("decode pre-roll: %v", err)
		}
	}

	// Three "lost" frames: empty and nil input both must produce a PLC frame.
	for i := 0; i < 3; i++ {
		var data []byte
		if i == 1 {
			data = []byte{}
		}
		pcm, err := dec.Decode(data, FrameSize)
		if err != nil {
			t.Fatalf("PLC frame %d: unexpected error: %v", i, err)
		}
		if len(pcm) != FrameSize {
			t.Fatalf("PLC frame %d: %d samples, want %d", i, len(pcm), FrameSize)
		}
		r := rms(pcm)
		t.Logf("PLC frame %d: RMS %.1f", i, r)
		if r < 300 {
			t.Errorf("PLC frame %d: RMS %.1f — decoder went silent, expected concealment audio", i, r)
		}
	}

	// libopus fades energy back in slowly after concealment (~200 ms ramp
	// measured: 22% of nominal on the first frame after loss, ~60% after
	// 90 ms) to avoid artifacts. Recovery = steady climb back to amplitude.
	var firstPost, lastPost float64
	for f := 7; f <= 15; f++ {
		pcm, err := dec.Decode(packets[f], FrameSize)
		if err != nil {
			t.Fatalf("decode after loss (frame %d): %v", f, err)
		}
		r := rms(pcm)
		if f == 7 {
			firstPost = r
		}
		t.Logf("recovery frame %d: RMS %.1f", f, r)
		lastPost = r
	}
	if firstPost < 1000 {
		t.Errorf("first frame after loss: RMS %.1f — silent", firstPost)
	}
	if lastPost <= 2*firstPost {
		t.Errorf("no recovery ramp: first %.1f, last %.1f", firstPost, lastPost)
	}
	if lastPost < 0.5*(testAmplitude/math.Sqrt2) {
		t.Errorf("recovery stalled at RMS %.1f (< 50%% of nominal %.1f)", lastPost, testAmplitude/math.Sqrt2)
	}
}

func TestDecodeFECFallback(t *testing.T) {
	enc, err := NewEncoder()
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	packets := encodeSine(t, enc, 440, 5, 127)
	for _, p := range packets {
		if _, err := dec.Decode(p, FrameSize); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	// fec=false must behave like Decode; fec=true with no in-band FEC data in
	// the packets falls back to concealment inside libopus (documented hraban
	// behavior) — either way: no panic, full frame of samples.
	pcm, err := dec.DecodeFEC(nil, FrameSize, true)
	if err != nil {
		t.Fatalf("DecodeFEC(nil, fec): %v", err)
	}
	if len(pcm) != FrameSize {
		t.Fatalf("DecodeFEC(nil, fec): %d samples, want %d", len(pcm), FrameSize)
	}
	pcm2, err := dec.DecodeFEC(packets[4], FrameSize, false)
	if err != nil {
		t.Fatalf("DecodeFEC(packet, no fec): %v", err)
	}
	if len(pcm2) != FrameSize {
		t.Fatalf("DecodeFEC(packet, no fec): %d samples, want %d", len(pcm2), FrameSize)
	}
}

// --- corrupt input ------------------------------------------------------------

func TestCorruptInput(t *testing.T) {
	dec, err := NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	// Prime the decoder with real audio so corrupt packets hit a decoder in
	// a realistic (mid-stream) state.
	enc, err := NewEncoder()
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for _, p := range encodeSine(t, enc, 440, 5, 127) {
		_, _ = dec.Decode(p, FrameSize)
	}

	rng := rand.New(rand.NewSource(20261104)) // deterministic garbage
	errors, decoded := 0, 0
	for i := 0; i < 100; i++ {
		n := 1 + rng.Intn(127)
		garbage := make([]byte, n)
		rng.Read(garbage)
		pcm, err := dec.Decode(garbage, FrameSize)
		if err != nil {
			errors++
			continue
		}
		// Rare random packets can be structurally valid Opus; require sane
		// output if so.
		decoded++
		if len(pcm) == 0 || len(pcm) > FrameSize {
			t.Fatalf("iteration %d: valid garbage decoded to %d samples", i, len(pcm))
		}
	}
	// Empirically ~15% of short random byte strings are structurally valid
	// Opus packets (many TOC byte values encode legal frame packings) and
	// libopus decodes them to noise. The corruption guarantee is: the large
	// majority is rejected with an error, anything accepted yields sane
	// output, and nothing panics or hangs.
	t.Logf("corrupt input: %d errors, %d accidental valid decodes", errors, decoded)
	if errors < 75 {
		t.Errorf("only %d/100 garbage packets rejected — decoder too lenient", errors)
	}

	// Crafted-invalid packets (code-3 frame count run lengths) must error.
	crafted := [][]byte{
		{0xFF, 0xFF},
		{0xF8, 0x02, 0x00, 0x00, 0x00, 0x00},
	}
	for i, c := range crafted {
		if _, err := dec.Decode(c, FrameSize); err == nil {
			t.Errorf("crafted invalid packet %d decoded without error", i)
		}
	}

	// Decoder still works after the garbage storm.
	packets := encodeSine(t, enc, 440, 3, 127)
	for i, p := range packets {
		if _, err := dec.Decode(p, FrameSize); err != nil {
			t.Fatalf("post-corruption decode %d: %v", i, err)
		}
	}
}

// --- reset --------------------------------------------------------------------

func TestReset(t *testing.T) {
	enc, err := NewEncoder()
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	dec, err := NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	packet, err := enc.Encode(sineFrame(440, FrameSize, 0), FrameSize, 127)
	if err != nil {
		t.Fatalf("encode before reset: %v", err)
	}
	if _, err := dec.Decode(packet, FrameSize); err != nil {
		t.Fatalf("decode before reset: %v", err)
	}

	enc.Reset()
	dec.Reset()

	packet2, err := enc.Encode(sineFrame(440, FrameSize, FrameSize), FrameSize, 127)
	if err != nil {
		t.Fatalf("encode after reset: %v", err)
	}
	pcm, err := dec.Decode(packet2, FrameSize)
	if err != nil {
		t.Fatalf("decode after reset: %v", err)
	}
	if r := rms(pcm); r < 0.5*(testAmplitude/math.Sqrt2) {
		t.Errorf("post-reset decode RMS %.1f too low", r)
	}
}

// --- registration seam ---------------------------------------------------------

// fakeRegistry mirrors gumble's codec registration surface
// (RegisterAudioCodec(id int, codec AudioCodec)) for testing the seam.
type fakeRegistry struct {
	id    int
	codec AudioCodec
}

func (f *fakeRegistry) RegisterAudioCodec(id int, codec AudioCodec) {
	f.id = id
	f.codec = codec
}

func TestRegisterInto(t *testing.T) {
	var f fakeRegistry
	RegisterInto(&f)
	if f.id != CodecID {
		t.Errorf("registered id = %d, want %d", f.id, CodecID)
	}
	if f.codec == nil {
		t.Fatal("no codec registered")
	}
	if got := f.codec.ID(); got != CodecID {
		t.Errorf("codec.ID() = %d, want %d", got, CodecID)
	}
	enc := f.codec.NewEncoder()
	if enc == nil {
		t.Fatal("NewEncoder() = nil")
	}
	dec := f.codec.NewDecoder()
	if dec == nil {
		t.Fatal("NewDecoder() = nil")
	}
	packet, err := enc.Encode(sineFrame(440, FrameSize, 0), FrameSize, 127)
	if err != nil {
		t.Fatalf("generator encoder: %v", err)
	}
	pcm, err := dec.Decode(packet, FrameSize)
	if err != nil {
		t.Fatalf("generator decoder: %v", err)
	}
	if len(pcm) != FrameSize {
		t.Fatalf("generator decode: %d samples", len(pcm))
	}
	// Generator encoder/decoder must satisfy the gumble-shaped interfaces.
	var (
		_ gumble.AudioEncoder = enc
		_ gumble.AudioDecoder = dec
	)
}
