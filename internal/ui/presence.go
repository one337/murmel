package ui

import (
	"context"

	"io.github/murmel/internal/bus"
)

// presenceKind classifies a presence change relevant to the local user.
type presenceKind int

const (
	presenceSelfConnected presenceKind = iota
	presenceSelfDisconnected
	presenceSelfMoved
	presenceUserConnected
	presenceUserDisconnected
	presenceUserJoinedChannel // another user moved into our channel
	presenceUserLeftChannel   // another user moved out of our channel
)

// presenceEvent is one presence change. For connects/disconnects,
// inMyChannel tells whether the user is (was) in our channel.
type presenceEvent struct {
	kind        presenceKind
	user        bus.UserInfo
	from, to    uint32 // channel moves
	inMyChannel bool
	reason      string // disconnects: kick/ban text, "" otherwise
}

// presenceTracker turns raw bus user events into presence events for the
// notifier and the event sounds. It keeps its own roster: the shared state
// store is updated before handlers run, so a removed user's name and
// channel are already gone from it when UserRemoved is handled. The
// initial sync (users already on the server) produces no events.
type presenceTracker struct {
	connected bool
	self      uint32
	users     map[uint32]bus.UserInfo
	listeners []func(presenceEvent)
}

func newPresenceTracker() *presenceTracker { return &presenceTracker{} }

// listen registers f for every presence event.
func (t *presenceTracker) listen(f func(presenceEvent)) { t.listeners = append(t.listeners, f) }

// subscribe wires the tracker to the bus for the lifetime of ctx.
func (t *presenceTracker) subscribe(ctx context.Context, b *bus.Bus) {
	bus.SubscribeCtx(ctx, b, t.onConnected)
	bus.SubscribeCtx(ctx, b, t.onDisconnected)
	bus.SubscribeCtx(ctx, b, t.onUserAdded)
	bus.SubscribeCtx(ctx, b, t.onUserRemoved)
	bus.SubscribeCtx(ctx, b, t.onUserUpdated)
}

func (t *presenceTracker) emit(ev presenceEvent) {
	for _, f := range t.listeners {
		f(ev)
	}
}

func (t *presenceTracker) myChannel() (uint32, bool) {
	me, ok := t.users[t.self]
	return me.Channel, ok
}

func (t *presenceTracker) onConnected(ev bus.Connected) {
	t.connected = true
	t.self = ev.SelfSession
	t.users = make(map[uint32]bus.UserInfo, len(ev.Users))
	for _, u := range ev.Users {
		t.users[u.Session] = u
	}
	t.emit(presenceEvent{kind: presenceSelfConnected, user: t.users[t.self]})
}

func (t *presenceTracker) onDisconnected(bus.Disconnected) {
	if !t.connected {
		return
	}
	t.connected = false
	t.users = nil
	t.emit(presenceEvent{kind: presenceSelfDisconnected})
}

func (t *presenceTracker) onUserAdded(ev bus.UserAdded) {
	if !t.connected {
		return // initial sync: already on the server
	}
	t.users[ev.User.Session] = ev.User
	if ev.User.Session == t.self {
		return
	}
	mine, ok := t.myChannel()
	t.emit(presenceEvent{kind: presenceUserConnected, user: ev.User, to: ev.User.Channel,
		inMyChannel: ok && ev.User.Channel == mine})
}

func (t *presenceTracker) onUserRemoved(ev bus.UserRemoved) {
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
	t.emit(presenceEvent{kind: presenceUserDisconnected, user: u, from: u.Channel,
		inMyChannel: ok && u.Channel == mine, reason: reason})
}

func (t *presenceTracker) onUserUpdated(ev bus.UserUpdated) {
	if !t.connected {
		return
	}
	prev, known := t.users[ev.User.Session]
	t.users[ev.User.Session] = ev.User
	if !known || !ev.Changed.Has(bus.UserChangeChannel) || prev.Channel == ev.User.Channel {
		return
	}
	if ev.User.Session == t.self {
		t.emit(presenceEvent{kind: presenceSelfMoved, user: ev.User, from: prev.Channel, to: ev.User.Channel})
		return
	}
	mine, ok := t.myChannel()
	switch {
	case !ok:
	case ev.User.Channel == mine:
		t.emit(presenceEvent{kind: presenceUserJoinedChannel, user: ev.User, from: prev.Channel, to: mine})
	case prev.Channel == mine:
		t.emit(presenceEvent{kind: presenceUserLeftChannel, user: ev.User, from: mine, to: ev.User.Channel})
	}
}
