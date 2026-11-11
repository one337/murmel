package ui

import (
	"math"
	"time"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

const (
	meterWidthPx = 104 // minimum; the meter grows with its button
	meterFloorDB = -60.0
	// meterSmooth is how many 30 ms readings the wave height averages
	// (≈ 0.12 s): steady enough to read, quick enough to feel live.
	meterSmooth = 4
)

// meterState is the colour of the wave.
type meterState uint8

const (
	meterQuiet    meterState = iota // below threshold: faint grey
	meterDetected                   // voice detected but not sent (muted): yellow
	meterTalking                    // voice is transmitted: green
	meterClipping                   // input overdriven: red
)

// meterWave is one of the overlaid sine waves.
type meterWave struct {
	cycles float64 // sine periods across the width
	speed  float64 // phase speed in rad/s (sign = direction)
	amp    float64 // share of the current amplitude
	alpha  float64
	width  float64 // line width in px
}

// Three thin waves drifting at different speeds; their sum reads as a
// single living line that swells with the voice.
var meterWaves = [...]meterWave{
	{cycles: 2.0, speed: 4.0, amp: 1.0, alpha: 1.0, width: 1.5},
	{cycles: 3.1, speed: -3.0, amp: 0.65, alpha: 0.5, width: 1.2},
	{cycles: 1.4, speed: 2.2, amp: 0.45, alpha: 0.3, width: 1.0},
}

// voiceMeter shows the microphone input as thin sine waves inside a pill
// (the child of the status bar's threshold button). Their height follows
// the input level; the colour tells what happens to the voice: faint grey
// below the activation threshold, green while it is transmitted, yellow
// while it is detected but not sent (muted), red when the input clips.
// The pill's outline takes the same colour.
type voiceMeter struct {
	root   *gtk.Box
	area   *gtk.DrawingArea
	levels [meterSmooth]float64 // recent levels 0..1, ring
	head   int
	state  meterState
}

func newVoiceMeter() *voiceMeter {
	m := &voiceMeter{}
	m.area = gtk.NewDrawingArea()
	m.area.SetContentWidth(meterWidthPx)
	m.area.SetHExpand(true)
	m.area.SetVExpand(true)
	m.area.SetDrawFunc(m.draw)

	// The pill stretches to the height of the neighbouring buttons
	// (AlignFill in the status bar row); padding comes from style.css.
	m.root = gtk.NewBox(gtk.OrientationHorizontal, 0)
	m.root.AddCSSClass("voice-meter")
	m.root.SetVAlign(gtk.AlignFill)
	m.root.SetHExpand(true)
	m.root.Append(m.area)
	return m
}

// push takes one InputLevel window. muted: the voice is not sent whatever
// the voice detection decides, so detected speech shows yellow.
func (m *voiceMeter) push(ev bus.InputLevel, muted bool) {
	m.levels[m.head] = normDB(ev.LevelDB)
	m.head = (m.head + 1) % meterSmooth
	m.setState(levelState(ev, muted))
	m.area.QueueDraw() // also advances the waves' phase
}

func levelState(ev bus.InputLevel, muted bool) meterState {
	switch {
	case ev.Clipping:
		return meterClipping
	case ev.Transmitting && muted:
		return meterDetected
	case ev.Transmitting:
		return meterTalking
	case ev.Detected:
		// Heard but not sent: push-to-talk not held.
		return meterDetected
	}
	return meterQuiet
}

// reset flattens the wave (disconnect: no capture running).
func (m *voiceMeter) reset() {
	m.levels = [meterSmooth]float64{}
	m.setState(meterQuiet)
	m.area.QueueDraw()
}

func (m *voiceMeter) setState(st meterState) {
	m.state = st
	toggleClass(m.root, "detected", st == meterDetected)
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
	w, h := float64(width), float64(height)
	mid := h / 2

	level := 0.0
	for _, l := range m.levels {
		level += l / meterSmooth
	}
	// A hint of movement even in silence, so the line reads as live.
	amp := math.Max(0.6, level*(mid-1.5))

	setColor := func(alpha float64) {
		switch m.state {
		case meterClipping:
			cr.SetSourceRGBA(0.878, 0.106, 0.141, alpha) // Adwaita red_3 #e01b24
		case meterTalking:
			cr.SetSourceRGBA(0.180, 0.761, 0.494, alpha) // Adwaita green_4 #2ec27e
		case meterDetected:
			cr.SetSourceRGBA(0.898, 0.647, 0.039, alpha) // Adwaita yellow_5 #e5a50a
		default:
			// Quiet: the foreground colour, faint enough not to stand out.
			alpha *= 0.35
			if fg := area.Color(); fg != nil {
				cr.SetSourceRGBA(float64(fg.Red()), float64(fg.Green()), float64(fg.Blue()), alpha)
			} else {
				cr.SetSourceRGBA(0.5, 0.5, 0.5, alpha)
			}
		}
	}

	t := float64(time.Now().UnixMilli()) / 1000
	cr.SetLineCap(cairo.LineCapRound)
	cr.SetLineJoin(cairo.LineJoinRound)
	for _, wv := range meterWaves {
		cr.NewPath()
		cr.SetLineWidth(wv.width)
		for x := 0.0; x <= w; x++ {
			u := x / w
			taper := math.Pow(math.Sin(math.Pi*u), 2) // calm at both ends
			y := mid + amp*wv.amp*taper*math.Sin(2*math.Pi*wv.cycles*u+wv.speed*t)
			if x == 0 {
				cr.MoveTo(x, y)
			} else {
				cr.LineTo(x, y)
			}
		}
		setColor(wv.alpha)
		cr.Stroke()
	}
}
