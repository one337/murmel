package ui

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"io.github/murmel/internal/audio/device"
	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui/levelmeter"
)

const (
	iconMicOn      = "audio-input-microphone-symbolic"
	iconMicOff     = "microphone-disabled-symbolic"
	iconHeadphones = "audio-headphones-symbolic"
	iconDeafened   = "audio-volume-muted-symbolic"
	iconDropdown   = "pan-down-symbolic"

	popoverWidthPx        = 300
	deviceListMaxHeightPx = 260
	deviceNameMaxChars    = 34
)

// statusBar is the voice strip pinned below the main content: self-mute
// and self-deafen toggles, each with a dropdown for device selection, the
// voice input meter (a button opening the activation threshold), and
// (right-aligned) the connection status dot — green when
// connected, red otherwise — whose tooltip shows the connection details.
// Audio widgets are fed by bus.InputLevel; the status dot by bus events
// routed through Window.
type statusBar struct {
	root       *gtk.Box
	selfMute   *gtk.ToggleButton
	selfDeafen *gtk.ToggleButton
	meter      *voiceMeter
	dot        *gtk.Box
	dotHolder  *gtk.Box

	settings settings.Settings
	bus      *bus.Bus
}

func newStatusBar(st settings.Settings, b *bus.Bus) *statusBar {
	s := &statusBar{settings: st, bus: b}

	s.selfMute = gtk.NewToggleButton()
	s.selfMute.SetIconName(iconMicOn)
	s.selfMute.SetTooltipText(tr("Mute microphone"))
	s.selfMute.ConnectToggled(func() {
		s.updateSelfMuteIcon()
	})

	s.selfDeafen = gtk.NewToggleButton()
	s.selfDeafen.SetIconName(iconHeadphones)
	s.selfDeafen.SetTooltipText(tr("Deafen"))
	s.selfDeafen.ConnectToggled(func() {
		s.updateSelfDeafenIcon()
	})

	micMenu := s.newDropdown(tr("Input device"), s.fillInputPopover)
	outMenu := s.newDropdown(tr("Output device"), s.fillOutputPopover)

	s.meter = newVoiceMeter()
	meterBtn := gtk.NewMenuButton()
	meterBtn.SetChild(s.meter.root)
	meterBtn.SetPopover(newRefreshingPopover(s.fillThresholdPopover))
	meterBtn.SetDirection(gtk.ArrowUp)
	meterBtn.SetVAlign(gtk.AlignFill)
	meterBtn.AddCSSClass("voice-meter-button")
	meterBtn.SetTooltipText(tr("Voice input: grey = below threshold, green = transmitting, red = too loud. Click to set the activation threshold."))

	// The dot sits in a padded holder so the tooltip has a comfortable
	// hover target.
	s.dot = gtk.NewBox(gtk.OrientationHorizontal, 0)
	s.dot.AddCSSClass("status-dot")
	s.dot.SetVAlign(gtk.AlignCenter)
	s.dot.SetHAlign(gtk.AlignCenter)
	s.dotHolder = gtk.NewBox(gtk.OrientationHorizontal, 0)
	s.dotHolder.AddCSSClass("status-dot-holder")
	s.dotHolder.Append(s.dot)

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)

	s.root = gtk.NewBox(gtk.OrientationHorizontal, 6)
	s.root.SetMarginTop(4)
	// Bottom matches the start margin so the buttons sit evenly in the
	// window corner.
	s.root.SetMarginBottom(6)
	s.root.SetMarginStart(6)
	s.root.SetMarginEnd(6)
	s.root.Append(linked(s.selfMute, micMenu))
	s.root.Append(linked(s.selfDeafen, outMenu))
	s.root.Append(meterBtn)
	s.root.Append(spacer)
	s.root.Append(s.dotHolder)
	s.setDisconnected()
	return s
}

// linked groups a toggle and its dropdown arrow into one split button.
func linked(toggle *gtk.ToggleButton, menu *gtk.MenuButton) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.AddCSSClass("linked")
	box.Append(toggle)
	box.Append(menu)
	return box
}

