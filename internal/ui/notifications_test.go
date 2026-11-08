package ui

import (
	"strings"
	"testing"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/settings"
)

func newTestNotifier(t *testing.T) (*notifier, *[]string) {
	t.Helper()
	st := settings.NewMemory()
	st.SetNotificationsEnabled(true)
	b := bus.New(nil)
	t.Cleanup(b.Close)

	n := &notifier{
		app:          &App{bus: b},
		settings:     st,
		bus:          b,
		windowActive: func() bool { return false },
	}
	sent := []string{}
	n.sendNotification = func(id, title, body, target string) {
		sent = append(sent, id+"|"+title+"|"+body+"|"+target)
	}
	return n, &sent
}

func seedState(n *notifier, self uint32, users ...bus.UserInfo) {
	// Emit alone is async (pump goroutine); Close drains it, so the store
	// is deterministically seeded once it returns.
	n.bus.Emit(bus.Connected{SelfSession: self, Users: users})
	n.bus.Close()
}

func TestNotifierPMWhileUnfocused(t *testing.T) {
	n, sent := newTestNotifier(t)
	seedState(n, 1, bus.UserInfo{Session: 1, Name: "me"}, bus.UserInfo{Session: 7, Name: "peerbot"})

	n.onTextMessage(bus.TextMessage{Sender: 7, UserSessions: []uint32{1}, Message: "hello <b>QA</b> &amp; friends"})

	if len(*sent) != 1 {
		t.Fatalf("want 1 notification, got %d: %v", len(*sent), *sent)
	}
	got := (*sent)[0]
	for _, want := range []string{"pm-7", "peerbot", "hello QA & friends", "7"} {
		if !strings.Contains(got, want) {
			t.Errorf("notification %q missing %q", got, want)
		}
	}
}

func TestNotifierChannelMessageNeverNotifies(t *testing.T) {
	n, sent := newTestNotifier(t)
	seedState(n, 1, bus.UserInfo{Session: 1, Name: "me"}, bus.UserInfo{Session: 7, Name: "peerbot"})

	n.onTextMessage(bus.TextMessage{Sender: 7, ChannelIDs: []uint32{0}, Message: "channel hi"})
	n.onTextMessage(bus.TextMessage{Sender: 7, TreeIDs: []uint32{0}, Message: "tree hi"})
	n.onTextMessage(bus.TextMessage{Sender: 7, Message: "no target list"})

	if len(*sent) != 0 {
		t.Fatalf("channel message must not notify, got %v", *sent)
	}
}

func TestNotifierOwnMessageNeverNotifies(t *testing.T) {
	n, sent := newTestNotifier(t)
	seedState(n, 1, bus.UserInfo{Session: 1, Name: "me"})

	n.onTextMessage(bus.TextMessage{Sender: 1, UserSessions: []uint32{1}, Message: "echo"})

	if len(*sent) != 0 {
		t.Fatalf("own message must not notify, got %v", *sent)
	}
}

func TestNotifierFocusedSuppressesPM(t *testing.T) {
	n, sent := newTestNotifier(t)
	seedState(n, 1, bus.UserInfo{Session: 7, Name: "peerbot"})
	n.windowActive = func() bool { return true }

	n.onTextMessage(bus.TextMessage{Sender: 7, UserSessions: []uint32{1}, Message: "hi"})

	if len(*sent) != 0 {
		t.Fatalf("focused window must suppress notification, got %v", *sent)
	}
}

func TestNotifierSettingGate(t *testing.T) {
	n, sent := newTestNotifier(t)
	seedState(n, 1, bus.UserInfo{Session: 7, Name: "peerbot"})
	n.settings.SetNotificationsEnabled(false)

	n.onTextMessage(bus.TextMessage{Sender: 7, UserSessions: []uint32{1}, Message: "hi"})
	n.onDisconnected(bus.Disconnected{Reason: bus.DisconnectError})
	n.onConnected(bus.Connected{})

	if len(*sent) != 0 {
		t.Fatalf("disabled setting must suppress all notifications, got %v", *sent)
	}
}

func TestNotifierUnsyncedSelfNeverPM(t *testing.T) {
	n, sent := newTestNotifier(t)
	seedState(n, 0)
	n.onTextMessage(bus.TextMessage{Sender: 7, UserSessions: []uint32{0, 1}, Message: "hi"})

	if len(*sent) != 0 {
		t.Fatalf("unsynced self session must not classify as PM, got %v", *sent)
	}
}

func TestNotifierReconnectAfterDrop(t *testing.T) {
	n, sent := newTestNotifier(t)
	seedState(n, 1)

	n.onConnected(bus.Connected{})
	n.onDisconnected(bus.Disconnected{Reason: bus.DisconnectUser})
	n.onConnected(bus.Connected{})
	if len(*sent) != 0 {
		t.Fatalf("user disconnect cycle must not notify, got %v", *sent)
	}

	n.onDisconnected(bus.Disconnected{Reason: bus.DisconnectError, Text: "reset by peer"})
	if n.wasDropped != true {
		t.Fatal("error disconnect must arm the reconnect marker")
	}
	n.onConnected(bus.Connected{})

	if len(*sent) != 1 || !strings.Contains((*sent)[0], "reconnect|") {
		t.Fatalf("want 1 reconnect notification, got %v", *sent)
	}
	// A later connect without an intervening drop stays silent.
	n.onConnected(bus.Connected{})
	if len(*sent) != 1 {
		t.Fatalf("reconnect notification must fire once per drop, got %v", *sent)
	}
}

