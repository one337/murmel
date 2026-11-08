package chat

// anchorEpsilon is the slack (in pixels) within which the scroll position
// still counts as "at the bottom": full-page heights jump discretely, and
// sub-pixel rounding keeps the exact bottom unreachable on HiDPI.
const anchorEpsilon = 24.0

// anchored reports whether a vertical adjustment is pinned to the bottom,
// i.e. new rows should keep the view scrolled down.
func anchored(value, upper, pageSize float64) bool {
	return upper-value-pageSize < anchorEpsilon
}
