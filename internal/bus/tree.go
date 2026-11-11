package bus

// UI-originated tree events. These are the outbound half of the
// channel-tree contract: the tree widget renders state from the events in
// events.go and, when the user acts, emits the events below. A controller
// (internal/ui/channeltree/controller.go) translates the *Requested commands
// into protocol calls; other views consume the selection events.
//
// Like every bus event they are value-typed snapshots; handlers run on the
// GLib main loop in emit order.

// ChannelJoinRequested is emitted when the user activates a channel row
// (double-click or Enter in the wide tree, the Join button in the narrow
// drill-down). A controller moves the local user into the channel.
type ChannelJoinRequested struct {
	// ChannelID is the ID of the channel to join.
	ChannelID uint32
}

// ChatTargetSelected is emitted when the tree selection changes. Exactly one
// of ChannelID / Session is non-zero: selecting a channel row targets the
// channel, selecting a user row targets a private conversation with that
// user. The chat composer binds its target to this event.
type ChatTargetSelected struct {
	// ChannelID is the selected channel ID (0 when a user is selected).
	ChannelID uint32
	// Session is the selected user's session ID (0 when a channel is
	// selected).
	Session uint32
	// Name is the target's display name at selection time (may go stale).
	Name string
}

// SelfMuteToggle is emitted when the user clicks the status-bar mute button
// or a self-mute menu action. The controller translates it into
// client.Self.SetSelfMuted(Muted).
type SelfMuteToggle struct {
	Muted bool
}

// SelfDeafenToggle is the same for the deafen button/action.
type SelfDeafenToggle struct {
	Deafened bool
}

// UserMoveRequested asks the controller to move Session into ChannelID
// when ToChannel is set (the root channel is ID 0), otherwise into the
// local user's current channel.
type UserMoveRequested struct {
	Session   uint32
	ChannelID uint32
	ToChannel bool
}

// UserMuteRequested asks the controller to server-mute or un-mute a user.
type UserMuteRequested struct {
	Session uint32
	Muted   bool
}

// UserDeafenRequested asks the controller to server-deafen or un-deafen.
type UserDeafenRequested struct {
	Session  uint32
	Deafened bool
}

// UserPriorityRequested toggles a user's priority-speaker status.
type UserPriorityRequested struct {
	Session  uint32
	Priority bool
}

// UserRegisterRequested asks the controller to register an unregistered user.
type UserRegisterRequested struct {
	Session uint32
}

// UserStatsRequested asks the controller to fetch and display stats for a user.
type UserStatsRequested struct {
	Session uint32
}

// UserVolumeChanged carries a local (client-side) volume adjustment for one
// remote user. The audio pipeline scales decoded PCM for that session.
// Volume is in [0.0, 2.0], where 1.0 = 100%.
type UserVolumeChanged struct {
	Session uint32
	Volume  float64
}

// ChannelStepRequested asks to join the channel Delta places away from
// the local user's in the channel tree's order (-1 previous, +1 next),
// skipping channels the user may not enter.
type ChannelStepRequested struct {
	Delta int
}

// UserLocalAudioRequested asks to set the local volume (0..2, 1 = 100 %)
// and the local mute of one remote user, for oneself only; the setting is
// remembered for that user on this server.
type UserLocalAudioRequested struct {
	Session uint32
	Volume  float64
	Muted   bool
}

// UserLocalAudioChanged reports a user's local volume and mute, after a
// request or when a remembered setting is applied to a user who appeared.
type UserLocalAudioChanged struct {
	Session uint32
	Volume  float64
	Muted   bool
}

// UserKickRequested asks the controller to kick a user from the server.
type UserKickRequested struct {
	Session uint32
	Reason  string
}

// UserBanRequested asks the controller to ban a user from the server.
type UserBanRequested struct {
	Session uint32
	Reason  string
}

