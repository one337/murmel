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
	if userMenuPerms(m, 1).register {
		t.Fatal("registering without a certificate must be disabled")
	}
	self := m.users[1]
	self.HasCertificate = true
	m.users[1] = self
	if !userMenuPerms(m, 1).register {
		t.Fatal("an unregistered user with a certificate and RegisterSelf must be able to register")
	}
	self.UserID = 42
	m.users[1] = self
	if userMenuPerms(m, 1).register {
		t.Fatal("an already registered user must not register again")
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
	if !u.kick || !u.ban || !u.mute || !u.move || !u.priority {
		t.Fatalf("admin user menu = %+v", u)
	}
	if u.register {
		t.Fatal("registering a user without a certificate must be disabled")
	}
	peer := m.users[7]
	peer.HasCertificate = true
	m.users[7] = peer
	if !userMenuPerms(m, 7).register {
		t.Fatal("an admin must be able to register a user with a certificate")
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

// TestSuperUserHasAllPermissions: the server answers the SuperUser's
// permission queries only for the root channel.
func TestSuperUserHasAllPermissions(t *testing.T) {
	all := p(gumble.PermissionWrite, gumble.PermissionEnter, gumble.PermissionMuteDeafen,
		gumble.PermissionMove, gumble.PermissionMakeChannel, gumble.PermissionKick,
		gumble.PermissionBan, gumble.PermissionRegister)
	m := permModel(all, nil) // sub-channel permissions never arrive
	m.users[1] = bus.UserInfo{Session: 1, Name: "SuperUser", Channel: 0}
	c := channelMenuPerms(m, 5)
	if !c.edit || !c.remove || !c.acl || !c.createSub || !c.join {
		t.Fatalf("SuperUser channel menu = %+v, want everything", c)
	}
	if u := userMenuPerms(m, 7); !u.mute || !u.move || !u.kick {
		t.Fatalf("SuperUser user menu = %+v", u)
	}
	su := m.users[1]
	su.HasCertificate = true
	m.users[1] = su
	if userMenuPerms(m, 1).register {
		t.Fatal("the SuperUser is already registered")
	}

	// Someone merely named like that without root write is not trusted.
	m2 := permModel(p(gumble.PermissionEnter), nil)
	m2.users[1] = bus.UserInfo{Session: 1, Name: "SuperUser", Channel: 0}
	if channelMenuPerms(m2, 5).edit {
		t.Fatal("name alone must not grant permissions")
	}
}
