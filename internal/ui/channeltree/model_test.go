package channeltree

import (
	"testing"

	"io.github/murmel/internal/bus"
)

func ch(id, parent uint32, name string, pos int32) bus.ChannelInfo {
	return bus.ChannelInfo{ID: id, ParentID: parent, Name: name, Position: pos}
}

func walkIDs(m *treeModel) []entry {
	return m.walk()
}

// Mumble's root channel is ID 0 and its direct children also carry
// ParentID 0; every one of them must be shown under the root.
func TestRootChildrenAllVisible(t *testing.T) {
	m := newTreeModel()
	m.apply(bus.Connected{
		Channels: []bus.ChannelInfo{
			ch(0, 0, "Root", 0),
			ch(1, 0, "Lobby", 0),
			ch(2, 0, "Games", 1),
			ch(3, 2, "Sub", 0),
		},
		Users: []bus.UserInfo{{Session: 7, Name: "simon", Channel: 3}},
	})

	got := walkIDs(m)
	want := []entry{
		{entryChannel, 0, 0},
		{entryChannel, 1, 1},
		{entryChannel, 2, 1},
		{entryChannel, 3, 2},
		{entryUser, 7, 3},
	}
	if len(got) != len(want) {
		t.Fatalf("walk = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("walk[%d] = %+v, want %+v (full: %+v)", i, got[i], want[i], got)
		}
	}
	for id, wantDepth := range map[uint32]int{0: 0, 1: 1, 2: 1, 3: 2} {
		if d := depthOf(m, id); d != wantDepth {
			t.Errorf("depthOf(%d) = %d, want %d", id, d, wantDepth)
		}
	}
}

// A child whose parent arrives later (parent ID > child ID after a move)
// and a user in a not-yet-known channel are attached once the channel
// appears.
func TestLateParentAndUserAttach(t *testing.T) {
	m := newTreeModel()
	m.apply(bus.Connected{
		Channels: []bus.ChannelInfo{
			ch(0, 0, "Root", 0),
			ch(4, 9, "Child", 0),
			ch(9, 0, "Parent", 0),
		},
		Users: []bus.UserInfo{{Session: 5, Name: "a", Channel: 12}},
	})
	m.apply(bus.ChannelAdded{Channel: ch(12, 4, "Grandchild", 0)})

	var ids []uint32
	for _, e := range m.walk() {
		ids = append(ids, e.id)
	}
	want := []uint32{0, 9, 4, 12, 5}
	if len(ids) != len(want) {
		t.Fatalf("walk ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("walk ids = %v, want %v", ids, want)
		}
	}
}

func TestUserMoveBetweenRootChildren(t *testing.T) {
	m := newTreeModel()
	m.apply(bus.Connected{
		SelfSession: 1,
		Channels:    []bus.ChannelInfo{ch(0, 0, "Root", 0), ch(1, 0, "A", 0), ch(2, 0, "B", 1)},
		Users:       []bus.UserInfo{{Session: 1, Name: "me", Channel: 1}},
	})
	if got := m.selfChannel(); got != 1 {
		t.Fatalf("selfChannel = %d, want 1", got)
	}
	if !m.apply(bus.UserUpdated{User: bus.UserInfo{Session: 1, Name: "me", Channel: 2}}) {
		t.Fatal("channel move must be structural")
	}
	if got := m.selfChannel(); got != 2 {
		t.Fatalf("selfChannel after move = %d, want 2", got)
	}
	if len(m.channels[1].users) != 0 || len(m.channels[2].users) != 1 {
		t.Fatalf("users not moved: A=%v B=%v", m.channels[1].users, m.channels[2].users)
	}
}

// TestCollapsedChannelHidesUsers: collapsing a channel hides its users,
// not only its sub-channels.
func TestCollapsedChannelHidesUsers(t *testing.T) {
	m := newTreeModel()
	m.apply(bus.Connected{
		Channels: []bus.ChannelInfo{ch(0, 0, "Root", 0), ch(1, 0, "A", 0)},
		Users:    []bus.UserInfo{{Session: 7, Name: "u", Channel: 1}},
	})
	m.channels[1].expanded = false
	for _, e := range m.walk() {
		if e.kind == entryUser {
			t.Fatalf("user %d shown in a collapsed channel: %+v", e.id, m.walk())
		}
	}
	m.channels[1].expanded = true
	if got := m.walk(); len(got) != 3 || got[2].kind != entryUser {
		t.Fatalf("expanded walk = %+v", got)
	}
}
