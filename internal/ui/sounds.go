package ui

import (
	"time"

	"io.github/murmel/internal/audio/effects"
	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/settings"
)

// connectMoveQuiet suppresses the "you moved" sound right after
// connecting: servers often move a returning user to their last channel,
// and that move was not the user's action.
const connectMoveQuiet = 1500 * time.Millisecond

// eventSounds plays the configured sound for presence and message events
// (gated by the sounds-enabled key; per-event choices in sound-events).
type eventSounds struct {
	settings    settings.Settings
	bus         *bus.Bus
	connectedAt time.Time
	// play is the output seam: production plays through the effects
	// player on the configured output device.
	play func(choice string)
}

func newEventSounds(app *App) *eventSounds {
	s := &eventSounds{settings: app.settings, bus: app.bus}
	s.play = func(choice string) {
		app.soundPlayer.PlayChoice(choice, app.settings.AudioOutputDevice())
	}
	app.presence.listen(s.onPresence)
	bus.SubscribeCtx(app.ctx, app.bus, s.onTextMessage)
	return s
}

func (s *eventSounds) trigger(ev effects.Event) {
	if !s.settings.SoundsEnabled() {
		return
	}
	if choice := s.choice(ev); choice != effects.Off {
		s.play(choice)
	}
}

func (s *eventSounds) choice(ev effects.Event) string {
	return effects.Choice(s.settings.SoundEvents(), ev)
}

func (s *eventSounds) onPresence(ev presenceEvent) {
	switch ev.kind {
	case presenceSelfConnected:
		s.connectedAt = time.Now()
		s.trigger(effects.SelfConnected)
	case presenceSelfDisconnected:
		s.trigger(effects.SelfDisconnected)
	case presenceSelfMoved:
		if time.Since(s.connectedAt) > connectMoveQuiet {
			s.trigger(effects.SelfMoved)
		}
	case presenceUserJoinedChannel:
		s.trigger(effects.UserJoinedChannel)
	case presenceUserLeftChannel:
		s.trigger(effects.UserLeftChannel)
	case presenceUserConnected:
		// Someone connecting straight into our channel sounds like a
		// channel join (if that sound is on); otherwise like a server join.
		if ev.inMyChannel && s.choice(effects.UserJoinedChannel) != effects.Off {
			s.trigger(effects.UserJoinedChannel)
		} else {
			s.trigger(effects.UserConnected)
		}
	case presenceUserDisconnected:
		if ev.inMyChannel && s.choice(effects.UserLeftChannel) != effects.Off {
			s.trigger(effects.UserLeftChannel)
		} else {
			s.trigger(effects.UserDisconnected)
		}
	}
}

func (s *eventSounds) onTextMessage(ev bus.TextMessage) {
	self := s.bus.State().SelfSession
	if ev.Sender == self || ev.Sender == 0 {
		return // own echo or server message
	}
	if isPrivateMessage(ev, self) {
		s.trigger(effects.PrivateMessage)
	} else {
		s.trigger(effects.ChannelMessage)
	}
}
