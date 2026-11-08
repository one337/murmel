// Package levelmeter is the microphone level meter with the activation
// threshold handle, shown in the status bar's threshold popover and in
// the preferences. While Murmel is connected it shows the voice engine's
// levels (bus.InputLevel); otherwise it runs a microphone test on the
// selected input device.
package levelmeter

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/audio/levelmon"
	"io.github/murmel/internal/audio/pipeline"
	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/settings"
)

const (
	trackHeight = 10.0
	knobRadius  = 8.0
	areaHeight  = 2*knobRadius + 8
	// clipFraction marks the red zone: −3 dBFS and louder.
	clipFraction = 0.95
	// releasePerUpdate is how far the bar falls per reading (~30 ms):
	// fast attack, smooth release.
	releasePerUpdate = 0.025
	peakHold         = 1200 * time.Millisecond
	// busQuiet is how long without engine levels before the meter starts
	// its own microphone test.
	busQuiet = 600 * time.Millisecond
	dbStep   = 1.0 / 60 // one dB on the meter scale
)

// Meter is the level meter widget.
type Meter struct {
	root  *gtk.Box
	area  *gtk.DrawingArea
	value *gtk.Label
	state *gtk.Label

	st      settings.Settings
	b       *bus.Bus
	process func() func([]int16)

	level, peak float64 // meter fractions
	peakAt      time.Time
	clipAt      time.Time
	threshold   float64

	cancel   context.CancelFunc
	unwatch  func()
	poll     glib.SourceHandle
	lastBus  time.Time
	mon      *levelmon.Monitor
	starting bool // a microphone test is being opened
	monGen   int  // invalidates monitor starts that finish too late
}

// New builds a meter bound to the vad-threshold setting. process, when
// non-nil, returns the input processing to apply in microphone-test mode
// (so the test shows what voice detection would see).
func New(st settings.Settings, b *bus.Bus, process func() func([]int16)) *Meter {
	m := &Meter{st: st, b: b, process: process, threshold: st.VADThreshold()}

	m.area = gtk.NewDrawingArea()
	m.area.SetContentHeight(int(areaHeight))
	m.area.SetHExpand(true)
	m.area.SetFocusable(true)
	m.area.SetCursorFromName("pointer")
	m.area.SetTooltipText(tr("Drag the handle to set the activation threshold: speech louder than the handle is transmitted. Arrow keys move it by 1 dB."))
	m.area.SetDrawFunc(m.draw)

	drag := gtk.NewGestureDrag()
	drag.ConnectDragBegin(func(x, _ float64) {
		m.area.GrabFocus()
		m.setThreshold(m.fractionAt(x))
	})
	drag.ConnectDragUpdate(func(dx, _ float64) {
		if x, _, ok := drag.StartPoint(); ok {
			m.setThreshold(m.fractionAt(x + dx))
		}
	})
	m.area.AddController(drag)

	keys := gtk.NewEventControllerKey()
	keys.ConnectKeyPressed(func(keyval, _ uint, _ gdk.ModifierType) bool {
		switch keyval {
		case gdk.KEY_Left, gdk.KEY_Down:
			m.setThreshold(m.threshold - dbStep)
		case gdk.KEY_Right, gdk.KEY_Up:
			m.setThreshold(m.threshold + dbStep)
		case gdk.KEY_Page_Down:
			m.setThreshold(m.threshold - 5*dbStep)
		case gdk.KEY_Page_Up:
			m.setThreshold(m.threshold + 5*dbStep)
		case gdk.KEY_Home:
			m.setThreshold(0)
		case gdk.KEY_End:
			m.setThreshold(1)
		default:
			return false
		}
		return true
	})
	m.area.AddController(keys)
	focus := gtk.NewEventControllerFocus()
	focus.ConnectEnter(m.area.QueueDraw)
	focus.ConnectLeave(m.area.QueueDraw)
	m.area.AddController(focus)

	scale := gtk.NewCenterBox()
	scale.SetStartWidget(scaleLabel("−60 dB"))
	scale.SetCenterWidget(scaleLabel("−30 dB"))
	scale.SetEndWidget(scaleLabel("0 dB"))

	m.value = gtk.NewLabel("")
	m.value.SetXAlign(0)
	m.value.SetHExpand(true)
	m.value.AddCSSClass("numeric")
	m.state = gtk.NewLabel("")
	m.state.SetXAlign(1)
	m.state.AddCSSClass("dim-label")
	m.state.AddCSSClass("numeric")
	info := gtk.NewBox(gtk.OrientationHorizontal, 6)
	info.Append(m.value)
	info.Append(m.state)

	m.root = gtk.NewBox(gtk.OrientationVertical, 2)
	m.root.AddCSSClass("level-meter")
	m.root.Append(m.area)
	m.root.Append(scale)
	m.root.Append(info)
	m.root.ConnectMap(m.start)
	m.root.ConnectUnmap(m.stop)
	m.updateLabels()
	return m
}

