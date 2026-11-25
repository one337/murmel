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

// Being suppressed is told in the words of the user menu entry that lifts
// it, for others and for oneself.
func TestLogLinesSuppressed(t *testing.T) {
	anna := bus.UserInfo{Session: 7, Name: "anna", Channel: 1}
	silent := anna
	silent.Suppressed = true
	me := bus.UserInfo{Session: 1, Name: "me", Channel: 1}
	meSilent := me
	meSilent.Suppressed = true
	for _, tt := range []struct {
		before, after bus.UserInfo
		want          string
	}{
		{anna, silent, "anna may not speak here"},
		{silent, anna, "anna may speak again"},
		{me, meSilent, "You may not speak here"},
		{meSilent, me, "You may speak again"},
	} {
		ev := presence.Event{Kind: presence.UserAudioChanged, User: tt.after, Before: tt.before, InMyChannel: true}
		if got := lines(ev, nil); got != tt.want {
			t.Errorf("suppressed %v → %v: got %q, want %q", tt.before.Suppressed, tt.after.Suppressed, got, tt.want)
		}
	}
}

// A line about another user needs their name: without one there is none.
func TestLogLinesWithoutName(t *testing.T) {
	all := map[string]bool{}
	for _, e := range LogEvents {
		all[string(e)] = true
	}
	nameless := bus.UserInfo{Session: 7, Channel: 1}
	muted := nameless
	muted.Muted, muted.SelfMuted = true, true
	for _, ev := range []presence.Event{
		{Kind: presence.UserJoinedChannel, User: nameless, To: 1},
		{Kind: presence.UserLeftChannel, User: nameless, To: 2},
		{Kind: presence.UserConnected, User: nameless, InMyChannel: true},
		{Kind: presence.UserDisconnected, User: nameless, InMyChannel: true},
		{Kind: presence.UserDisconnected, User: nameless, Reason: "Kicked from the server"},
		{Kind: presence.UserAudioChanged, User: muted, Before: nameless, InMyChannel: true},
	} {
		if got := lines(ev, all); got != "" {
			t.Errorf("kind %d without a name: %q", ev.Kind, got)
		}
	}
	if got := lines(presence.Event{Kind: presence.SelfMoved, User: bus.UserInfo{Session: 1}, To: 2}, all); got != "You moved to Games" {
		t.Errorf("own move: %q", got)
	}
}

// chatLog feeds bus events through a presence tracker and returns the
// channel chat's log lines (default settings). The local user is session
// 1 in Lobby (1); "Quiet" (3) is a channel without Speak.
func chatLog(selfSuppressed bool, events ...bus.Event) []string {
	tracker := presence.NewTracker()
	var out []string
	enabled := func(e LogEvent) bool { return LogEnabled(nil, e) }
	names := map[uint32]string{1: "Lobby", 2: "Games", 3: "Quiet"}
	tracker.Listen(func(ev presence.Event) {
		out = append(out, logLines(ev, enabled, 1, func(id uint32) string { return names[id] })...)
	})
	tracker.OnConnected(bus.Connected{SelfSession: 1, Users: []bus.UserInfo{
		{Session: 1, Name: "me", Channel: 1, Suppressed: selfSuppressed},
		{Session: 7, Name: "anna", Channel: 2},
	}})
	for _, ev := range events {
		switch ev := ev.(type) {
		case bus.UserAdded:
			tracker.OnUserAdded(ev)
		case bus.UserUpdated:
			tracker.OnUserUpdated(ev)
		case bus.UserRemoved:
			tracker.OnUserRemoved(ev)
		}
	}
	return out
}

// The server sends a move and the suppression it brings in one update.
// The chat tells the move first, then that the user may not speak; the
// end of a suppression that comes with a move is not told.
func TestLogSuppressionWithMove(t *testing.T) {
	move := func(session uint32, name string, channel uint32, suppressed bool) bus.Event {
		return bus.UserUpdated{
			User:    bus.UserInfo{Session: session, Name: name, Channel: channel, Suppressed: suppressed},
			Changed: bus.UserChangeChannel | bus.UserChangeAudio,
		}
	}
	for _, tt := range []struct {
		name           string
		selfSuppressed bool
		events         []bus.Event
		want           string
	}{
		{"someone moves into my channel without Speak", false,
			[]bus.Event{move(7, "anna", 1, true)},
			"anna joined the channel | anna may not speak here"},
		{"someone comes from a channel without Speak", false,
			[]bus.Event{move(7, "anna", 3, true), move(7, "anna", 1, false)},
			"anna joined the channel"},
		{"I move into a channel without Speak", false,
			[]bus.Event{move(1, "me", 3, true)},
			"You moved to Quiet | You may not speak here"},
		{"I move out of it", false,
			[]bus.Event{move(1, "me", 3, true), move(1, "me", 2, false)},
			"You moved to Quiet | You may not speak here | You moved to Games"},
		{"allowed to speak where I am", true,
			[]bus.Event{bus.UserUpdated{User: bus.UserInfo{Session: 1, Name: "me", Channel: 1},
				Changed: bus.UserChangeAudio}},
			"You may not speak here | You may speak again"},
	} {
		if got := strings.Join(chatLog(tt.selfSuppressed, tt.events...), " | "); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

// Connecting straight into a channel without Speak: the join line, then
// that the user may not speak, both when the name comes with the first
// state and when the server sends the suppression before the name.
func TestLogSuppressionOnConnect(t *testing.T) {
	for _, tt := range []struct {
		name   string
		events []bus.Event
	}{
		{"name with the first state", []bus.Event{
			bus.UserAdded{User: bus.UserInfo{Session: 9, Name: "guest", Channel: 1, Suppressed: true}},
		}},
		{"name after the suppression", []bus.Event{
			bus.UserAdded{User: bus.UserInfo{Session: 9, Channel: 1, Suppressed: true}},
			bus.UserUpdated{User: bus.UserInfo{Session: 9, Name: "guest", Channel: 1, Suppressed: true},
				Changed: bus.UserChangeName | bus.UserChangeChannel},
		}},
	} {
		events := append(append(append([]bus.Event{}, tt.events...), bus.UserRemoved{Session: 9}), tt.events...)
		want := "guest joined the channel | guest may not speak here | guest left the channel | " +
			"guest joined the channel | guest may not speak here"
		if got := strings.Join(chatLog(false, events...), " | "); got != want {
			t.Errorf("%s: got %q, want %q", tt.name, got, want)
		}
	}
	// Elsewhere on the server: nothing (server-wide lines are off by default).
	if got := chatLog(false, bus.UserAdded{User: bus.UserInfo{Session: 9, Name: "guest", Channel: 3, Suppressed: true}}); len(got) != 0 {
		t.Errorf("connect into another channel: %q", got)
	}
}

// Connecting oneself straight into a channel without Speak says so.
func TestLogSuppressionOnOwnConnect(t *testing.T) {
	if got := strings.Join(chatLog(true), " | "); got != "You may not speak here" {
		t.Errorf("own connect into a channel without Speak: %q", got)
	}
	if got := chatLog(false); len(got) != 0 {
		t.Errorf("own connect where one may speak: %q", got)
	}
}
