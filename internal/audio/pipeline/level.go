package pipeline

// Level meter scale shared by the VAD threshold, the voice meters and the
// threshold setting: RMS level in dBFS from LevelFloorDB (silence) to 0.
const LevelFloorDB = -60.0

// ThresholdDB maps the threshold setting x in [0,1] (the position on the
// level meter) to dBFS: 0 → LevelFloorDB, 1 → 0 dBFS.
func ThresholdDB(x float64) float64 {
	x = min(1, max(0, x))
	return LevelFloorDB * (1 - x)
}

// LevelFraction maps a dBFS level onto the meter scale [0,1].
func LevelFraction(db float64) float64 {
	return min(1, max(0, 1-db/LevelFloorDB))
}

// ClipSample is the absolute sample value treated as clipping (≈ −0.2 dBFS).
const ClipSample = 32400

// LevelDB returns the RMS level of frame in dBFS (−120 for silence).
func LevelDB(frame []int16) float64 { return dbfs(RMS(frame)) }

// Clipped reports whether any sample of frame reaches ClipSample.
func Clipped(frame []int16) bool {
	for _, s := range frame {
		if s >= ClipSample || s <= -ClipSample {
			return true
		}
	}
	return false
}
