package pipeline

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const corpusDir = "../testdata"

type region struct {
	start, end int // frames (10 ms)
}

func loadLabels(t *testing.T) map[string][]region {
	t.Helper()
	f, err := os.Open(filepath.Join(corpusDir, "labels.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]region{}
	for i, row := range rows {
		if i == 0 || len(row) != 3 {
			continue
		}
		var s, e int
		fmt.Sscanf(row[1], "%d", &s)
		fmt.Sscanf(row[2], "%d", &e)
		out[row[0]] = append(out[row[0]], region{s / 10, e / 10})
	}
	return out
}

func loadClip(t *testing.T, name string) []int16 {
	t.Helper()
	clip, err := ReadWAV(filepath.Join(corpusDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if clip.SampleRate != SampleRate {
		t.Fatalf("%s: sample rate %d", name, clip.SampleRate)
	}
	return clip.PCM
}

// runVAD feeds a clip through a fresh VAD and returns per-frame
// decisions and transmitted-voice flags (Start/Continue decisions plus
// the pre-roll frames a Start flushes — the audio actually transmitted).
func runVAD(pcm []int16) (decisions []Decision, transmitted []bool) {
	v := NewVAD(VADConfig{})
	nFrames := (len(pcm) + FrameSize - 1) / FrameSize
	decisions = make([]Decision, nFrames)
	transmitted = make([]bool, nFrames)
	frame := make([]int16, FrameSize)
	for i := 0; i < nFrames; i++ {
		n := copy(frame, pcm[i*FrameSize:])
		for j := n; j < FrameSize; j++ {
			frame[j] = 0
		}
		d := v.Sample(frame)
		decisions[i] = d
		if d.Voice() {
			transmitted[i] = true
		}
		if d == Start {
			for p := 1; p <= v.PreRollFrames() && i-p >= 0; p++ {
				transmitted[i-p] = true
			}
		}
	}
	return decisions, transmitted
}

// TestVADCorpus runs the labeled corpus: ≥95 % of labeled speech frames
// must be transmitted (decisions + pre-roll), and ≤5 % of noise-corpus
// frames may be flagged voice.
func TestVADCorpus(t *testing.T) {
	labels := loadLabels(t)

	speechFiles := []string{"speech_normal.wav", "speech_soft.wav"}
	noiseFiles := []string{"silence_room.wav", "keyboard.wav", "music_background.wav"}

	for _, name := range speechFiles {
		pcm := loadClip(t, name)
		decisions, transmitted := runVAD(pcm)
		regions := labels[name]
		if len(regions) == 0 {
			t.Fatalf("%s: no labels", name)
		}

		var labeled, covered, decisionCovered int
		for _, r := range regions {
			for f := r.start; f < r.end && f < len(transmitted); f++ {
				labeled++
				if transmitted[f] {
					covered++
				}
				if decisions[f].Voice() {
					decisionCovered++
				}
			}
		}
		recall := float64(covered) / float64(labeled)
		decRecall := float64(decisionCovered) / float64(labeled)
		t.Logf("%s: %d labeled frames, transmitted recall %.1f %%, decision recall %.1f %%",
			name, labeled, recall*100, decRecall*100)
		if recall < 0.95 {
			t.Errorf("%s: speech recall %.1f %% < 95 %%", name, recall*100)
		}

		// every labeled episode must be detected at all
		for ei, r := range regions {
			seen := false
			for f := r.start; f < r.end && f < len(decisions); f++ {
				if decisions[f] == Start {
					seen = true
					break
				}
			}
			if !seen {
				t.Errorf("%s: episode %d [%d,%d) never opened", name, ei, r.start, r.end)
			}
		}
	}

	for _, name := range noiseFiles {
		pcm := loadClip(t, name)
		decisions, _ := runVAD(pcm)
		if labels[name] != nil {
			t.Fatalf("%s: expected no labels for noise clip", name)
		}
		var flagged int
		for _, d := range decisions {
			if d.Voice() {
				flagged++
			}
		}
		fp := float64(flagged) / float64(len(decisions))
		t.Logf("%s: %d frames, false-positive %.1f %%", name, len(decisions), fp*100)
		if fp > 0.05 {
			t.Errorf("%s: false-positive rate %.1f %% > 5 %%", name, fp*100)
		}
	}
}
