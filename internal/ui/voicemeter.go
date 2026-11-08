package ui

import (
	"math"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

const (
	meterBarPx   = 3 // bar width; whole pixels keep the edges crisp
	meterGapPx   = 2
	meterBars    = 21 // history length: 21 × 30 ms ≈ 0.6 s
	meterWidthPx = meterBars*(meterBarPx+meterGapPx) - meterGapPx
	meterFloorDB = -60.0
)

// meterState is the colour class of one bar.
type meterState uint8

const (
	meterQuiet    meterState = iota // below threshold / not transmitting: grey
	meterTalking                    // above threshold, voice is transmitted: green
	meterClipping                   // input overdriven: red
)

type meterSample struct {
	level float64 // 0..1
	state meterState
}

// voiceMeter shows the recent microphone input as a small mirrored
// waveform of rounded bars inside a pill (the child of the status bar's
// threshold button): grey while below
// the activation threshold (or muted), green while the voice is
// transmitted, red when the input clips. A faint band marks the
// threshold; the pill's outline lights up green/red with the live state.
type voiceMeter struct {
	root      *gtk.Box
	area      *gtk.DrawingArea
	samples   [meterBars]meterSample
	head      int // index of the oldest sample
	threshold float64
}

func newVoiceMeter() *voiceMeter {
	m := &voiceMeter{threshold: -1}
	m.area = gtk.NewDrawingArea()
	m.area.SetContentWidth(meterWidthPx)
	m.area.SetVExpand(true)
	m.area.SetDrawFunc(m.draw)

	// The pill stretches to the height of the neighbouring buttons
	// (AlignFill in the status bar row); padding comes from style.css.
	m.root = gtk.NewBox(gtk.OrientationHorizontal, 0)
	m.root.AddCSSClass("voice-meter")
	m.root.SetVAlign(gtk.AlignFill)
	m.root.Append(m.area)
	return m
}

// push appends one InputLevel window. muted forces grey (nothing is
// transmitted while self-muted, whatever the VAD decides).
func (m *voiceMeter) push(ev bus.InputLevel, muted bool) {
	st := meterQuiet
	switch {
	case ev.Clipping:
		st = meterClipping
	case ev.Transmitting && !muted:
		st = meterTalking
	}
	m.samples[m.head] = meterSample{level: normDB(ev.LevelDB), state: st}
	m.head = (m.head + 1) % meterBars
	m.threshold = normDB(ev.ThresholdDB)
	m.setState(st)
	m.area.QueueDraw()
}

// reset clears the history (disconnect: no capture running).
func (m *voiceMeter) reset() {
	m.samples = [meterBars]meterSample{}
	m.head = 0
	m.threshold = -1
	m.setState(meterQuiet)
	m.area.QueueDraw()
}

func (m *voiceMeter) setState(st meterState) {
	toggleClass(m.root, "transmitting", st == meterTalking)
	toggleClass(m.root, "clipping", st == meterClipping)
}

func toggleClass(w *gtk.Box, class string, on bool) {
	if on {
		w.AddCSSClass(class)
	} else {
		w.RemoveCSSClass(class)
	}
}

// normDB maps dBFS onto 0..1 over [meterFloorDB, 0].
func normDB(db float64) float64 {
	v := (db - meterFloorDB) / -meterFloorDB
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	}
	return v
}

func (m *voiceMeter) draw(area *gtk.DrawingArea, cr *cairo.Context, width, height int) {
	// Pixel-snapped layout: bars of whole-pixel width at whole-pixel
	// positions, centred in the area, so antialiasing only softens the
	// rounded ends.
	total := meterBars*(meterBarPx+meterGapPx) - meterGapPx
	x0 := float64((width - total) / 2)
	mid := float64(height) / 2
	const r = meterBarPx / 2.0
	maxHalf := mid - 1

	fg := area.Color()
	setFG := func(alpha float64) {
		if fg != nil {
			cr.SetSourceRGBA(float64(fg.Red()), float64(fg.Green()), float64(fg.Blue()), alpha)
		} else {
			cr.SetSourceRGBA(0.5, 0.5, 0.5, alpha)
		}
	}

	// Activation threshold: a faint band behind the bars — everything
	// that stays inside it is not transmitted.
	if m.threshold > 0 {
		off := math.Max(r, m.threshold*maxHalf)
		setFG(0.08)
		roundedRect(cr, x0-2, mid-off, float64(total)+4, 2*off, math.Min(off, 3))
		cr.Fill()
	}

	for i := 0; i < meterBars; i++ {
		s := m.samples[(m.head+i)%meterBars]
		switch s.state {
		case meterClipping:
			cr.SetSourceRGBA(0.878, 0.106, 0.141, 1) // Adwaita red_3 #e01b24
		case meterTalking:
			cr.SetSourceRGBA(0.180, 0.761, 0.494, 1) // Adwaita green_4 #2ec27e
		default:
			setFG(0.35)
		}
		// Minimum height is one bar width: silence reads as a row of dots.
		half := math.Max(r, math.Round(s.level*maxHalf))
		x := x0 + float64(i*(meterBarPx+meterGapPx))
		roundedRect(cr, x, mid-half, meterBarPx, 2*half, r)
		cr.Fill()
	}
}

// roundedRect adds a rectangle with corner radius r to the current path.
func roundedRect(cr *cairo.Context, x, y, w, h, r float64) {
	r = math.Min(r, math.Min(w, h)/2)
	cr.NewPath()
	cr.Arc(x+w-r, y+r, r, -math.Pi/2, 0)
	cr.Arc(x+w-r, y+h-r, r, 0, math.Pi/2)
	cr.Arc(x+r, y+h-r, r, math.Pi/2, math.Pi)
	cr.Arc(x+r, y+r, r, math.Pi, 3*math.Pi/2)
	cr.ClosePath()
}
