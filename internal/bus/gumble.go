package bus

import (
	"errors"
	"net"
	"sort"

	"io.github/murmel/internal/protocol/gumble"
)

// attachGumbleBridge attaches a translating listener to client.Config so
// every fork event becomes a bus event. Each listener method only copies
// immutable data out of the fork's structures and performs a single
// non-blocking Emit; the listeners run on the network read loop and must
// never block, send on the network, or call client.Do.
//
// The fork mutates its channel/user structures only on the same read loop
// that invokes these listeners, so reading their exported fields here is
// race-free. The Detacher is returned as a plain undo function for Bus.Close.
func attachGumbleBridge(b *Bus, client *gumble.Client) func() {
	bridge := &gumbleBridge{bus: b}
	detacher := client.Config.Attach(bridge)
	return detacher.Detach
}

type gumbleBridge struct {
	bus *Bus
}

// OnConnect seeds the whole world state: the fork delivers no channel/user
// events before the initial sync completes, so the tree is snapshotted here
// and carried by the Connected event.
func (g *gumbleBridge) OnConnect(e *gumble.ConnectEvent) {
	if e == nil || e.Client == nil {
		return
	}
	ev := Connected{
		Welcome:    derefOr(e.WelcomeMessage, ""),
		MaxBitrate: copyIntPtr(e.MaximumBitrate),
	}
	if e.Client.Self != nil {
		ev.SelfSession = e.Client.Self.Session
	}
	ev.Channels = make([]ChannelInfo, 0, len(e.Client.Channels))
	for _, ch := range e.Client.Channels {
		ev.Channels = append(ev.Channels, copyChannel(ch))
	}
	sort.Slice(ev.Channels, func(i, j int) bool {
		return ev.Channels[i].ID < ev.Channels[j].ID
	})
	ev.Users = make([]UserInfo, 0, len(e.Client.Users))
	for _, u := range e.Client.Users {
		ev.Users = append(ev.Users, copyUser(u))
	}
	g.bus.Emit(ev)
}

func (g *gumbleBridge) OnDisconnect(e *gumble.DisconnectEvent) {
	if e == nil {
		return
	}
	g.bus.Emit(Disconnected{
		Reason: disconnectReason(e.Type),
		Text:   e.String,
	})
}

func (g *gumbleBridge) OnTextMessage(e *gumble.TextMessageEvent) {
	if e == nil {
		return
	}
	ev := TextMessage{Message: e.Message}
	if e.Sender != nil {
		ev.Sender = e.Sender.Session
	}
	ev.UserSessions = userSessions(e.Users)
	ev.ChannelIDs = channelIDs(e.Channels)
	ev.TreeIDs = channelIDs(e.Trees)
	g.bus.Emit(ev)
}

func (g *gumbleBridge) OnUserChange(e *gumble.UserChangeEvent) {
	if e == nil {
		return
	}
	switch {
	case e.Type.Has(gumble.UserChangeDisconnected):
		ev := UserRemoved{
			Reason: e.String,
			Kicked: e.Type.Has(gumble.UserChangeKicked),
			Banned: e.Type.Has(gumble.UserChangeBanned),
		}
		if e.User != nil {
			ev.Session = e.User.Session
		}
		if e.Actor != nil {
			ev.Actor = e.Actor.Session
		}
		g.bus.Emit(ev)
	case e.Type.Has(gumble.UserChangeConnected):
		g.bus.Emit(UserAdded{User: copyUser(e.User)})
	default:
		g.bus.Emit(UserUpdated{User: copyUser(e.User), Changed: userChanges(e.Type)})
	}
}

func (g *gumbleBridge) OnChannelChange(e *gumble.ChannelChangeEvent) {
	if e == nil {
		return
	}
	switch {
	case e.Type.Has(gumble.ChannelChangeRemoved):
		var id uint32
		if e.Channel != nil {
			id = e.Channel.ID
		}
		g.bus.Emit(ChannelRemoved{ID: id})
	case e.Type.Has(gumble.ChannelChangeCreated):
		g.bus.Emit(ChannelAdded{Channel: copyChannel(e.Channel)})
	default:
		g.bus.Emit(ChannelUpdated{Channel: copyChannel(e.Channel), Changed: channelChanges(e.Type)})
	}
}

