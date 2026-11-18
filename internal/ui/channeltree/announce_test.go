package channeltree

import (
	"testing"

	"io.github/murmel/internal/bus"
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
		users  int
		here   bool
		unread int
		want   string
	}{
		{0, false, 0, ""},
		{1, false, 0, "1 user"},
		{3, false, 0, "3 users"},
		{3, true, 2, "3 users, you are here, 2 unread messages"},
		{0, false, 1, "1 unread message"},
	} {
		if got := channelDescription(c.users, c.here, c.unread); got != c.want {
			t.Errorf("channelDescription(%d, %v, %d) = %q, want %q", c.users, c.here, c.unread, got, c.want)
		}
	}
}
