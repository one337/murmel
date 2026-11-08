package channeltree

import (
	"testing"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/protocol/gumble"
)

func permModel(rootPerm, chanPerm *int) *treeModel {
	m := newTreeModel()
	root := ch(0, 0, "Root", 0)
	root.Permissions = rootPerm
	sub := ch(5, 0, "Gaming", 0)
	sub.Permissions = chanPerm
	m.addChannel(root)
	m.addChannel(sub)
	m.self = 1
	m.users[1] = bus.UserInfo{Session: 1, Name: "me", Channel: 0}
	m.users[7] = bus.UserInfo{Session: 7, Name: "peer", Channel: 5}
	return m
}

func p(perms ...gumble.Permission) *int {
	v := 0
	for _, x := range perms {
		v |= int(x)
	}
	return &v
}

func TestUnknownPermissionsDisableEverything(t *testing.T) {
	m := permModel(nil, nil)
	if got := channelMenuPerms(m, 5); got != (channelMenuState{}) {
		t.Fatalf("unknown channel permissions enabled %+v", got)
	}
	u := userMenuPerms(m, 7)
	if u.kick || u.ban || u.mute || u.move || u.register {
		t.Fatalf("unknown permissions enabled admin actions: %+v", u)
	}
	if !u.message || !u.stats {
		t.Fatal("message/stats must stay available")
	}
}

func TestRegularUserPermissions(t *testing.T) {
	// A normal user: may enter and speak, nothing else.
	m := permModel(p(gumble.PermissionEnter, gumble.PermissionTraverse, gumble.PermissionRegisterSelf),
		p(gumble.PermissionEnter, gumble.PermissionTraverse))
	c := channelMenuPerms(m, 5)
	if !c.join || c.edit || c.remove || c.acl || c.createSub {
		t.Fatalf("regular user channel menu = %+v, want only join", c)
	}
	if channelMenuPerms(m, 0).join {
		t.Fatal("joining the channel you are in must be disabled")
	}
	u := userMenuPerms(m, 7)
	if u.kick || u.ban || u.mute || u.move || u.register {
		t.Fatalf("regular user gets admin actions: %+v", u)
	}
	if !userMenuPerms(m, 1).register {
		t.Fatal("an unregistered user with RegisterSelf must be able to register")
	}
}

func TestAdminPermissions(t *testing.T) {
	all := p(gumble.PermissionWrite, gumble.PermissionEnter, gumble.PermissionMuteDeafen,
		gumble.PermissionMove, gumble.PermissionMakeChannel, gumble.PermissionKick,
		gumble.PermissionBan, gumble.PermissionRegister)
	m := permModel(all, all)
	c := channelMenuPerms(m, 5)
	if !c.edit || !c.remove || !c.acl || !c.createSub || !c.join {
		t.Fatalf("admin channel menu = %+v", c)
	}
	if channelMenuPerms(m, 0).remove {
		t.Fatal("the root channel must never be removable")
	}
	u := userMenuPerms(m, 7)
	if !u.kick || !u.ban || !u.mute || !u.move || !u.register || !u.priority {
		t.Fatalf("admin user menu = %+v", u)
	}
}

func TestPermissionChannels(t *testing.T) {
	m := permModel(nil, nil)
	if got := permissionChannels(m, 0, 7); len(got) != 2 || got[0] != 0 || got[1] != 5 {
		t.Fatalf("user in channel 5 needs root and 5, got %v", got)
	}
	if got := permissionChannels(m, 5, 0); len(got) != 2 || got[1] != 5 {
		t.Fatalf("channel 5 menu needs root and 5, got %v", got)
	}
}