func (g *gumbleBridge) OnPermissionDenied(e *gumble.PermissionDeniedEvent) {
	if e == nil {
		return
	}
	ev := PermissionDenied{
		Reason:     denyReason(e.Type),
		Text:       e.String,
		Permission: int(e.Permission),
	}
	if e.Channel != nil {
		ev.ChannelID = e.Channel.ID
	}
	if e.User != nil {
		ev.Session = e.User.Session
	}
	g.bus.Emit(ev)
}

func (g *gumbleBridge) OnServerConfig(e *gumble.ServerConfigEvent) {
	if e == nil {
		return
	}
	g.bus.Emit(ServerConfigUpdated{
		Welcome:               copyStrPtr(e.WelcomeMessage),
		MaxBitrate:            copyIntPtr(e.MaximumBitrate),
		AllowHTML:             copyBoolPtr(e.AllowHTML),
		MaxMessageLength:      copyIntPtr(e.MaximumMessageLength),
		MaxImageMessageLength: copyIntPtr(e.MaximumImageMessageLength),
		MaxUsers:              copyIntPtr(e.MaximumUsers),
		CodecAlpha:            copyInt32Ptr(e.CodecAlpha),
		CodecBeta:             copyInt32Ptr(e.CodecBeta),
		CodecPreferAlpha:      copyBoolPtr(e.CodecPreferAlpha),
		CodecOpus:             copyBoolPtr(e.CodecOpus),
	})
}

func (g *gumbleBridge) OnBanList(e *gumble.BanListEvent) {
	if e == nil {
		return
	}
	ev := BanListFetched{Entries: make([]BanEntry, 0, len(e.BanList))}
	for _, ban := range e.BanList {
		ev.Entries = append(ev.Entries, BanEntry{
			Address:  copyIP(ban.Address),
			Mask:     copyIPMask(ban.Mask),
			Name:     ban.Name,
			Hash:     ban.Hash,
			Reason:   ban.Reason,
			Start:    ban.Start,
			Duration: ban.Duration,
		})
	}
	g.bus.Emit(ev)
}

func (g *gumbleBridge) OnUserList(e *gumble.UserListEvent) {
	if e == nil {
		return
	}
	ev := UserListFetched{Users: make([]RegisteredUserInfo, 0, len(e.UserList))}
	for _, u := range e.UserList {
		info := RegisteredUserInfo{
			UserID:   u.UserID,
			Name:     u.Name,
			LastSeen: u.LastSeen,
		}
		if u.LastChannel != nil {
			info.LastChannel = u.LastChannel.ID
		}
		ev.Users = append(ev.Users, info)
	}
	g.bus.Emit(ev)
}

func (g *gumbleBridge) OnACL(e *gumble.ACLEvent) {
	if e == nil || e.ACL == nil || e.ACL.Channel == nil {
		return
	}
	acl := e.ACL
	ev := ACLFetched{
		ChannelID: acl.Channel.ID,
		Inherits:  acl.Inherits,
		Rules:     make([]ACLRuleInfo, 0, len(acl.Rules)),
		Groups:    make([]ACLGroupInfo, 0, len(acl.Groups)),
	}
	for _, rule := range acl.Rules {
		r := ACLRuleInfo{
			AppliesCurrent:  rule.AppliesCurrent,
			AppliesChildren: rule.AppliesChildren,
			Inherited:       rule.Inherited,
			Granted:         int(rule.Granted),
			Denied:          int(rule.Denied),
		}
		if rule.User != nil {
			r.UserID = rule.User.UserID
			r.UserName = rule.User.Name
		}
		if rule.Group != nil {
			r.GroupName = rule.Group.Name
		}
		ev.Rules = append(ev.Rules, r)
	}
	for _, group := range acl.Groups {
		gi := ACLGroupInfo{
			Name:           group.Name,
			Inherited:      group.Inherited,
			InheritUsers:   group.InheritUsers,
			Inheritable:    group.Inheritable,
			UsersAdd:       copyACLUsers(group.UsersAdd),
			UsersRemove:    copyACLUsers(group.UsersRemove),
			UsersInherited: copyACLUsers(group.UsersInherited),
		}
		ev.Groups = append(ev.Groups, gi)
	}
	g.bus.Emit(ev)
}

