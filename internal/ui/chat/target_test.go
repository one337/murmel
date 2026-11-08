package chat

import (
	"testing"

	"io.github/murmel/internal/bus"
)

func TestTargetFromEvent(t *testing.T) {
	cases := []struct {
		name string
		ev   bus.ChatTargetChanged
		want target
	}{
		{"zero follows self", bus.ChatTargetChanged{}, target{mode: targetSelf}},
		{"session selects user", bus.ChatTargetChanged{Session: 7}, target{mode: targetUser, session: 7}},
		{"channel selects channel", bus.ChatTargetChanged{ChannelID: 3}, target{mode: targetChannel, channelID: 3}},
		{"session wins over channel", bus.ChatTargetChanged{ChannelID: 3, Session: 9}, target{mode: targetUser, session: 9}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := targetFromEvent(tc.ev)
			if got != tc.want {
				t.Fatalf("targetFromEvent(%+v) = %+v, want %+v", tc.ev, got, tc.want)
			}
		})
	}
}

func TestTargetResolvedChannelID(t *testing.T) {
	self := target{mode: targetSelf}
	if got := self.resolvedChannelID(42); got != 42 {
		t.Fatalf("self target resolved to %d, want 42", got)
	}
	if got := self.resolvedChannelID(0); got != 0 {
		t.Fatalf("self target with unknown channel resolved to %d, want 0", got)
	}
	fixed := target{mode: targetChannel, channelID: 5}
	if got := fixed.resolvedChannelID(42); got != 5 {
		t.Fatalf("channel target resolved to %d, want 5", got)
	}
	pm := target{mode: targetUser, session: 9}
	if got := pm.resolvedChannelID(42); got != 42 {
		t.Fatalf("user target resolved to %d, want 42 (ignored)", got)
	}
}

func TestTargetBannerText(t *testing.T) {
	cases := []struct {
		name                    string
		tgt                     target
		channelName, userName   string
		want                    string
	}{
		{"channel", target{mode: targetChannel, channelID: 1}, "Lobby", "", "#Lobby"},
		{"channel name fallback", target{mode: targetChannel, channelID: 1}, "", "", "#channel"},
		{"self", target{mode: targetSelf}, "Lobby", "", "#Lobby"},
		{"self name fallback", target{mode: targetSelf}, "", "", "#Current channel"},
		{"user", target{mode: targetUser, session: 2}, "", "alice", "Private message to @alice"},
		{"user name fallback", target{mode: targetUser, session: 2}, "", "", "Private message to @Unknown user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tgt.bannerText(tc.channelName, tc.userName); got != tc.want {
				t.Fatalf("bannerText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRowCSSClass(t *testing.T) {
	cases := []struct {
		kind rowKind
		want string
	}{
		{rowMessage, ""},
		{rowPrivate, ""},
		{rowError, "error"},
		{rowNotice, "dim-label"},
	}
	for _, tc := range cases {
		if got := rowCSSClass(tc.kind); got != tc.want {
			t.Fatalf("rowCSSClass(%d) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestErrorRowText(t *testing.T) {
	if got := errorRowText(""); got != "Message not delivered." {
		t.Fatalf("empty reason = %q", got)
	}
	if got := errorRowText("message too long"); got != "Message not delivered: message too long" {
		t.Fatalf("reason = %q", got)
	}
}
