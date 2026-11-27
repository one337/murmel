package channeltree

import (
	"testing"

	"github.com/one337/murmel/internal/bus"
)

func TestUserDescription(t *testing.T) {
	plain := localAudio{volume: 1}
	for _, c := range []struct {
		u      bus.UserInfo
		a      localAudio
		unread int
		want   string
	}{
		{bus.UserInfo{}, plain, 0, ""},
		{bus.UserInfo{SelfMuted: true}, plain, 0, "muted themselves"},
		{bus.UserInfo{Muted: true}, plain, 0, "muted on the server"},
		{bus.UserInfo{SelfMuted: true, Muted: true}, plain, 0, "muted themselves, muted on the server"},
		// Deafened includes muted of the same kind, not of the other.
		{bus.UserInfo{SelfDeafened: true, SelfMuted: true}, plain, 0, "deafened themselves"},
		{bus.UserInfo{Deafened: true, Muted: true}, plain, 0, "deafened on the server"},
		{bus.UserInfo{SelfDeafened: true, SelfMuted: true, Muted: true}, plain, 0, "deafened themselves, muted on the server"},
		// Suppressed is told apart from muted.
		{bus.UserInfo{Suppressed: true}, plain, 0, "not allowed to speak here"},
		{bus.UserInfo{Muted: true, Suppressed: true}, plain, 0, "muted on the server, not allowed to speak here"},
		{bus.UserInfo{PrioritySpeaker: true, Recording: true}, plain, 0, "priority speaker, recording"},
		{bus.UserInfo{}, localAudio{volume: 1, muted: true}, 0, "muted for you"},
		{bus.UserInfo{}, localAudio{volume: 1.5, muted: true}, 0, "muted for you"},
		{bus.UserInfo{}, localAudio{volume: 1.5}, 0, "volume for you: 150 %"},
		{bus.UserInfo{}, localAudio{volume: 1.001}, 0, ""},
		{bus.UserInfo{}, plain, 1, "1 unread message"},
		{bus.UserInfo{Deafened: true, Muted: true}, localAudio{volume: 0.5}, 3, "deafened on the server, volume for you: 50 %, 3 unread messages"},
	} {
		if got := userDescription(c.u, c.a, c.unread); got != c.want {
			t.Errorf("userDescription(%+v, %+v, %d) = %q, want %q", c.u, c.a, c.unread, got, c.want)
		}
	}
}

func TestChannelDescription(t *testing.T) {
	for _, c := range []struct {
		users      int
		kids, here bool
		unread     int
		want       string
	}{
		// Empty is a channel without users and sub-channels.
		{0, false, false, 0, "empty"},
		{0, true, false, 0, ""},
		{1, true, false, 0, "1 user"},
		{3, true, false, 0, "3 users"},
		{3, true, true, 2, "3 users, you are here, 2 unread messages"},
		{0, false, false, 1, "empty, 1 unread message"},
	} {
		if got := channelDescription(c.users, c.kids, c.here, c.unread); got != c.want {
			t.Errorf("channelDescription(%d, %v, %v, %d) = %q, want %q", c.users, c.kids, c.here, c.unread, got, c.want)
		}
	}
}

// A change of the focused row is told in one line: what ended, then what
// began or changed. Muted and deafened of one kind count as one state.
func TestChangeText(t *testing.T) {
	plain := localAudio{volume: 1}
	user := func(u bus.UserInfo, a localAudio) []part { return userParts(u, a) }
	for _, c := range []struct {
		name     string
		old, now []part
		want     string
	}{
		{"mute", user(bus.UserInfo{}, plain), user(bus.UserInfo{SelfMuted: true}, plain), "muted themselves"},
		{"unmute", user(bus.UserInfo{SelfMuted: true}, plain), user(bus.UserInfo{}, plain), "unmuted themselves"},
		{"muted, then deafened", user(bus.UserInfo{SelfMuted: true}, plain),
			user(bus.UserInfo{SelfMuted: true, SelfDeafened: true}, plain), "deafened themselves"},
		{"deafened, then only muted", user(bus.UserInfo{SelfMuted: true, SelfDeafened: true}, plain),
			user(bus.UserInfo{SelfMuted: true}, plain), "undeafened themselves"},
		{"undeafen", user(bus.UserInfo{SelfMuted: true, SelfDeafened: true}, plain), user(bus.UserInfo{}, plain), "undeafened themselves"},
		{"server", user(bus.UserInfo{Deafened: true, Muted: true}, plain), user(bus.UserInfo{Muted: true}, plain), "undeafened on the server"},
		{"server unmute", user(bus.UserInfo{Muted: true}, plain), user(bus.UserInfo{}, plain), "unmuted on the server"},
		{"several at once", user(bus.UserInfo{SelfMuted: true, Suppressed: true}, plain),
			user(bus.UserInfo{Recording: true, PrioritySpeaker: true}, plain),
			"unmuted themselves, allowed to speak here again, priority speaker, recording"},
		{"ended", user(bus.UserInfo{PrioritySpeaker: true, Recording: true}, plain), user(bus.UserInfo{}, plain),
			"no longer priority speaker, stopped recording"},
		{"unmuted for you", user(bus.UserInfo{}, localAudio{volume: 1.5, muted: true}), user(bus.UserInfo{}, localAudio{volume: 1.5}),
			"unmuted for you, volume for you: 150 %"},
		{"volume back", user(bus.UserInfo{}, localAudio{volume: 0.5}), user(bus.UserInfo{}, plain), "volume for you: 100 %"},
		{"volume changed", user(bus.UserInfo{}, localAudio{volume: 0.5}), user(bus.UserInfo{}, localAudio{volume: 0.8}), "volume for you: 80 %"},
		{"nothing", user(bus.UserInfo{Recording: true}, plain), user(bus.UserInfo{Recording: true}, plain), ""},
		{"channel empties", channelParts(1, true, false), channelParts(0, false, false), "empty"},
		{"channel fills", channelParts(0, false, false), channelParts(1, true, false), "1 user"},
		{"user count", channelParts(2, true, false), channelParts(3, true, false), "3 users"},
		{"one leaves", channelParts(2, true, true), channelParts(1, true, false), "you are no longer in this channel, 1 user"},
		{"one enters", channelParts(1, true, false), channelParts(2, true, true), "2 users, you are here"},
	} {
		if got := changeText(c.old, c.now); got != c.want {
			t.Errorf("%s: changeText = %q, want %q", c.name, got, c.want)
		}
	}
}