// ContextActionChange has no bus event.
func (g *gumbleBridge) OnContextActionChange(*gumble.ContextActionChangeEvent) {}

// TranslateReject converts a gumble dial error into a ConnectError event.
// The fork reports login rejections through the error returned by Dial (a
// *gumble.RejectError), not through a listener; the application calls this
// helper and Emits the result so UI error handling subscribes uniformly.
// ok is false when err carries no rejection reason.
func TranslateReject(err error) (ev ConnectError, ok bool) {
	var ptr *gumble.RejectError
	if errors.As(err, &ptr) && ptr != nil {
		return ConnectError{Reason: rejectReason(ptr.Type), Text: ptr.Reason}, true
	}
	var val gumble.RejectError
	if errors.As(err, &val) {
		return ConnectError{Reason: rejectReason(val.Type), Text: val.Reason}, true
	}
	return ConnectError{}, false
}

func disconnectReason(t gumble.DisconnectType) DisconnectReason {
	switch t {
	case gumble.DisconnectUser:
		return DisconnectUser
	case gumble.DisconnectKicked:
		return DisconnectKicked
	case gumble.DisconnectBanned:
		return DisconnectBanned
	default:
		return DisconnectError
	}
}

func rejectReason(t gumble.RejectType) RejectReason {
	switch t {
	case gumble.RejectVersion:
		return RejectVersion
	case gumble.RejectUserName:
		return RejectUserName
	case gumble.RejectUserCredentials:
		return RejectUserCredentials
	case gumble.RejectServerPassword:
		return RejectServerPassword
	case gumble.RejectUsernameInUse:
		return RejectUsernameInUse
	case gumble.RejectServerFull:
		return RejectServerFull
	case gumble.RejectNoCertificate:
		return RejectNoCertificate
	case gumble.RejectAuthenticatorFail:
		return RejectAuthenticatorFail
	default:
		return RejectNone
	}
}

// denyReason translates by named constant, not numeric cast: the two enums
// only coincide by construction today.
func denyReason(t gumble.PermissionDeniedType) DenyReason {
	switch t {
	case gumble.PermissionDeniedPermission:
		return DenyPermission
	case gumble.PermissionDeniedSuperUser:
		return DenySuperUser
	case gumble.PermissionDeniedInvalidChannelName:
		return DenyInvalidChannelName
	case gumble.PermissionDeniedTextTooLong:
		return DenyTextTooLong
	case gumble.PermissionDeniedTemporaryChannel:
		return DenyTemporaryChannel
	case gumble.PermissionDeniedMissingCertificate:
		return DenyMissingCertificate
	case gumble.PermissionDeniedInvalidUserName:
		return DenyInvalidUserName
	case gumble.PermissionDeniedChannelFull:
		return DenyChannelFull
	case gumble.PermissionDeniedNestingLimit:
		return DenyNestingLimit
	case gumble.PermissionDeniedChannelCountLimit:
		return DenyChannelCountLimit
	default:
		return DenyText
	}
}

// userChanges and channelChanges translate by bit, not cast: the fork's
// bitmask layouts differ from the bus's (the bus drops the lifecycle bits it
// models as separate Added/Removed events).
func userChanges(t gumble.UserChangeType) (c UserChanges) {
	if t.Has(gumble.UserChangeName) {
		c |= UserChangeName
	}
	if t.Has(gumble.UserChangeChannel) {
		c |= UserChangeChannel
	}
	if t.Has(gumble.UserChangeComment) {
		c |= UserChangeComment
	}
	if t.Has(gumble.UserChangeAudio) {
		c |= UserChangeAudio
	}
	if t.Has(gumble.UserChangeRegistered) {
		c |= UserChangeRegistered
	}
	if t.Has(gumble.UserChangeUnregistered) {
		c |= UserChangeUnregistered
	}
	if t.Has(gumble.UserChangePrioritySpeaker) {
		c |= UserChangePrioritySpeaker
	}
	if t.Has(gumble.UserChangeRecording) {
		c |= UserChangeRecording
	}
	if t.Has(gumble.UserChangeTexture) {
		c |= UserChangeTexture
	}
	if t.Has(gumble.UserChangeStats) {
		c |= UserChangeStats
	}
	return c
}

