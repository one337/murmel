package presence

import (
	"context"

	"io.github/murmel/internal/bus"
)

// Kind classifies a presence change relevant to the local user.
type Kind int

const (
	SelfConnected Kind = iota
	SelfDisconnected
	SelfMoved
	UserConnected
	UserDisconnected
	UserJoinedChannel // another user moved into our channel
	UserLeftChannel   // another user moved out of our channel
	// UserAudioChanged: a user's mute/deafen state changed (Before holds
	// the previous state); InMyChannel tells whether it is in ours.
	UserAudioChanged
)

// Event is one presence change. For connects/disconnects,
// inMyChannel tells whether the user is (was) in our channel.
type Event struct {
	Kind        Kind
	User        bus.UserInfo
	From, To    uint32 // channel moves
	InMyChannel bool
	Reason      string // disconnects: kick/ban text, "" otherwise
	// Before is the user's previous state (audio changes).
	Before bus.UserInfo
}

// Tracker turns raw bus user events into presence events for the
// notifier and the event sounds. It keeps its own roster: the shared state
// store is updated before handlers run, so a removed user's name and
// channel are already gone from it when UserRemoved is handled. The
// initial sync (users already on the server) produces no events.
type Tracker struct {
	connected bool
	self      uint32
	users     map[uint32]bus.UserInfo
	listeners []func(Event)
}

func NewTracker() *Tracker { return &Tracker{} }

// listen registers f for every presence event.
func (t *Tracker) Listen(f func(Event)) { t.listeners = append(t.listeners, f) }

// subscribe wires the tracker to the bus for the lifetime of ctx.
func (t *Tracker) Subscribe(ctx context.Context, b *bus.Bus) {
	bus.SubscribeCtx(ctx, b, t.OnConnected)
	bus.SubscribeCtx(ctx, b, t.OnDisconnected)
	bus.SubscribeCtx(ctx, b, t.OnUserAdded)
	bus.SubscribeCtx(ctx, b, t.OnUserRemoved)
	bus.SubscribeCtx(ctx, b, t.OnUserUpdated)
}

func (t *Tracker) emit(ev Event) {
	for _, f := range t.listeners {
		f(ev)
	}
}

func (t *Tracker) myChannel() (uint32, bool) {
	me, ok := t.users[t.self]
	return me.Channel, ok
}

func (t *Tracker) OnConnected(ev bus.Connected) {
	t.connected = true
	t.self = ev.SelfSession
	t.users = make(map[uint32]bus.UserInfo, len(ev.Users))
	for _, u := range ev.Users {
		t.users[u.Session] = u
	}
	t.emit(Event{Kind: SelfConnected, User: t.users[t.self]})
}

func (t *Tracker) OnDisconnected(bus.Disconnected) {
	if !t.connected {
		return
	}
	t.connected = false
	t.users = nil
	t.emit(Event{Kind: SelfDisconnected})
}

func (t *Tracker) OnUserAdded(ev bus.UserAdded) {
	if !t.connected {
		return // initial sync: already on the server
	}
	t.users[ev.User.Session] = ev.User
	if ev.User.Session == t.self {
		return
	}
	mine, ok := t.myChannel()
	t.emit(Event{Kind: UserConnected, User: ev.User, To: ev.User.Channel,
		InMyChannel: ok && ev.User.Channel == mine})
}

func (t *Tracker) OnUserRemoved(ev bus.UserRemoved) {
	if !t.connected {
		return
	}
	u, known := t.users[ev.Session]
	delete(t.users, ev.Session)
	if !known || ev.Session == t.self {
		return
	}
	reason := ""
	switch {
	case ev.Banned:
		reason = tr("Banned from the server")
	case ev.Kicked:
		reason = tr("Kicked from the server")
	}
	if ev.Reason != "" {
		if reason == "" {
			reason = ev.Reason
		} else {
			reason += ": " + ev.Reason
		}
	}
	mine, ok := t.myChannel()
	t.emit(Event{Kind: UserDisconnected, User: u, From: u.Channel,
		InMyChannel: ok && u.Channel == mine, Reason: reason})
}

func (t *Tracker) OnUserUpdated(ev bus.UserUpdated) {
	if !t.connected {
		return
	}
	prev, known := t.users[ev.User.Session]
	t.users[ev.User.Session] = ev.User
	if !known {
		return
	}
	if ev.Changed.Has(bus.UserChangeAudio) {
		mine, ok := t.myChannel()
		t.emit(Event{Kind: UserAudioChanged, User: ev.User, Before: prev,
			InMyChannel: ok && ev.User.Channel == mine})
	}
	if !ev.Changed.Has(bus.UserChangeChannel) || prev.Channel == ev.User.Channel {
		return
	}
	if ev.User.Session == t.self {
		t.emit(Event{Kind: SelfMoved, User: ev.User, From: prev.Channel, To: ev.User.Channel})
		return
	}
	mine, ok := t.myChannel()
	switch {
	case !ok:
	case ev.User.Channel == mine:
		t.emit(Event{Kind: UserJoinedChannel, User: ev.User, From: prev.Channel, To: mine})
	case prev.Channel == mine:
		t.emit(Event{Kind: UserLeftChannel, User: ev.User, From: mine, To: ev.User.Channel})
	}
}