// newDropdown builds the narrow arrow button of a split button. The
// popover content is rebuilt by fill every time it opens so the device
// list reflects devices plugged in since.
func (s *statusBar) newDropdown(tooltip string, fill func(*gtk.Box)) *gtk.MenuButton {
	btn := gtk.NewMenuButton()
	btn.SetIconName(iconDropdown)
	btn.SetTooltipText(tooltip)
	btn.SetPopover(newRefreshingPopover(fill))
	btn.SetDirection(gtk.ArrowUp)
	btn.AddCSSClass("dropdown-arrow")
	return btn
}

// newRefreshingPopover returns a popover whose content fill rebuilds every
// time it opens.
func newRefreshingPopover(fill func(*gtk.Box)) *gtk.Popover {
	content := gtk.NewBox(gtk.OrientationVertical, 6)
	content.SetSizeRequest(popoverWidthPx, -1)
	content.SetMarginTop(6)
	content.SetMarginBottom(6)
	content.SetMarginStart(6)
	content.SetMarginEnd(6)

	pop := gtk.NewPopover()
	pop.SetChild(content)
	pop.ConnectShow(func() {
		for c := content.FirstChild(); c != nil; c = content.FirstChild() {
			content.Remove(c)
		}
		fill(content)
	})
	return pop
}

func (s *statusBar) fillInputPopover(box *gtk.Box) {
	var infos []device.Info
	if list, err := device.GetDevices(); err == nil {
		for _, d := range list.Capture {
			// PulseAudio/PipeWire "Monitor of …" sources record an
			// output, not a microphone.
			if !strings.HasPrefix(d.Name, "Monitor of ") {
				infos = append(infos, d)
			}
		}
	} else {
		slog.Info("status bar: listing capture devices failed", "err", err)
	}
	box.Append(heading(tr("Input Device")))
	box.Append(deviceChoices(infos, s.settings.AudioInputDevice(), s.settings.SetAudioInputDevice))
}

func (s *statusBar) fillThresholdPopover(box *gtk.Box) {
	box.Append(heading(tr("Activation Threshold")))
	box.Append(levelmeter.New(s.settings, s.bus, nil).Root())
	hint := gtk.NewLabel(tr("Speak normally and drag the handle just below your speech level: everything louder than the handle is transmitted. Red means the input is too loud."))
	hint.SetWrap(true)
	hint.SetMaxWidthChars(36)
	hint.SetXAlign(0)
	hint.AddCSSClass("caption")
	hint.AddCSSClass("dim-label")
	box.Append(hint)
}

func (s *statusBar) fillOutputPopover(box *gtk.Box) {
	var infos []device.Info
	if list, err := device.GetDevices(); err == nil {
		infos = list.Playback
	} else {
		slog.Info("status bar: listing playback devices failed", "err", err)
	}
	box.Append(heading(tr("Output Device")))
	box.Append(deviceChoices(infos, s.settings.AudioOutputDevice(), s.settings.SetAudioOutputDevice))
}

func heading(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.AddCSSClass("heading")
	l.SetXAlign(0)
	return l
}

// deviceChoices renders "System default" plus every device as a radio
// group; picking one stores its name (or "" for the default). Long lists
// scroll, long names are ellipsized (full name in the tooltip).
func deviceChoices(infos []device.Info, current string, store func(string)) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 2)
	found := current == ""

	add := func(label, value string, group *gtk.CheckButton) *gtk.CheckButton {
		text := gtk.NewLabel(label)
		text.SetEllipsize(pango.EllipsizeEnd)
		text.SetMaxWidthChars(deviceNameMaxChars)
		text.SetXAlign(0)
		cb := gtk.NewCheckButton()
		cb.SetChild(text)
		cb.SetTooltipText(label)
		if group != nil {
			cb.SetGroup(group)
		}
		if value == current {
			cb.SetActive(true)
			found = true
		}
		cb.ConnectToggled(func() {
			if cb.Active() {
				store(value)
			}
		})
		box.Append(cb)
		return cb
	}

	first := add(tr("System default"), "", nil)
	for _, d := range infos {
		add(d.Name, d.Name, first)
	}
	if !found {
		// The stored device is gone (unplugged): show it so the user
		// sees why the default is in use, and can switch away.
		add(fmt.Sprintf(tr("%s (not available)"), current), current, first)
	}

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(box)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetPropagateNaturalHeight(true)
	scroll.SetMaxContentHeight(deviceListMaxHeightPx)
	return scroll
}