// Root returns the widget to pack.
func (m *Meter) Root() *gtk.Box { return m.root }

func scaleLabel(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.AddCSSClass("caption")
	l.AddCSSClass("dim-label")
	l.AddCSSClass("numeric")
	return l
}

// --- level source ---------------------------------------------------------

func (m *Meter) start() {
	var ctx context.Context
	ctx, m.cancel = context.WithCancel(context.Background())
	m.lastBus = time.Time{}
	bus.SubscribeCtx(ctx, m.b, func(ev bus.InputLevel) {
		m.lastBus = time.Now()
		m.stopMonitor()
		m.feed(ev.LevelDB, ev.Clipping)
	})
	m.unwatch = m.st.ConnectChanged(func(key string) {
		if key == "vad-threshold" {
			if t := m.st.VADThreshold(); t != m.threshold {
				m.threshold = t
				m.updateLabels()
				m.area.QueueDraw()
			}
		}
	})
	m.poll = glib.TimeoutAdd(uint(busQuiet/time.Millisecond), func() bool {
		if m.mon == nil && !m.starting && time.Since(m.lastBus) > busQuiet {
			m.startMonitor()
		}
		return true
	})
}

func (m *Meter) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.unwatch != nil {
		m.unwatch()
		m.unwatch = nil
	}
	if m.poll != 0 {
		glib.SourceRemove(m.poll)
		m.poll = 0
	}
	m.monGen++
	m.stopMonitor()
}

// startMonitor opens the microphone test off the main loop (opening the
// device blocks briefly) and adopts it unless the meter was hidden or the
// engine's levels arrived meanwhile.
func (m *Meter) startMonitor() {
	m.monGen++
	gen := m.monGen
	dev := m.st.AudioInputDevice()
	var process func([]int16)
	if m.process != nil {
		process = m.process()
	}
	m.starting = true
	go func() {
		mon, err := levelmon.Start(dev, process, func(l levelmon.Level) {
			glib.IdleAdd(func() {
				if gen == m.monGen {
					m.feed(l.DB, l.Clipping)
				}
			})
		})
		glib.IdleAdd(func() {
			if gen == m.monGen {
				m.starting = false
			}
			if err != nil {
				if gen == m.monGen {
					m.state.SetText(tr("No microphone"))
				}
				return
			}
			if gen != m.monGen {
				go mon.Stop()
				return
			}
			m.mon = mon
		})
	}()
}

func (m *Meter) stopMonitor() {
	if m.starting {
		m.starting = false
		m.monGen++ // the start in flight discards its monitor
	}
	if m.mon == nil {
		return
	}
	mon := m.mon
	m.mon = nil
	m.monGen++
	go mon.Stop()
}

// --- state ----------------------------------------------------------------

func (m *Meter) feed(db float64, clipping bool) {
	f := pipeline.LevelFraction(db)
	m.level = math.Max(f, m.level-releasePerUpdate)
	now := time.Now()
	if f >= m.peak || now.Sub(m.peakAt) > peakHold {
		m.peak, m.peakAt = f, now
	}
	if clipping {
		m.clipAt = now
	}
	m.updateLabels()
	m.area.QueueDraw()
}

func (m *Meter) setThreshold(x float64) {
	x = math.Round(min(1, max(0, x))/dbStep) * dbStep // whole dB
	if x == m.threshold {
		return
	}
	m.threshold = x
	m.st.SetVADThreshold(x)
	m.updateLabels()
	m.area.QueueDraw()
}

func (m *Meter) fractionAt(x float64) float64 {
	left, width := m.track()
	return (x - left) / width
}

// track returns the horizontal extent of the meter track: inset by the
// knob radius so the handle stays fully visible at both ends.
func (m *Meter) track() (left, width float64) {
	w := float64(m.area.Width())
	return knobRadius, math.Max(1, w-2*knobRadius)
}

