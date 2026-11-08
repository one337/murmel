package chat

import (
	"strings"
	"testing"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/ui/presence"
)

func lines(ev presence.Event, stored map[string]bool) string {
	enabled := func(e LogEvent) bool { return LogEnabled(stored, e) }
	names := map[uint32]string{1: "Lobby", 2: "Games"}
	return strings.Join(logLines(ev, enabled, 1, func(id uint32) string { return names[id] }), " | ")
}

func TestLogLinesDefaults(t *testing.T) {
	anna := bus.UserInfo{Session: 7, Name: "anna", Channel: 1}
	for _, tt := range []struct {
		ev   presence.Event
		want string
	}{
		{presence.Event{Kind: presence.UserJoinedChannel, User: anna, To: 1}, "anna joined the channel"},
		{presence.Event{Kind: presence.UserLeftChannel, User: anna, To: 2}, "anna moved to Games"},
		{presence.Event{Kind: presence.SelfMoved, User: bus.UserInfo{Session: 1}, To: 2}, "You moved to Games"},
		{presence.Event{Kind: presence.UserConnected, User: anna, InMyChannel: true}, "anna joined the channel"},
		{presence.Event{Kind: presence.UserConnected, User: anna}, ""}, // server-wide: off by default
		{presence.Event{Kind: presence.UserDisconnected, User: anna, InMyChannel: true}, "anna left the channel"},
		{presence.Event{Kind: presence.UserDisconnected, User: anna, Reason: "Kicked from the server: spam"},
			"anna left the server (Kicked from the server: spam)"},
		{presence.Event{Kind: presence.UserAudioChanged, User: bus.UserInfo{Session: 7, Name: "anna", Muted: true},
			Before: anna, InMyChannel: true}, "anna was muted by the server"},
		{presence.Event{Kind: presence.UserAudioChanged, User: bus.UserInfo{Session: 7, Name: "anna", SelfMuted: true},
			Before: anna, InMyChannel: true}, ""}, // self-mute: off by default
		{presence.Event{Kind: presence.UserAudioChanged, User: bus.UserInfo{Session: 7, Name: "anna", Muted: true},
			Before: anna}, ""}, // not in my channel
	} {
		if got := lines(tt.ev, nil); got != tt.want {
			t.Errorf("kind %d: got %q, want %q", tt.ev.Kind, got, tt.want)
		}
	}
}

func TestLogLinesOverrides(t *testing.T) {
	anna := bus.UserInfo{Session: 7, Name: "anna"}
	on := map[string]bool{"server-connect": true, "self-mute": true, "channel-join": false}
	if got := lines(presence.Event{Kind: presence.UserConnected, User: anna}, on); got != "anna connected to the server" {
		t.Errorf("server connect: %q", got)
	}
	if got := lines(presence.Event{Kind: presence.UserJoinedChannel, User: anna}, on); got != "" {
		t.Errorf("switched-off channel join logged: %q", got)
	}
	me := bus.UserInfo{Session: 1, Name: "me"}
	got := lines(presence.Event{Kind: presence.UserAudioChanged, User: bus.UserInfo{Session: 1, Name: "me", SelfMuted: true, SelfDeafened: true}, Before: me}, on)
	if got != "You muted yourself | You deafened yourself" {
		t.Errorf("self audio: %q", got)
	}
}
