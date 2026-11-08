package bus

import (
	"errors"
	"testing"

	"io.github/murmel/internal/protocol/gumble"
)

// newBridgeBus wires a bus (synchronous idleAdd) to a hand-built fork client
// whose Channels/Users/Self maps the tests populate directly — no network.
func newBridgeBus(t *testing.T) (*Bus, *gumbleBridge, *gumble.Client) {
	t.Helper()
	b := testBus(syncIdleAdd)
	t.Cleanup(b.Close)
	client := &gumble.Client{Config: gumble.NewConfig()}
	detach := attachGumbleBridge(b, client)
	t.Cleanup(detach)
	return b, &gumbleBridge{bus: b}, client
}

func TestBridgeConnectSeedsTree(t *testing.T) {
	b, bridge, client := newBridgeBus(t)
	chConn, unsubConn := signal[Connected](t, b)
	defer unsubConn()

	root := &gumble.Channel{ID: 0, Name: "Root"}
	lounge := &gumble.Channel{ID: 2, Name: "Lounge", Parent: root, Position: 5}
	other := &gumble.Channel{ID: 3, Name: "Other"}
	root.Links = gumble.Channels{3: other}
	other.Links = gumble.Channels{0: root}
	alice := &gumble.User{Session: 7, Name: "alice", Channel: lounge, SelfMuted: true}
	client.Channels = gumble.Channels{0: root, 2: lounge, 3: other}
	client.Users = gumble.Users{7: alice}
	client.Self = alice

	welcome := "hello"
	bitrate := 72000
	bridge.OnConnect(&gumble.ConnectEvent{
		Client:         client,
		WelcomeMessage: &welcome,
		MaximumBitrate: &bitrate,
	})
	ev := waitEvent[Connected](t, chConn, "Connected")

	if ev.SelfSession != 7 || ev.Welcome != "hello" || ev.MaxBitrate == nil || *ev.MaxBitrate != 72000 {
		t.Fatalf("Connected = %+v", ev)
	}
	if len(ev.Channels) != 3 || len(ev.Users) != 1 {
		t.Fatalf("tree not seeded: %d channels, %d users", len(ev.Channels), len(ev.Users))
	}
	if ev.Channels[0].ID != 0 || ev.Channels[1].ID != 2 || ev.Channels[2].ID != 3 {
		t.Fatalf("channels not sorted by ID: %+v", ev.Channels)
	}
	if c := ev.Channels[0]; len(c.Links) != 1 || c.Links[0] != 3 {
		t.Fatalf("root links = %v, want [3]", c.Links)
	}
	if c := ev.Channels[1]; c.ParentID != 0 || c.Position != 5 {
		t.Fatalf("lounge = %+v", c)
	}
	if u := ev.Users[0]; u.Session != 7 || u.Channel != 2 || !u.SelfMuted {
		t.Fatalf("alice = %+v", u)
	}

	welcome = "mutated"
	bitrate = 1
	alice.Name = "mutated"
	root.Links = nil
	if ev.Welcome != "hello" || *ev.MaxBitrate != 72000 || ev.Users[0].Name != "alice" || len(ev.Channels[0].Links) != 1 {
		t.Fatal("event aliases fork structures")
	}

	st := b.State()
	if st.Connection != StateConnected || st.SelfSession != 7 || len(st.Channels) != 3 || len(st.Users) != 1 {
		t.Fatalf("store not seeded: %+v", st)
	}
}

func TestBridgeDisconnect(t *testing.T) {
	b, bridge, _ := newBridgeBus(t)
	ch, unsub := signal[Disconnected](t, b)
	defer unsub()

	for _, tc := range []struct {
		typ  gumble.DisconnectType
		want DisconnectReason
	}{
		{gumble.DisconnectError, DisconnectError},
		{gumble.DisconnectKicked, DisconnectKicked},
		{gumble.DisconnectBanned, DisconnectBanned},
		{gumble.DisconnectUser, DisconnectUser},
	} {
		bridge.OnDisconnect(&gumble.DisconnectEvent{Type: tc.typ, String: "s"})
		ev := waitEvent[Disconnected](t, ch, "Disconnected")
		if ev.Reason != tc.want || ev.Text != "s" {
			t.Fatalf("DisconnectType %d → %+v, want reason %d", tc.typ, ev, tc.want)
		}
	}
}

