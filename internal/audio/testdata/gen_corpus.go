//go:build ignore

// gen_corpus synthesizes the deterministic VAD test corpus into this
// directory: mono 48 kHz PCM16 WAV clips plus labels.csv with exact
// speech-region ground truth (milliseconds, derived from the synthesis
// loop itself, so labels are trustworthy by construction).
//
// Synthesis parameters (also documented in README.md):
//
//	room noise      one-pole lowpassed white noise, RMS 30 (≈ −60.8 dBFS)
//	speech          syllable trains: one-pole lowpassed noise, 15% attack
//	                and 30% release envelope per syllable, 120–180 ms
//	                syllables, 60–90 ms gaps, syllables normalized to the
//	                target episode RMS (normal: 300 ≈ −40.8 dBFS,
//	                soft: 150 ≈ −46.8 dBFS)
//	keyboard        exponentially decaying click bursts (τ = 2 ms, peak
//	                20000) over the noise floor, spacing 80–160 ms plus a
//	                fast-typing section at 60 ms
//	music           4-tone chord (220/277.18/329.63/440 Hz) with ±0.5%
//	                frequency vibrato only (constant amplitude), fading in
//	                over 1.2 s to RMS 60 (+6 dB over the noise floor —
//	                below the +10 dB open margin) and out over 1.3 s
//
// Run: go run gen_corpus.go  (from internal/audio/testdata)
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
)

const sampleRate = 48000
const frame = 480

func main() {
	rng := rand.New(rand.NewSource(11))

	type clip struct {
		name   string
		pcm    []int16
		events [][2]int // [startFrame, endFrame) speech regions
	}
	var clips []clip

	// --- speech_normal: 3 episodes over room noise -----------------
	{
		var pcm []int16
		var events [][2]int
		appendNoise(rng, &pcm, 30, 80)
		for range 3 {
			start := len(pcm) / frame
			syllableEpisode(rng, &pcm, 300, 8+rng.Intn(2))
			events = append(events, [2]int{start, len(pcm) / frame})
			appendNoise(rng, &pcm, 30, 70)
		}
		appendNoise(rng, &pcm, 30, 50)
		clips = append(clips, clip{"speech_normal.wav", pcm, events})
	}

	// --- speech_soft: quieter episodes ------------------------------
	{
		var pcm []int16
		var events [][2]int
		appendNoise(rng, &pcm, 30, 80)
		for range 2 {
			start := len(pcm) / frame
			syllableEpisode(rng, &pcm, 150, 8)
			events = append(events, [2]int{start, len(pcm) / frame})
			appendNoise(rng, &pcm, 30, 70)
		}
		appendNoise(rng, &pcm, 30, 50)
		clips = append(clips, clip{"speech_soft.wav", pcm, events})
	}

	// --- silence_room -----------------------------------------------
	{
		var pcm []int16
		appendNoise(rng, &pcm, 30, 500)
		clips = append(clips, clip{"silence_room.wav", pcm, nil})
	}

	// --- keyboard ----------------------------------------------------
	{
		var pcm []int16
		appendNoise(rng, &pcm, 30, 500)
		// deterministic click pattern: mixed spacing + fast-typing run
		pattern := []int{12, 9, 14, 8, 6, 6, 6, 6, 6, 13, 10, 15, 9, 12, 8, 6, 6, 6, 6, 6, 6, 11, 14, 9, 13, 10, 8, 6, 6, 6, 6, 12, 15, 9, 11}
		for _, gap := range pattern {
			click(rng, &pcm)
			appendNoise(rng, &pcm, 30, gap)
		}
		appendNoise(rng, &pcm, 30, 30)
		clips = append(clips, clip{"keyboard.wav", pcm, nil})
	}

	// --- music_background --------------------------------------------
	{
		var pcm []int16
		appendNoise(rng, &pcm, 30, 100)
		// structure: 1 s noise lead, 1.2 s fade-in, 3.5 s sustain,
		// 1.3 s fade-out; sustain RMS 60 = +6 dB over the noise floor
		const sustainRMS = 60
		freqs := []float64{220, 277.18, 329.63, 440}
		total := 100 + 120 + 350 + 130
		for i := 0; i < total*frame; i++ {
			fr := float64(len(pcm)) / frame
			var env float64
			switch {
			case fr < 100:
				env = 0
			case fr < 220:
				env = (fr - 100) / 120
			case fr < 570:
				env = 1
			default:
				env = math.Max(0, 1-(fr-570)/130)
			}
			t := float64(len(pcm)) / sampleRate
			v := 0.0
			for k, f := range freqs {
				vib := 1 + 0.005*math.Sin(2*math.Pi*5*float64(k+1)*t)
				v += math.Sin(2 * math.Pi * f * vib * t)
			}
			// v is a sum of 4 unit sines: RMS(v) = √2
			s := env * sustainRMS * v / math.Sqrt2
			pcm = append(pcm, int16(clamp(s, -32767, 32767)))
		}
		clips = append(clips, clip{"music_background.wav", pcm, nil})
	}

	// --- write files ---------------------------------------------------
	labels, err := os.Create("labels.csv")
	if err != nil {
		panic(err)
	}
	defer labels.Close()
	fmt.Fprintln(labels, "file,start_ms,end_ms")
	for _, c := range clips {
		if err := writeWAV(c.name, c.pcm); err != nil {
			panic(err)
		}
		for _, e := range c.events {
			fmt.Fprintf(labels, "%s,%d,%d\n", c.name, e[0]*10, e[1]*10)
		}
		fmt.Printf("%s: %.1f s, %d episodes\n", c.name, float64(len(c.pcm))/sampleRate, len(c.events))
	}
}