func channelChanges(t gumble.ChannelChangeType) (c ChannelChanges) {
	if t.Has(gumble.ChannelChangeMoved) {
		c |= ChannelChangeMoved
	}
	if t.Has(gumble.ChannelChangeName) {
		c |= ChannelChangeName
	}
	if t.Has(gumble.ChannelChangeLinks) {
		c |= ChannelChangeLinks
	}
	if t.Has(gumble.ChannelChangeDescription) {
		c |= ChannelChangeDescription
	}
	if t.Has(gumble.ChannelChangePosition) {
		c |= ChannelChangePosition
	}
	if t.Has(gumble.ChannelChangePermission) {
		c |= ChannelChangePermission
	}
	if t.Has(gumble.ChannelChangeMaxUsers) {
		c |= ChannelChangeMaxUsers
	}
	return c
}

func copyUser(u *gumble.User) UserInfo {
	if u == nil {
		return UserInfo{}
	}
	info := UserInfo{
		Session:         u.Session,
		UserID:          u.UserID,
		Name:            u.Name,
		Muted:           u.Muted,
		Deafened:        u.Deafened,
		Suppressed:      u.Suppressed,
		SelfMuted:       u.SelfMuted,
		SelfDeafened:    u.SelfDeafened,
		PrioritySpeaker: u.PrioritySpeaker,
		Recording:       u.Recording,
	}
	if u.Channel != nil {
		info.Channel = u.Channel.ID
	}
	return info
}

func copyChannel(c *gumble.Channel) ChannelInfo {
	if c == nil {
		return ChannelInfo{}
	}
	info := ChannelInfo{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		Position:    c.Position,
		MaxUsers:    c.MaxUsers,
		Temporary:   c.Temporary,
	}
	if c.Parent != nil {
		info.ParentID = c.Parent.ID
	}
	if p := c.Permission(); p != nil {
		v := int(*p)
		info.Permissions = &v
	}
	if n := len(c.Links); n > 0 {
		info.Links = make([]uint32, 0, n)
		for id := range c.Links {
			info.Links = append(info.Links, id)
		}
		sort.Slice(info.Links, func(i, j int) bool { return info.Links[i] < info.Links[j] })
	}
	return info
}

func userSessions(users []*gumble.User) []uint32 {
	if len(users) == 0 {
		return nil
	}
	out := make([]uint32, 0, len(users))
	for _, u := range users {
		if u != nil {
			out = append(out, u.Session)
		}
	}
	return out
}

func channelIDs(channels []*gumble.Channel) []uint32 {
	if len(channels) == 0 {
		return nil
	}
	out := make([]uint32, 0, len(channels))
	for _, c := range channels {
		if c != nil {
			out = append(out, c.ID)
		}
	}
	return out
}

func derefOr(s *string, def string) string {
	if s == nil {
		return def
	}
	return *s
}

func copyStrPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func copyIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func copyInt32Ptr(p *int32) *int32 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func copyBoolPtr(p *bool) *bool {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func copyIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	out := make(net.IP, len(ip))
	copy(out, ip)
	return out
}

func copyIPMask(mask net.IPMask) net.IPMask {
	if mask == nil {
		return nil
	}
	out := make(net.IPMask, len(mask))
	copy(out, mask)
	return out
}

func copyACLUsers(m map[uint32]*gumble.ACLUser) []ACLUserInfo {
	if len(m) == 0 {
		return nil
	}
	out := make([]ACLUserInfo, 0, len(m))
	for _, u := range m {
		out = append(out, ACLUserInfo{UserID: u.UserID, Name: u.Name})
	}
	return out
}
