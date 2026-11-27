package presence

import (
	"context"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
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
	// SelfSpeakChanged: whether one may speak, told after every own move
	// into a channel without the right to speak (also from one such
	// channel into another), after a move out of one, and when it changes
	// where one is (Moved false). Allowed is whether one may speak now.
	SelfSpeakChanged
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
	// Moved: the audio change came with a move to another channel (the
	// server works out anew whether the user may speak there); the move
	// is reported first.
	Moved bool
	// Allowed: whether one may speak now (SelfSpeakChanged).
	Allowed bool
}

// Tracker turns raw bus user events into presence events for the
// notifier and the event sounds. It keeps its own roster: the shared state
// store is updated before handlers run, so a removed user's name and
// channel are already gone from it when UserRemoved is handled. The
// initial sync (users already on the server) produces no events.
//
// A server may announce a new user before their name (the server sends
// their right to speak in the channel first), so a user who arrives
// without a name is reported as connected once the name follows, in the
// channel they are in then; one who leaves before that is not reported.
type Tracker struct {
	connected bool
	self      uint32
	users     map[uint32]bus.UserInfo
	unnamed   map[uint32]bool
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
	t.unnamed = make(map[uint32]bool)
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
	t.unnamed = nil
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
	if ev.User.Name == "" {
		t.unnamed[ev.User.Session] = true
		return
	}
	t.emitConnected(ev.User)
}

func (t *Tracker) emitConnected(u bus.UserInfo) {
	mine, ok := t.myChannel()
	t.emit(Event{Kind: UserConnected, User: u, To: u.Channel,
		InMyChannel: ok && u.Channel == mine})
}

func (t *Tracker) OnUserRemoved(ev bus.UserRemoved) {
	if !t.connected {
		return
	}
	u, known := t.users[ev.Session]
	unnamed := t.unnamed[ev.Session]
	delete(t.users, ev.Session)
	delete(t.unnamed, ev.Session)
	if !known || unnamed || ev.Session == t.self {
		return
	}
	reason := ""
	switch {
	case ev.Banned:
		reason = i18n.Tr("Banned from the server")
	case ev.Kicked:
		reason = i18n.Tr("Kicked from the server")
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
	if t.unnamed[ev.User.Session] {
		if ev.User.Name != "" {
			delete(t.unnamed, ev.User.Session)
			t.emitConnected(ev.User)
		}
		return
	}
	moved := ev.Changed.Has(bus.UserChangeChannel) && prev.Channel != ev.User.Channel
	if moved {
		t.emitMove(prev, ev.User)
	}
	if ev.Changed.Has(bus.UserChangeAudio) {
		mine, ok := t.myChannel()
		t.emit(Event{Kind: UserAudioChanged, User: ev.User, Before: prev,
			InMyChannel: ok && ev.User.Channel == mine, Moved: moved})
	}
	if ev.User.Session == t.self {
		t.selfSpeak(prev, ev.User, moved)
	}
}

// selfSpeak tells whether one may speak after an own state update. A
// server sends the suppression only when it changes, so a move from one
// channel without the right to speak into another carries none: after a
// move the state is told from the user's state as it is now.
func (t *Tracker) selfSpeak(prev, u bus.UserInfo, moved bool) {
	switch {
	case moved && (u.Suppressed || prev.Suppressed):
	case !moved && u.Suppressed != prev.Suppressed:
	default:
		return
	}
	t.emit(Event{Kind: SelfSpeakChanged, User: u, Before: prev, Moved: moved, Allowed: !u.Suppressed})
}

// emitMove reports a user's move from prev's channel to u's.
func (t *Tracker) emitMove(prev, u bus.UserInfo) {
	if u.Session == t.self {
		t.emit(Event{Kind: SelfMoved, User: u, From: prev.Channel, To: u.Channel})
		return
	}
	mine, ok := t.myChannel()
	switch {
	case !ok:
	case u.Channel == mine:
		t.emit(Event{Kind: UserJoinedChannel, User: u, From: prev.Channel, To: mine})
	case prev.Channel == mine:
		t.emit(Event{Kind: UserLeftChannel, User: u, From: mine, To: u.Channel})
	}
}