func randNoise(rng *rand.Rand, rms float64) float64 {
	a := rms * math.Sqrt(3)
	return rng.Float64()*2*a - a
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

// appendNoise appends n frames of one-pole lowpassed noise at target RMS.
func appendNoise(rng *rand.Rand, pcm *[]int16, rms float64, n int) {
	lp := 0.0
	buf := make([]int16, 0, n*frame)
	for i := 0; i < n*frame; i++ {
		x := randNoise(rng, rms)
		lp += 0.18 * (x - lp)
		buf = append(buf, int16(clamp(lp*math.Sqrt(10.15), -32767, 32767)))
	}
	// lowpass with a=0.18 leaves RMS factor sqrt(a/(2−a)) ≈ 0.313;
	// compensate to land near the target RMS, then normalize exactly
	normalize(buf, rms)
	*pcm = append(*pcm, buf...)
}

// syllableEpisode appends an episode of n syllables, normalizing each
// syllable to the target RMS. 60–90 ms gaps (deterministic from rng).
func syllableEpisode(rng *rand.Rand, pcm *[]int16, rms float64, n int) {
	lp := 0.0
	for s := 0; s < n; s++ {
		nFrames := 12 + rng.Intn(7) // 120–180 ms
		syl := make([]int16, 0, nFrames*frame)
		for f := 0; f < nFrames; f++ {
			for i := 0; i < frame; i++ {
				x := randNoise(rng, 1000)
				lp += 0.18 * (x - lp)
				env := 1.0
				pos := float64(f) / float64(nFrames)
				if pos < 0.15 {
					env = pos / 0.15
				} else if pos > 0.7 {
					env = math.Max(0, (1-pos)/0.3)
				}
				syl = append(syl, int16(clamp(lp*env, -32767, 32767)))
			}
		}
		normalize(syl, rms)
		*pcm = append(*pcm, syl...)
		appendNoise(rng, pcm, 30, 6+rng.Intn(4))
	}
}

// normalize scales buf in place so its RMS equals target.
func normalize(buf []int16, target float64) {
	var sum float64
	for _, v := range buf {
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return
	}
	cur := math.Sqrt(sum / float64(len(buf)))
	k := target / cur
	for i := range buf {
		buf[i] = int16(clamp(float64(buf[i])*k, -32767, 32767))
	}
}

// click appends a 3 ms exponentially decaying burst (τ = 2 ms).
func click(rng *rand.Rand, pcm *[]int16) {
	n := 3 * sampleRate / 1000
	for i := 0; i < n; i++ {
		decay := math.Exp(-float64(i) / (2 * sampleRate / 1000.0))
		v := (rng.Float64()*2 - 1) * 20000 * decay
		*pcm = append(*pcm, int16(clamp(v, -32767, 32767)))
	}
}

func writeWAV(name string, pcm []int16) error {
	dataLen := len(pcm) * 2
	hdr := make([]byte, 44)
	binary.LittleEndian.PutUint32(hdr[0:], 0x46464952) // "RIFF"
	binary.LittleEndian.PutUint32(hdr[4:], uint32(36+dataLen))
	copy(hdr[8:], "WAVE")
	copy(hdr[12:], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], 1) // PCM
	binary.LittleEndian.PutUint16(hdr[22:], 1) // mono
	binary.LittleEndian.PutUint32(hdr[24:], sampleRate)
	binary.LittleEndian.PutUint32(hdr[28:], sampleRate*2) // byte rate
	binary.LittleEndian.PutUint16(hdr[32:], 2)            // block align
	binary.LittleEndian.PutUint16(hdr[34:], 16)           // bits
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], uint32(dataLen))

	f, err := os.Create(name)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(hdr); err != nil {
		return err
	}
	raw := make([]byte, dataLen)
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(raw[2*i:], uint16(v))
	}
	_, err = f.Write(raw)
	return err
}
