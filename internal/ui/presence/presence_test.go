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
