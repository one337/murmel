package ui

import (
	"strings"
	"testing"
	"time"

	"io.github/murmel/internal/audio/effects"
	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui/presence"
)

func newTestSounds(t *testing.T) (*eventSounds, *presence.Tracker, *[]string) {
	t.Helper()
	st := settings.NewMemory()
	b := bus.New(nil)
	t.Cleanup(b.Close)
	played := []string{}
	s := &eventSounds{settings: st, bus: b, play: func(c string) { played = append(played, c) }}
	pt := presence.NewTracker()
	pt.Listen(s.onPresence)
	ev := bus.Connected{
		SelfSession: 1,
		Users: []bus.UserInfo{
			{Session: 1, Name: "me", Channel: 5},
			{Session: 7, Name: "peer", Channel: 0},
		},
	}
	b.Emit(ev)
	b.Close()
	pt.OnConnected(ev)
	return s, pt, &played
}

func TestSoundsDefaults(t *testing.T) {
	s, pt, played := newTestSounds(t)
	pt.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 1, Channel: 0}, Changed: bus.UserChangeChannel}) // own move right after connect: quiet
	s.connectedAt = time.Now().Add(-time.Minute)
	pt.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 1, Channel: 5}, Changed: bus.UserChangeChannel})
	pt.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 7, Channel: 5}, Changed: bus.UserChangeChannel})
	pt.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 7, Channel: 0}, Changed: bus.UserChangeChannel})
	pt.OnUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 9, Channel: 0}}) // server join: off by default
	s.onTextMessage(bus.TextMessage{Sender: 7, UserSessions: []uint32{1}})
	s.onTextMessage(bus.TextMessage{Sender: 7, ChannelIDs: []uint32{5}})   // channel chat: off by default
	s.onTextMessage(bus.TextMessage{Sender: 1, UserSessions: []uint32{7}}) // own echo
	pt.OnDisconnected(bus.Disconnected{})
	want := []string{"builtin:connect", "builtin:move", "builtin:join", "builtin:leave", "builtin:message", "builtin:disconnect"}
	if strings.Join(*played, " ") != strings.Join(want, " ") {
		t.Fatalf("played %v, want %v", *played, want)
	}
}

func TestSoundsOverridesAndMaster(t *testing.T) {
	s, pt, played := newTestSounds(t)
	*played = nil
	s.settings.SetSoundEvents(map[string]string{
		string(effects.UserConnected):     effects.BuiltinChoice("blip-up"),
		string(effects.UserJoinedChannel): effects.Off,
	})
	pt.OnUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 9, Channel: 5}}) // into my channel, channel sound off → server sound
	pt.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 7, Channel: 5}, Changed: bus.UserChangeChannel})
	s.settings.SetSoundsEnabled(false)
	pt.OnUserRemoved(bus.UserRemoved{Session: 7})
	if strings.Join(*played, " ") != "builtin:blip-up" {
		t.Fatalf("played %v", *played)
	}
}
