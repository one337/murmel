package ui

import (
	"time"

	"io.github/murmel/internal/audio/effects"
	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui/presence"
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
	connected   bool
	pttHeld     bool
	// play is the output seam: production plays through the effects
	// player on the configured output device.
	play func(choice string)
}

func newEventSounds(app *App) *eventSounds {
	s := &eventSounds{settings: app.settings, bus: app.bus}
	s.play = func(choice string) {
		app.soundPlayer.PlayChoice(choice, app.settings.AudioOutputDevice())
	}
	app.presence.Listen(s.onPresence)
	bus.SubscribeCtx(app.ctx, app.bus, s.onTextMessage)
	bus.SubscribeCtx(app.ctx, app.bus, s.onPushToTalk)
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

func (s *eventSounds) onPresence(ev presence.Event) {
	switch ev.Kind {
	case presence.SelfConnected:
		s.connectedAt = time.Now()
		s.connected = true
		s.trigger(effects.SelfConnected)
	case presence.SelfDisconnected:
		s.connected = false
		s.trigger(effects.SelfDisconnected)
	case presence.SelfMoved:
		if time.Since(s.connectedAt) > connectMoveQuiet {
			s.trigger(effects.SelfMoved)
		}
	case presence.UserJoinedChannel:
		s.trigger(effects.UserJoinedChannel)
	case presence.UserLeftChannel:
		s.trigger(effects.UserLeftChannel)
	case presence.UserConnected:
		// Someone connecting straight into our channel sounds like a
		// channel join (if that sound is on); otherwise like a server join.
		if ev.InMyChannel && s.choice(effects.UserJoinedChannel) != effects.Off {
			s.trigger(effects.UserJoinedChannel)
		} else {
			s.trigger(effects.UserConnected)
		}
	case presence.UserDisconnected:
		if ev.InMyChannel && s.choice(effects.UserLeftChannel) != effects.Off {
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

// onPushToTalk clicks when push-to-talk starts and stops sending (only in
// push-to-talk mode and while connected; key repeat is ignored).
func (s *eventSounds) onPushToTalk(ev bus.PushToTalk) {
	if ev.Pressed == s.pttHeld {
		return
	}
	s.pttHeld = ev.Pressed
	if !s.connected || s.settings.TransmissionMode() != "push-to-talk" {
		return
	}
	if ev.Pressed {
		s.trigger(effects.PushToTalkPressed)
	} else {
		s.trigger(effects.PushToTalkRelease)
	}
}