func TestBridgeUserChange(t *testing.T) {
	b, bridge, _ := newBridgeBus(t)
	chAdd, unsubAdd := signal[UserAdded](t, b)
	defer unsubAdd()
	chRem, unsubRem := signal[UserRemoved](t, b)
	defer unsubRem()
	chUpd, unsubUpd := signal[UserUpdated](t, b)
	defer unsubUpd()

	lounge := &gumble.Channel{ID: 2, Name: "Lounge"}
	bob := &gumble.User{Session: 11, Name: "bob", Channel: lounge, Deafened: true}
	alice := &gumble.User{Session: 7, Name: "alice"}

	bridge.OnUserChange(&gumble.UserChangeEvent{Type: gumble.UserChangeConnected, User: bob})
	ev := waitEvent[UserAdded](t, chAdd, "UserAdded")
	if ev.User.Session != 11 || ev.User.Channel != 2 || !ev.User.Deafened {
		t.Fatalf("UserAdded = %+v", ev)
	}
	bob.Name = "mutated"
	if ev.User.Name != "bob" {
		t.Fatal("UserAdded aliases fork user")
	}

	bridge.OnUserChange(&gumble.UserChangeEvent{
		Type:   gumble.UserChangeDisconnected | gumble.UserChangeKicked,
		User:   bob,
		Actor:  alice,
		String: "noise",
	})
	rem := waitEvent[UserRemoved](t, chRem, "UserRemoved")
	if rem.Session != 11 || rem.Actor != 7 || rem.Reason != "noise" || !rem.Kicked || rem.Banned {
		t.Fatalf("UserRemoved = %+v", rem)
	}

	bridge.OnUserChange(&gumble.UserChangeEvent{
		Type: gumble.UserChangeName | gumble.UserChangeChannel,
		User: bob,
	})
	upd := waitEvent[UserUpdated](t, chUpd, "UserUpdated")
	if upd.Changed != UserChangeName|UserChangeChannel {
		t.Fatalf("Changed = %d, want Name|Channel = %d", upd.Changed, UserChangeName|UserChangeChannel)
	}
	if !upd.Changed.Has(UserChangeName) || !upd.Changed.Has(UserChangeChannel) {
		t.Fatalf("Has() broken: %d", upd.Changed)
	}

	bridge.OnUserChange(&gumble.UserChangeEvent{Type: gumble.UserChangeAudio | gumble.UserChangeRecording | gumble.UserChangeStats, User: bob})
	upd = waitEvent[UserUpdated](t, chUpd, "UserUpdated 2")
	want := UserChangeAudio | UserChangeRecording | UserChangeStats
	if upd.Changed != want {
		t.Fatalf("Changed = %d, want %d", upd.Changed, want)
	}
}

func TestBridgeChannelChange(t *testing.T) {
	b, bridge, _ := newBridgeBus(t)
	chAdd, unsubAdd := signal[ChannelAdded](t, b)
	defer unsubAdd()
	chRem, unsubRem := signal[ChannelRemoved](t, b)
	defer unsubRem()
	chUpd, unsubUpd := signal[ChannelUpdated](t, b)
	defer unsubUpd()

	root := &gumble.Channel{ID: 0, Name: "Root"}
	l1 := &gumble.Channel{ID: 9, Name: "nine"}
	l2 := &gumble.Channel{ID: 4, Name: "four"}
	l1.Links = gumble.Channels{4: l2, 0: root}

	bridge.OnChannelChange(&gumble.ChannelChangeEvent{Type: gumble.ChannelChangeCreated, Channel: l1})
	add := waitEvent[ChannelAdded](t, chAdd, "ChannelAdded")
	if len(add.Channel.Links) != 2 || add.Channel.Links[0] != 0 || add.Channel.Links[1] != 4 {
		t.Fatalf("links not sorted: %v", add.Channel.Links)
	}
	l1.Name = "mutated"
	if add.Channel.Name != "nine" {
		t.Fatal("ChannelAdded aliases fork channel")
	}

	bridge.OnChannelChange(&gumble.ChannelChangeEvent{Type: gumble.ChannelChangeRemoved, Channel: l1})
	if rem := waitEvent[ChannelRemoved](t, chRem, "ChannelRemoved"); rem.ID != 9 {
		t.Fatalf("ChannelRemoved = %+v", rem)
	}

	bridge.OnChannelChange(&gumble.ChannelChangeEvent{
		Type:    gumble.ChannelChangeMoved | gumble.ChannelChangeDescription | gumble.ChannelChangePermission,
		Channel: l2,
	})
	upd := waitEvent[ChannelUpdated](t, chUpd, "ChannelUpdated")
	if upd.Changed != ChannelChangeMoved|ChannelChangeDescription|ChannelChangePermission {
		t.Fatalf("Changed = %d", upd.Changed)
	}
}

func TestBridgeTextMessage(t *testing.T) {
	b, bridge, _ := newBridgeBus(t)
	ch, unsub := signal[TextMessage](t, b)
	defer unsub()

	sender := &gumble.User{Session: 3}
	to := []*gumble.User{{Session: 4}, {Session: 5}}
	chs := []*gumble.Channel{{ID: 1}, {ID: 2}}
	trees := []*gumble.Channel{{ID: 0}}

	bridge.OnTextMessage(&gumble.TextMessageEvent{
		TextMessage: gumble.TextMessage{Sender: sender, Users: to, Channels: chs, Trees: trees, Message: "m"},
	})
	ev := waitEvent[TextMessage](t, ch, "TextMessage")
	if ev.Sender != 3 || ev.Message != "m" {
		t.Fatalf("TextMessage = %+v", ev)
	}
	if len(ev.UserSessions) != 2 || ev.UserSessions[0] != 4 || ev.UserSessions[1] != 5 {
		t.Fatalf("UserSessions = %v", ev.UserSessions)
	}
	if len(ev.ChannelIDs) != 2 || ev.ChannelIDs[0] != 1 || ev.ChannelIDs[1] != 2 {
		t.Fatalf("ChannelIDs = %v", ev.ChannelIDs)
	}
	if len(ev.TreeIDs) != 1 || ev.TreeIDs[0] != 0 {
		t.Fatalf("TreeIDs = %v", ev.TreeIDs)
	}

	bridge.OnTextMessage(&gumble.TextMessageEvent{TextMessage: gumble.TextMessage{Message: "bare"}})
	ev = waitEvent[TextMessage](t, ch, "bare TextMessage")
	if ev.Sender != 0 || ev.UserSessions != nil || ev.ChannelIDs != nil || ev.TreeIDs != nil {
		t.Fatalf("nil recipients must stay nil: %+v", ev)
	}
}