// ChannelCreateRequested asks the controller to create a sub-channel.
type ChannelCreateRequested struct {
	ParentID  uint32
	Name      string
	Temporary bool
}

// ChannelDeleteRequested asks the controller to remove a channel and its
// sub-channels from the server tree.
type ChannelDeleteRequested struct {
	ChannelID uint32
}

// ChannelEditRequested asks the controller to change a channel's
// properties. Nil fields stay unchanged.
type ChannelEditRequested struct {
	ChannelID   uint32
	Name        *string
	Description *string
	Position    *int32
	MaxUsers    *uint32
}

// AccessTokenAdded gives the server an access token (a channel password)
// and, if ChannelID is set, retries joining that channel with it.
type AccessTokenAdded struct {
	Token     string
	ChannelID uint32
}

// ACLRequested asks the controller to fetch a channel's ACL from the server.
type ACLRequested struct {
	ChannelID uint32
}

// ACLSaveRequested asks the controller to write a modified ACL to the server.
type ACLSaveRequested struct {
	ChannelID uint32
	Inherits  bool
	Groups    []ACLGroupInfo
	Rules     []ACLRuleInfo
}

// DisconnectRequested asks the controller to leave the server (user action).
type DisconnectRequested struct{}

// ChannelPermissionRequested asks the controller to query the local user's
// permissions in a channel; the answer arrives as ChannelUpdated.
type ChannelPermissionRequested struct {
	ChannelID uint32
}

// UserTextureRequested asks the controller to fetch a user's avatar (the
// server sent only its hash).
type UserTextureRequested struct {
	Session uint32
}

// ChannelDescriptionRequested asks the controller to fetch a channel's
// long description (the server sent only its hash).
type ChannelDescriptionRequested struct {
	ChannelID uint32
}

// BanListRequested asks the controller to request the server ban list.
type BanListRequested struct{}

// UserListRequested asks the controller to request the registered user list.
type UserListRequested struct{}

// BanListWriteRequested asks the controller to write the given entries back to
// the server as the new ban list (replaces the full list).
type BanListWriteRequested struct {
	Entries []BanEntry
}

// UserUnregisterRequested asks the controller to deregister a registered user.
type UserUnregisterRequested struct {
	UserID uint32
}

func (ChannelJoinRequested) isBusEvent()        {}
func (ChatTargetSelected) isBusEvent()          {}
func (SelfMuteToggle) isBusEvent()              {}
func (SelfDeafenToggle) isBusEvent()            {}
func (UserMoveRequested) isBusEvent()           {}
func (UserMuteRequested) isBusEvent()           {}
func (UserDeafenRequested) isBusEvent()         {}
func (UserPriorityRequested) isBusEvent()       {}
func (UserRegisterRequested) isBusEvent()       {}
func (UserStatsRequested) isBusEvent()          {}
func (UserVolumeChanged) isBusEvent()           {}
func (UserKickRequested) isBusEvent()           {}
func (UserBanRequested) isBusEvent()            {}
func (ChannelCreateRequested) isBusEvent()      {}
func (ChannelDeleteRequested) isBusEvent()      {}
func (ChannelEditRequested) isBusEvent()        {}
func (AccessTokenAdded) isBusEvent()            {}
func (ACLRequested) isBusEvent()                {}
func (ACLSaveRequested) isBusEvent()            {}
func (BanListRequested) isBusEvent()            {}
func (DisconnectRequested) isBusEvent()         {}
func (ChannelDescriptionRequested) isBusEvent() {}
func (UserTextureRequested) isBusEvent()        {}
func (ChannelPermissionRequested) isBusEvent()  {}
func (UserListRequested) isBusEvent()           {}
func (BanListWriteRequested) isBusEvent()       {}
func (UserUnregisterRequested) isBusEvent()     {}
func (UserLocalAudioRequested) isBusEvent()     {}
func (ChannelStepRequested) isBusEvent()        {}
func (UserLocalAudioChanged) isBusEvent()       {}
