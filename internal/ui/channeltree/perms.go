package channeltree

import "io.github/murmel/internal/protocol/gumble"

// Context menu permissions, evaluated like the official client: channel
// actions against the local user's permissions in that channel; kick, ban
// and registering users against the root channel; server mute, deafen,
// priority speaker and move against the target user's channel. Unknown
// permissions (not yet answered by the server) count as not granted.

// superUserName is reserved by Mumble servers for the server
// administrator; nobody else can log in under it.
const superUserName = "SuperUser"

// channelPerm returns the local user's permissions in channel id and
// whether they are known. The SuperUser bypasses all ACLs, so the server
// never answers its permission queries for channels other than the root;
// like the official client, its root permissions apply everywhere.
func (m *treeModel) channelPerm(id uint32) (gumble.Permission, bool) {
	if n, ok := m.channels[id]; ok && n.info.Permissions != nil {
		return gumble.Permission(*n.info.Permissions), true
	}
	if m.isSuperUser() {
		return m.rootPerm(), true
	}
	return 0, false
}

func (m *treeModel) rootPerm() gumble.Permission {
	if n, ok := m.channels[0]; ok && n.info.Permissions != nil {
		return gumble.Permission(*n.info.Permissions)
	}
	return 0
}

// isSuperUser reports whether the local user is the server's SuperUser.
func (m *treeModel) isSuperUser() bool {
	self, ok := m.users[m.self]
	return ok && self.Name == superUserName && self.UserID == 0 &&
		m.rootPerm().Has(gumble.PermissionWrite)
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
	join, createSub, edit, remove bool
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
		// Registering binds the account to the client certificate: not
		// possible without one, nor for the (already registered) SuperUser.
		return userMenuState{
			register: u.UserID == 0 && u.HasCertificate && !m.isSuperUser() &&
				m.allowed(0, gumble.PermissionRegisterSelf),
			stats: true,
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
		register: u.UserID == 0 && u.HasCertificate && m.allowed(0, gumble.PermissionRegister),
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

// Dragging users into channels follows the server's checks for a move
// (murmur's UserState handler):
//
//  1. moving someone else needs Move in that user's current channel;
//  2. in the target channel, Move for the one who moves, or Enter for the
//     one who is moved;
//  3. the target channel must not be full, unless the mover has Write
//     there.
//
// The Enter permission of another user is only known to the server, so a
// target without Move is a "maybe": the drop is allowed and the server
// decides (a refusal shows up in the chat). Dragging oneself joins the
// target channel, which needs no Move at all.

// dropVerdict is how a drop of a user on a channel will likely end.
type dropVerdict uint8

const (
	dropNo    dropVerdict = iota // the server will refuse, or nothing would change
	dropMaybe                    // depends on what the server knows
	dropYes                      // the local permissions allow it
)

// canDragUser reports whether session's drag handle is offered: always for
// oneself (to join a channel), for others with Move in their channel.
func canDragUser(m *treeModel, session uint32) bool {
	u, ok := m.users[session]
	if !ok {
		return false
	}
	return session == m.self || m.allowed(u.Channel, gumble.PermissionMove)
}

// dropTarget judges dropping session on channel.
func dropTarget(m *treeModel, session, channel uint32) dropVerdict {
	u, ok := m.users[session]
	n, ok2 := m.channels[channel]
	if !ok || !ok2 || u.Channel == channel || !canDragUser(m, session) {
		return dropNo
	}
	p, known := m.channelPerm(channel)
	if n.info.MaxUsers > 0 && len(n.users) >= int(n.info.MaxUsers) {
		if known && !p.Has(gumble.PermissionWrite) {
			return dropNo // full, and no Write to override the limit
		}
		if !known {
			return dropMaybe
		}
	}
	switch {
	case !known:
		return dropMaybe
	case p.Has(gumble.PermissionMove):
		return dropYes
	case session == m.self && p.Has(gumble.PermissionEnter):
		return dropYes
	default:
		// Someone else may still have Enter there; for oneself, a
		// password channel asks for the password after the refusal.
		return dropMaybe
	}
}