func TestBridgePermissionDenied(t *testing.T) {
	b, bridge, _ := newBridgeBus(t)
	ch, unsub := signal[PermissionDenied](t, b)
	defer unsub()

	bridge.OnPermissionDenied(&gumble.PermissionDeniedEvent{
		Type:       gumble.PermissionDeniedChannelFull,
		String:     "full",
		Channel:    &gumble.Channel{ID: 6},
		User:       &gumble.User{Session: 2},
		Permission: gumble.PermissionMakeChannel,
	})
	ev := waitEvent[PermissionDenied](t, ch, "PermissionDenied")
	if ev.Reason != DenyChannelFull || ev.Text != "full" || ev.ChannelID != 6 || ev.Session != 2 {
		t.Fatalf("PermissionDenied = %+v", ev)
	}

	bridge.OnPermissionDenied(&gumble.PermissionDeniedEvent{Type: gumble.PermissionDeniedOther})
	ev = waitEvent[PermissionDenied](t, ch, "PermissionDenied 2")
	if ev.Reason != DenyText || ev.ChannelID != 0 || ev.Session != 0 {
		t.Fatalf("nil channel/user must map to 0: %+v", ev)
	}
}

func TestBridgeServerConfig(t *testing.T) {
	b, bridge, _ := newBridgeBus(t)
	ch, unsub := signal[ServerConfigUpdated](t, b)
	defer unsub()

	html := true
	maxLen := 5000
	alpha := int32(1)
	bridge.OnServerConfig(&gumble.ServerConfigEvent{
		AllowHTML:            &html,
		MaximumMessageLength: &maxLen,
		CodecAlpha:           &alpha,
	})
	ev := waitEvent[ServerConfigUpdated](t, ch, "ServerConfigUpdated")
	if ev.AllowHTML == nil || !*ev.AllowHTML || ev.MaxMessageLength == nil || *ev.MaxMessageLength != 5000 {
		t.Fatalf("ServerConfigUpdated = %+v", ev)
	}
	if ev.CodecAlpha == nil || *ev.CodecAlpha != 1 || ev.Welcome != nil || ev.CodecOpus != nil {
		t.Fatalf("unset fields must stay nil: %+v", ev)
	}

	html = false
	maxLen = 0
	if *ev.AllowHTML != true || *ev.MaxMessageLength != 5000 {
		t.Fatal("event aliases fork pointers")
	}
}

// TestBridgeAttachLifecycle covers New()'s wiring path: a client with a
// Config gets the bridge attached (Close detaches it), detach is idempotent,
// and the fork's unexported fire path is not reachable from here — delivery
// itself is covered by the direct bridge.OnX tests above.
func TestBridgeAttachLifecycle(t *testing.T) {
	client := &gumble.Client{Config: gumble.NewConfig()}
	b := New(client)
	b.Emit(TextMessage{Message: "x"})
	b.Close()
	b.Close()

	detach := attachGumbleBridge(testBus(syncIdleAdd), client)
	detach()
	detach()
}

func TestBridgeNilEventsAreSafe(t *testing.T) {
	bridge := &gumbleBridge{bus: New(nil)}
	bridge.OnConnect(nil)
	bridge.OnDisconnect(nil)
	bridge.OnTextMessage(nil)
	bridge.OnUserChange(nil)
	bridge.OnChannelChange(nil)
	bridge.OnPermissionDenied(nil)
	bridge.OnServerConfig(nil)
	bridge.bus.Close()
}

func TestTranslateReject(t *testing.T) {
	ev, ok := TranslateReject(&gumble.RejectError{Type: gumble.RejectUsernameInUse, Reason: "taken"})
	if !ok || ev.Reason != RejectUsernameInUse || ev.Text != "taken" {
		t.Fatalf("TranslateReject = %+v, %v", ev, ok)
	}
	ev, ok = TranslateReject(gumble.RejectError{Type: gumble.RejectServerFull})
	if !ok || ev.Reason != RejectServerFull {
		t.Fatalf("value-form RejectError not handled: %+v, %v", ev, ok)
	}
	if _, ok := TranslateReject(errors.New("plain")); ok {
		t.Fatal("plain error must not translate")
	}
}
