package channeltree

import "io.github/murmel/internal/protocol/gumble"

// Context menu permissions, evaluated like the official client: channel
// actions against the local user's permissions in that channel; kick, ban
// and registering users against the root channel; server mute, deafen,
// priority speaker and move against the target user's channel. Unknown
// permissions (not yet answered by the server) count as not granted.

// channelPerm returns the local user's permissions in channel id and
// whether the server has told us.
func (m *treeModel) channelPerm(id uint32) (gumble.Permission, bool) {
	n, ok := m.channels[id]
	if !ok || n.info.Permissions == nil {
		return 0, false
	}
	return gumble.Permission(*n.info.Permissions), true
}

func (m *treeModel) allowed(id uint32, perms ...gumble.Permission) bool {
	p, ok := m.channelPerm(id)
	if !ok {
		return false
	}
	for _, want := range perms {
		if p.Has(want) {
			return true
		}
	}
	return false
}

// channelMenuState is which channel menu entries are enabled.
type channelMenuState struct {
	join, createSub, edit, remove, acl bool
}

func channelMenuPerms(m *treeModel, id uint32) channelMenuState {
	here := false
	if self, ok := m.users[m.self]; ok {
		here = self.Channel == id
	}
	write := m.allowed(id, gumble.PermissionWrite)
	return channelMenuState{
		join:      !here && m.allowed(id, gumble.PermissionEnter),
		createSub: m.allowed(id, gumble.PermissionMakeChannel, gumble.PermissionMakeTemporaryChannel),
		edit:      write,
		remove:    write && id != 0, // the root channel cannot be removed
		acl:       write,
	}
}

// userMenuState is which user menu entries are enabled.
type userMenuState struct {
	message, move, mute, deafen, priority, kick, ban, register, stats bool
}

func userMenuPerms(m *treeModel, session uint32) userMenuState {
	u, ok := m.users[session]
	if !ok {
		return userMenuState{}
	}
	if session == m.self {
		return userMenuState{
			register: u.UserID == 0 && m.allowed(0, gumble.PermissionRegisterSelf),
			stats:    true,
		}
	}
	inMyChannel := false
	if self, ok := m.users[m.self]; ok {
		inMyChannel = self.Channel == u.Channel
	}
	muteDeafen := m.allowed(u.Channel, gumble.PermissionMuteDeafen)
	return userMenuState{
		message:  true,
		move:     !inMyChannel && m.allowed(u.Channel, gumble.PermissionMove),
		mute:     muteDeafen,
		deafen:   muteDeafen,
		priority: muteDeafen,
		kick:     m.allowed(0, gumble.PermissionKick),
		ban:      m.allowed(0, gumble.PermissionBan),
		register: u.UserID == 0 && m.allowed(0, gumble.PermissionRegister),
		stats:    true,
	}
}

// permissionChannels lists the channels whose permissions a menu needs, so
// the tree can ask the server for the unknown ones.
func permissionChannels(m *treeModel, channel uint32, session uint32) []uint32 {
	ids := []uint32{0}
	if session != 0 {
		if u, ok := m.users[session]; ok {
			channel = u.Channel
		}
	}
	if channel != 0 {
		ids = append(ids, channel)
	}
	return ids
}
