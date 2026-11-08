package ui

import (
	"strings"
	"testing"
	"time"

	"io.github/murmel/internal/audio/effects"
	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/settings"
)

func newTestSounds(t *testing.T) (*eventSounds, *presenceTracker, *[]string) {
	t.Helper()
	st := settings.NewMemory()
	b := bus.New(nil)
	t.Cleanup(b.Close)
	played := []string{}
	s := &eventSounds{settings: st, bus: b, play: func(c string) { played = append(played, c) }}
	pt := newPresenceTracker()
	pt.listen(s.onPresence)
	ev := bus.Connected{
		SelfSession: 1,
		Users: []bus.UserInfo{
			{Session: 1, Name: "me", Channel: 5},
			{Session: 7, Name: "peer", Channel: 0},
		},
	}
	b.Emit(ev)
	b.Close()
	pt.onConnected(ev)
	return s, pt, &played
}

func TestSoundsDefaults(t *testing.T) {
	s, pt, played := newTestSounds(t)
	pt.onUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 1, Channel: 0}, Changed: bus.UserChangeChannel}) // own move right after connect: quiet
	s.connectedAt = time.Now().Add(-time.Minute)
	pt.onUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 1, Channel: 5}, Changed: bus.UserChangeChannel})
	pt.onUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 7, Channel: 5}, Changed: bus.UserChangeChannel})
	pt.onUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 7, Channel: 0}, Changed: bus.UserChangeChannel})
	pt.onUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 9, Channel: 0}}) // server join: off by default
	s.onTextMessage(bus.TextMessage{Sender: 7, UserSessions: []uint32{1}})
	s.onTextMessage(bus.TextMessage{Sender: 7, ChannelIDs: []uint32{5}})   // channel chat: off by default
	s.onTextMessage(bus.TextMessage{Sender: 1, UserSessions: []uint32{7}}) // own echo
	pt.onDisconnected(bus.Disconnected{})
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
	pt.onUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 9, Channel: 5}}) // into my channel, channel sound off → server sound
	pt.onUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 7, Channel: 5}, Changed: bus.UserChangeChannel})
	s.settings.SetSoundsEnabled(false)
	pt.onUserRemoved(bus.UserRemoved{Session: 7})
	if strings.Join(*played, " ") != "builtin:blip-up" {
		t.Fatalf("played %v", *played)
	}
}