func (s *statusBar) updateSelfMuteIcon() {
	if s.selfMute.Active() {
		s.selfMute.SetIconName(iconMicOff)
	} else {
		s.selfMute.SetIconName(iconMicOn)
	}
}

func (s *statusBar) updateSelfDeafenIcon() {
	if s.selfDeafen.Active() {
		s.selfDeafen.SetIconName(iconDeafened)
	} else {
		s.selfDeafen.SetIconName(iconHeadphones)
	}
}

// SetSelfMuted and SelfMuted reflect the effective self-mute state.
func (s *statusBar) SetSelfMuted(muted bool) { s.selfMute.SetActive(muted) }
func (s *statusBar) SelfMuted() bool         { return s.selfMute.Active() }

// SetSelfDeafened and SelfDeafened mirror SetSelfMuted.
func (s *statusBar) SetSelfDeafened(deafened bool) { s.selfDeafen.SetActive(deafened) }
func (s *statusBar) SelfDeafened() bool            { return s.selfDeafen.Active() }

// pushLevel feeds the voice meter.
func (s *statusBar) pushLevel(ev bus.InputLevel) {
	s.meter.push(ev, s.SelfMuted() || s.SelfDeafened())
}

func (s *statusBar) setDot(connected bool, tooltip string) {
	if connected {
		s.dot.RemoveCSSClass("disconnected")
		s.dot.AddCSSClass("connected")
	} else {
		s.dot.RemoveCSSClass("connected")
		s.dot.AddCSSClass("disconnected")
	}
	s.dotHolder.SetTooltipText(tooltip)
}

func (s *statusBar) setConnected() {
	s.setDot(true, tr("Connected"))
}

// setInfo refreshes the connection details shown in the dot's tooltip.
func (s *statusBar) setInfo(ev bus.ConnectionInfo) {
	lines := []string{fmt.Sprintf(tr("Connected to %s"), ev.Address)}
	if ev.Username != "" {
		lines = append(lines, fmt.Sprintf(tr("User: %s"), ev.Username))
	}
	if ev.Channel != "" {
		lines = append(lines, fmt.Sprintf(tr("Channel: %s"), ev.Channel))
	}
	if ev.ServerVersion != "" {
		lines = append(lines, fmt.Sprintf(tr("Server: Mumble %s"), ev.ServerVersion))
	}
	transport := tr("UDP")
	if ev.Transport != "udp" {
		transport = tr("TCP (UDP unavailable)")
	}
	lines = append(lines, fmt.Sprintf(tr("Voice: %s"), transport))
	if ev.Ping > 0 {
		lines = append(lines, fmt.Sprintf(tr("Ping: %d ms"), ev.Ping.Milliseconds()))
	}
	if ev.MaxBandwidth > 0 {
		lines = append(lines, fmt.Sprintf(tr("Bandwidth limit: %d kbit/s"), ev.MaxBandwidth/1000))
	}
	s.setDot(true, strings.Join(lines, "\n"))
}

func (s *statusBar) setDisconnected() {
	s.setDot(false, tr("Not connected"))
	s.meter.reset()
}

func (s *statusBar) setError(text string) {
	s.meter.reset()
	if text == "" {
		s.setDot(false, tr("Connection failed"))
		return
	}
	s.setDot(false, fmt.Sprintf(tr("Connection failed: %s"), text))
}