func TestNotifierSenderFallbackName(t *testing.T) {
	n, sent := newTestNotifier(t)
	seedState(n, 1, bus.UserInfo{Session: 1, Name: "me"})

	n.onTextMessage(bus.TextMessage{Sender: 7, UserSessions: []uint32{1}, Message: "hi"})

	if len(*sent) != 1 || !strings.Contains((*sent)[0], "session-7") {
		t.Fatalf("unknown sender needs a stable fallback title, got %v", *sent)
	}
}

func TestMessagePreview(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "hello world", "hello world"},
		{"tags", "<b>bold</b> and <a href='x'>link</a>", "bold and link"},
		{"entities", "a &amp; b &lt;c&gt; &#39;q&#39;", "a & b <c> 'q'"},
		{"whitespace", "  lots\t\tof\n\nspaces  ", "lots of spaces"},
		{"unclosed tag", "<i>italic", "italic"},
		{"empty", "", ""},
		{"only tags", "<br/><hr>", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := messagePreview(tc.in); got != tc.want {
				t.Errorf("messagePreview(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMessagePreviewTruncation(t *testing.T) {
	long := strings.Repeat("ä", previewMaxRunes+50)
	got := messagePreview(long)
	runes := []rune(got)
	if len(runes) != previewMaxRunes {
		t.Fatalf("want %d runes, got %d", previewMaxRunes, len(runes))
	}
	if runes[len(runes)-1] != '…' {
		t.Errorf("truncated preview must end with ellipsis, got %q", got)
	}
}

func presenceNotifier(t *testing.T, channel, server bool) (*notifier, *[]string, *presenceTracker) {
	t.Helper()
	n, sent := newTestNotifier(t)
	n.settings.SetNotifyChannelPresence(channel)
	n.settings.SetNotifyServerPresence(server)
	ev := bus.Connected{
		SelfSession: 1,
		Channels:    []bus.ChannelInfo{{ID: 0, Name: "Root"}, {ID: 5, Name: "Gaming"}},
		Users: []bus.UserInfo{
			{Session: 1, Name: "me", Channel: 5},
			{Session: 7, Name: "peer", Channel: 0},
		},
	}
	n.bus.Emit(ev)
	n.bus.Close()
	pt := newPresenceTracker()
	pt.listen(n.onPresence)
	n.onConnected(ev)
	pt.onConnected(ev)
	return n, sent, pt
}

func TestPresenceOffByDefault(t *testing.T) {
	st := settings.NewMemory()
	if st.NotifyChannelPresence() || st.NotifyServerPresence() {
		t.Fatal("presence notifications must default to off")
	}
	_, sent, pt := presenceNotifier(t, false, false)
	pt.onUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 9, Name: "new", Channel: 5}})
	pt.onUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 7, Name: "peer", Channel: 5}, Changed: bus.UserChangeChannel})
	pt.onUserRemoved(bus.UserRemoved{Session: 7})
	if len(*sent) != 0 {
		t.Fatalf("notified while disabled: %v", *sent)
	}
}

func TestPresenceChannelJoinLeave(t *testing.T) {
	_, sent, pt := presenceNotifier(t, true, false)
	pt.onUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 7, Name: "peer", Channel: 5}, Changed: bus.UserChangeChannel})
	pt.onUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 7, Name: "peer", Channel: 0}, Changed: bus.UserChangeChannel})
	pt.onUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 9, Name: "elsewhere", Channel: 0}})
	pt.onUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 10, Name: "direct", Channel: 5}})
	pt.onUserRemoved(bus.UserRemoved{Session: 10})
	want := []string{
		"presence-7|peer joined your channel|From Root|",
		"presence-7|peer left your channel|Moved to Root|",
		"presence-10|direct joined your channel|In Gaming|",
		"presence-10|direct left your channel||",
	}
	if strings.Join(*sent, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(*sent, "\n"), strings.Join(want, "\n"))
	}
}

func TestPresenceOwnMoveSilent(t *testing.T) {
	n, sent, pt := presenceNotifier(t, true, true)
	pt.onUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 1, Name: "me", Channel: 0}, Changed: bus.UserChangeChannel})
	if len(*sent) != 0 {
		t.Fatalf("own move notified: %v", *sent)
	}
	// After moving, peer (channel 0) is in our channel: leaving the
	// server now counts.
	n.settings.SetNotifyServerPresence(false)
	pt.onUserRemoved(bus.UserRemoved{Session: 7, Kicked: true, Reason: "spam"})
	if len(*sent) != 1 || (*sent)[0] != "presence-7|peer left your channel|Kicked from the server: spam|" {
		t.Fatalf("got %v", *sent)
	}
}

func TestPresenceServer(t *testing.T) {
	_, sent, pt := presenceNotifier(t, true, true)
	pt.onUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 9, Name: "new", Channel: 5}})
	pt.onUserRemoved(bus.UserRemoved{Session: 7})
	want := "presence-9|new connected|In Gaming|\npresence-7|peer disconnected||"
	if strings.Join(*sent, "\n") != want {
		t.Fatalf("got %v", *sent)
	}
}

func TestPresenceIgnoresInitialSync(t *testing.T) {
	n, sent := newTestNotifier(t)
	n.settings.SetNotifyServerPresence(true)
	pt := newPresenceTracker()
	pt.listen(n.onPresence)
	pt.onUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 9, Name: "early"}})
	if len(*sent) != 0 {
		t.Fatalf("sync users notified: %v", *sent)
	}
}
