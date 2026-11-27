package presence

import (
	"testing"

	"github.com/one337/murmel/internal/bus"
)

func connectedTracker() (*Tracker, *[]Event) {
	t := NewTracker()
	var got []Event
	t.Listen(func(ev Event) { got = append(got, ev) })
	t.OnConnected(bus.Connected{SelfSession: 1, Users: []bus.UserInfo{
		{Session: 1, Name: "me", Channel: 5},
		{Session: 7, Name: "anna", Channel: 0},
	}})
	got = nil
	return t, &got
}

// A user the server announces before their name (it sends the right to
// speak first) is reported once, with the name and the channel they are in
// when it arrives.
func TestUserNamedLateConnectsOnce(t *testing.T) {
	tr, got := connectedTracker()
	tr.OnUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 9, Channel: 0, Suppressed: true}})
	tr.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 9, Channel: 0, Suppressed: false},
		Changed: bus.UserChangeAudio})
	tr.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 9, Channel: 5}, Changed: bus.UserChangeChannel})
	if len(*got) != 0 {
		t.Fatalf("events before the name arrived: %+v", *got)
	}
	tr.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 9, Name: "SuperUser", Channel: 5},
		Changed: bus.UserChangeName})
	tr.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 9, Name: "SuperUser", Channel: 5, Recording: true}})
	if len(*got) != 1 {
		t.Fatalf("events %+v, want one connect", *got)
	}
	ev := (*got)[0]
	if ev.Kind != UserConnected || ev.User.Name != "SuperUser" || ev.To != 5 || !ev.InMyChannel {
		t.Errorf("event %+v, want SuperUser connected into my channel 5", ev)
	}
	tr.OnUserRemoved(bus.UserRemoved{Session: 9})
	if len(*got) != 2 || (*got)[1].Kind != UserDisconnected || (*got)[1].User.Name != "SuperUser" {
		t.Errorf("events %+v, want a disconnect of SuperUser last", *got)
	}
}

// A user who leaves before their name arrives is not reported at all.
func TestUserGoneBeforeNameIsNotReported(t *testing.T) {
	tr, got := connectedTracker()
	tr.OnUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 9, Channel: 5, Suppressed: true}})
	tr.OnUserRemoved(bus.UserRemoved{Session: 9})
	tr.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 9, Name: "late", Channel: 5}, Changed: bus.UserChangeName})
	if len(*got) != 0 {
		t.Errorf("events %+v, want none", *got)
	}
}

// The users already on the server when we connect are not reported, also
// not when their state changes to carry a name afterwards.
func TestUsersAlreadyThereAreNotReported(t *testing.T) {
	tr := NewTracker()
	var got []Event
	tr.Listen(func(ev Event) { got = append(got, ev) })
	tr.OnUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 7, Name: "anna", Channel: 5}})
	tr.OnUserAdded(bus.UserAdded{User: bus.UserInfo{Session: 8, Channel: 5}})
	tr.OnConnected(bus.Connected{SelfSession: 1, Users: []bus.UserInfo{
		{Session: 1, Name: "me", Channel: 5},
		{Session: 7, Name: "anna", Channel: 5},
		{Session: 8, Channel: 5},
	}})
	tr.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 8, Name: "bob", Channel: 5}, Changed: bus.UserChangeName})
	if len(got) != 1 || got[0].Kind != SelfConnected {
		t.Errorf("events %+v, want only our own connect", got)
	}
}

// After an own move the tracker tells whether one may speak, from one's
// state after the update: in a channel without Speak every time (the
// server sends no suppression when it does not change), out of one once,
// between channels with Speak never. Without a move only a change is told.
func TestSelfSpeakChanged(t *testing.T) {
	type step struct {
		channel    uint32
		suppressed bool
		changed    bus.UserChanges
	}
	moveTo := func(ch uint32, sup bool, audio bool) step {
		c := bus.UserChangeChannel
		if audio {
			c |= bus.UserChangeAudio
		}
		return step{ch, sup, c}
	}
	for _, tt := range []struct {
		name  string
		steps []step
		want  string // per step: "-" nothing, "L" locked, "A" allowed
	}{
		{"allowed → locked → locked → allowed → allowed",
			[]step{moveTo(3, true, true), moveTo(4, true, false), moveTo(2, false, true), moveTo(6, false, false)},
			"LLA-"},
		{"let speak where one is, then moved on",
			[]step{moveTo(3, true, true), {3, false, bus.UserChangeAudio}, moveTo(4, true, true),
				{4, false, bus.UserChangeAudio}, moveTo(2, false, false)},
			"LALA-"},
		{"another audio change without a move", []step{{5, false, bus.UserChangeAudio}}, "-"},
	} {
		tr, got := connectedTracker()
		var out []byte
		for i, s := range tt.steps {
			*got = nil
			tr.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 1, Name: "me", Channel: s.channel,
				Suppressed: s.suppressed}, Changed: s.changed})
			mark := byte('-')
			for _, ev := range *got {
				if ev.Kind != SelfSpeakChanged {
					continue
				}
				if mark != '-' {
					t.Errorf("%s step %d: more than one event", tt.name, i)
				}
				mark = map[bool]byte{false: 'L', true: 'A'}[ev.Allowed]
				if ev.Moved != s.changed.Has(bus.UserChangeChannel) {
					t.Errorf("%s step %d: Moved %v", tt.name, i, ev.Moved)
				}
			}
			out = append(out, mark)
		}
		if string(out) != tt.want {
			t.Errorf("%s: got %s, want %s", tt.name, out, tt.want)
		}
	}
	// Someone else's suppression is not one's own.
	tr, got := connectedTracker()
	tr.OnUserUpdated(bus.UserUpdated{User: bus.UserInfo{Session: 7, Name: "anna", Channel: 5, Suppressed: true},
		Changed: bus.UserChangeChannel | bus.UserChangeAudio})
	for _, ev := range *got {
		if ev.Kind == SelfSpeakChanged {
			t.Errorf("event for another user: %+v", ev)
		}
	}
}