func (m *Meter) updateLabels() {
	m.value.SetText(fmt.Sprintf(tr("Threshold: %d dB"), int(math.Round(pipeline.ThresholdDB(m.threshold)))))
	switch {
	case time.Since(m.clipAt) < peakHold:
		m.state.SetText(tr("Too loud"))
	case m.level >= m.threshold && m.level > 0:
		m.state.SetText(tr("Speech detected"))
	case m.level > 0:
		m.state.SetText(tr("Below threshold"))
	default:
		m.state.SetText("")
	}
}

// --- drawing --------------------------------------------------------------

func (m *Meter) draw(area *gtk.DrawingArea, cr *cairo.Context, width, height int) {
	left, tw := m.track()
	mid := float64(height) / 2
	top := mid - trackHeight/2

	fg := area.Color()
	r, g, b := 0.5, 0.5, 0.5
	if fg != nil {
		r, g, b = float64(fg.Red()), float64(fg.Green()), float64(fg.Blue())
	}
	dark := r+g+b > 1.5 // light text → dark theme
	setFG := func(a float64) { cr.SetSourceRGBA(r, g, b, a) }

	// Track and scale ticks every 10 dB.
	setFG(0.10)
	roundedRect(cr, left, top, tw, trackHeight, trackHeight/2)
	cr.Fill()
	setFG(0.25)
	for i := 0; i <= 6; i++ {
		x := math.Round(left+tw*float64(i)/6) + 0.5
		cr.MoveTo(x, top+trackHeight+2)
		cr.LineTo(x, top+trackHeight+5)
	}
	cr.SetLineWidth(1)
	cr.Stroke()

	// Level: grey below the threshold, green above, red in the clip zone.
	clipX := left + tw*clipFraction
	thrX := left + tw*m.threshold
	lvX := left + tw*m.level
	cr.Save()
	roundedRect(cr, left, top, tw, trackHeight, trackHeight/2)
	cr.Clip()
	if lvX > left {
		setFG(0.40)
		cr.Rectangle(left, top, math.Min(lvX, thrX)-left, trackHeight)
		cr.Fill()
		if lvX > thrX {
			cr.SetSourceRGBA(0.180, 0.761, 0.494, 1) // green #2ec27e
			cr.Rectangle(thrX, top, math.Min(lvX, clipX)-thrX, trackHeight)
			cr.Fill()
		}
		if lvX > clipX {
			cr.SetSourceRGBA(0.878, 0.106, 0.141, 1) // red #e01b24
			cr.Rectangle(clipX, top, lvX-clipX, trackHeight)
			cr.Fill()
		}
	}
	// Peak hold.
	if m.peak > 0 {
		px := math.Round(left + tw*m.peak)
		setFG(0.6)
		cr.Rectangle(px-1, top, 2, trackHeight)
		cr.Fill()
	}
	cr.Restore()

	// Threshold handle: a line across the track and a round knob.
	ar, ag, ab := 0.208, 0.518, 0.894 // accent #3584e4
	if dark {
		ar, ag, ab = 0.471, 0.682, 0.929 // #78aeed
	}
	cr.SetSourceRGBA(ar, ag, ab, 1)
	cr.Rectangle(math.Round(thrX)-1, top-3, 2, trackHeight+6)
	cr.Fill()
	cr.Arc(thrX, mid, knobRadius-1, 0, 2*math.Pi)
	cr.SetSourceRGBA(ar, ag, ab, 1)
	cr.FillPreserve()
	if dark {
		cr.SetSourceRGBA(0.12, 0.12, 0.14, 1)
	} else {
		cr.SetSourceRGBA(1, 1, 1, 1)
	}
	cr.SetLineWidth(2)
	cr.Stroke()
	if area.HasFocus() {
		cr.Arc(thrX, mid, knobRadius+2, 0, 2*math.Pi)
		cr.SetSourceRGBA(ar, ag, ab, 0.5)
		cr.SetLineWidth(2)
		cr.Stroke()
	}
}

func roundedRect(cr *cairo.Context, x, y, w, h, r float64) {
	r = math.Min(r, math.Min(w, h)/2)
	cr.NewPath()
	cr.Arc(x+w-r, y+r, r, -math.Pi/2, 0)
	cr.Arc(x+w-r, y+h-r, r, 0, math.Pi/2)
	cr.Arc(x+r, y+h-r, r, math.Pi/2, math.Pi)
	cr.Arc(x+r, y+r, r, math.Pi, 3*math.Pi/2)
	cr.ClosePath()
}
