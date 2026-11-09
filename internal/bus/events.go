// Package bus is the mandatory bridge between gumble's blocking listener
// callbacks and the GTK main loop.
//
// gumble executes EventListener methods synchronously on its network read
// routine (internal/protocol/gumble/event.go: "Listener methods […] block
// network reads from happening until all handlers for an event are called").
// The bus therefore never does real work inside those callbacks: its listener
// implementations copy immutable event data (IDs, names, snapshots) into
// value-typed event structs and perform a single non-blocking send into a
// buffered channel. One pump goroutine drains that channel, applies the event
// to the state store, and re-dispatches it to subscriber handlers on the
// main loop the UI installs with SetMainLoop (glib.IdleAdd), so handlers
// always run on the GLib main loop.
//
// Backpressure policy: if the channel is full the event is dropped and a
// dropped-event counter is incremented — the network loop is never blocked.
// The counter is surfaced in the debug log and via Bus.Dropped.
//
// The package imports nothing from gotk4, so the audio and protocol layers
// can use it and stay GTK-free; TestNoGotk4Imports and the repository's
// TestLayerBoundary enforce this.
package bus

import (
	"net"
	"time"
)

// Event is the sealed interface implemented by every bus event type.
// Handlers subscribe to exactly one concrete event type via Subscribe[T].
type Event interface {
	isBusEvent()
}

// ConnectionStarting is emitted on the application bus when the controller
// starts to connect: Reconnect is true for an automatic reconnect after the
// connection dropped (the session continues), false for a new connection
// the user asked for.
type ConnectionStarting struct {
	Reconnect bool
}

// Connected is emitted once the server connection is established and the
// initial state sync has completed (gumble OnConnect). The fork does not
// deliver channel/user events for the initial sync, so the bridge snapshots
// the client's channel/user maps at OnConnect time and carries them here:
// subscribers build the initial tree from this event (or from Bus.State),
// then maintain it via the Added/Updated/Removed events.
type Connected struct {
	// SelfSession is the session ID of the local user (0 if unknown).
	SelfSession uint32
	// Welcome is the server's welcome message ("" if it sent none).
	Welcome string
	// MaxBitrate is the server's maximum bitrate in bit/s (nil if unsent).
	MaxBitrate *int
	// Channels is the full channel tree at connect time.
	Channels []ChannelInfo
	// Users is every user on the server at connect time.
	Users []UserInfo
}

// DisconnectReason categorizes why an established connection ended.
type DisconnectReason int

// Disconnect reasons.
const (
	// DisconnectError means the connection was lost (network error).
	DisconnectError DisconnectReason = iota
	// DisconnectUser means Disconnect was called locally.
	DisconnectUser
	// DisconnectKicked means the local user was kicked by an operator.
	DisconnectKicked
	// DisconnectBanned means the local user was banned by an operator.
	DisconnectBanned
)

// String returns a short stable name for the reason.
func (r DisconnectReason) String() string {
	switch r {
	case DisconnectUser:
		return "user"
	case DisconnectKicked:
		return "kicked"
	case DisconnectBanned:
		return "banned"
	default:
		return "error"
	}
}

// Disconnected is emitted when an established connection ended.
type Disconnected struct {
	Reason DisconnectReason
	// Text is a human-readable detail string ("" if none).
	Text string
}

// RejectReason categorizes a server rejection during login.
type RejectReason int

// Rejection reasons (mirroring the fork's gumble.RejectType).
const (
	RejectNone RejectReason = iota
	RejectVersion
	RejectUserName
	RejectUserCredentials
	RejectServerPassword
	RejectUsernameInUse
	RejectServerFull
	RejectNoCertificate
	RejectAuthenticatorFail
)

// ConnectError is emitted by the application layer when dialing a server
// failed. gumble reports login rejections through the Dial error, not through
// a listener, so the app translates that error into this event. The bus keeps
// it in the event set so UI error handling subscribes uniformly.
type ConnectError struct {
	Reason RejectReason
	// Text is the server-provided reason string ("" if none).
	Text string
}

// ChannelInfo is an immutable snapshot of a channel's state.
type ChannelInfo struct {
	ID   uint32
	Name string
	// ParentID is 0 for the root channel (root's parent is none).
	ParentID uint32
	// Links lists the IDs of linked channels, sorted; nil if none.
	Links []uint32
	// Description is "" while only its hash is known.
	Description string
	// DescriptionPending: the server announced a description (by hash)
	// that has not been fetched yet (ChannelDescriptionRequested).
	DescriptionPending bool
	Position           int32
	MaxUsers           uint32
	Temporary          bool
	// Permissions is the local user's permission bitmask in this channel,
	// using the fork's gumble.Permission bit values; nil while the server
	// has not answered a permission query for the channel (unknown, not
	// insufficient).
	Permissions *int
}

// ChannelAdded is emitted when a channel appears in the server tree
// (gumble ChannelChangeCreated).
type ChannelAdded struct {
	Channel ChannelInfo
}

// ChannelRemoved is emitted when a channel left the server tree.
type ChannelRemoved struct {
	ID uint32
}

// ChannelChanges is a bitmask of what changed in a ChannelUpdated event.
type ChannelChanges int

// Channel change items.
const (
	ChannelChangeName ChannelChanges = 1 << iota
	ChannelChangeMoved
	ChannelChangeLinks
	ChannelChangeDescription
	ChannelChangePosition
	ChannelChangePermission
	ChannelChangeMaxUsers
)

// Has reports whether c contains all bits of other.
func (c ChannelChanges) Has(other ChannelChanges) bool { return c&other == other }

// ChannelUpdated is emitted when an already-known channel changed.
type ChannelUpdated struct {
	Channel ChannelInfo
	Changed ChannelChanges
}

// UserInfo is an immutable snapshot of a connected user's state.
type UserInfo struct {
	Session uint32
	// UserID is the registered user ID, 0 if unregistered.
	UserID          uint32
	Name            string
	Channel         uint32
	Muted           bool
	Deafened        bool
	Suppressed      bool
	SelfMuted       bool
	SelfDeafened    bool
	PrioritySpeaker bool
	Recording       bool
	// HasCertificate reports whether the user connected with a client
	// certificate (the server announced its hash); registering needs one.
	HasCertificate bool
	// Texture is the user's avatar image as uploaded (PNG/JPEG; very old
	// clients sent raw pixels, which are not shown). nil if none or not
	// fetched yet.
	Texture []byte
	// TexturePending: the server announced an avatar (by hash) that has
	// not been fetched yet (UserTextureRequested).
	TexturePending bool
}

// UserAdded is emitted when a user connected (gumble UserChangeConnected).
type UserAdded struct {
	User UserInfo
}

// UserRemoved is emitted when a user left the server.
type UserRemoved struct {
	// Session is the removed user's session ID.
	Session uint32
	// Actor is the acting user's session ID (0 if none/server).
	Actor uint32
	// Reason is the kick/ban reason string ("" if none).
	Reason string
	Kicked bool
	Banned bool
}

// UserChanges is a bitmask of what changed in a UserUpdated event.
type UserChanges int

// User change items.
const (
	UserChangeName UserChanges = 1 << iota
	UserChangeChannel
	UserChangeComment
	// UserChangeAudio covers any mute/deafen/suppress flag change.
	UserChangeAudio
	UserChangeRegistered
	UserChangeUnregistered
	UserChangePrioritySpeaker
	UserChangeRecording
	UserChangeTexture
	UserChangeStats
)

// Has reports whether u contains all bits of other.
func (u UserChanges) Has(other UserChanges) bool { return u&other == other }

// UserUpdated is emitted when an already-known user changed.
type UserUpdated struct {
	User    UserInfo
	Changed UserChanges
}

// TextMessage is a chat message received from the server.
type TextMessage struct {
	// Sender is the sending user's session ID (0 if none).
	Sender  uint32
	Message string
	// UserSessions lists direct recipient session IDs.
	UserSessions []uint32
	// ChannelIDs lists recipient channel IDs.
	ChannelIDs []uint32
	// TreeIDs lists recipient channel IDs whose subtrees received the
	// message.
	TreeIDs []uint32
}

// DenyReason categorizes why the server denied a permission or action
// (mirroring the fork's gumble.PermissionDeniedType values).
type DenyReason int

// Permission-denied reasons.
const (
	DenyText DenyReason = iota
	DenyPermission
	DenySuperUser
	DenyInvalidChannelName
	DenyTextTooLong
	DenyTemporaryChannel
	DenyMissingCertificate
	DenyInvalidUserName
	DenyChannelFull
	DenyNestingLimit
	DenyChannelCountLimit
)

// PermissionDenied is emitted when the server denied an action.
type PermissionDenied struct {
	Reason DenyReason
	// Text is a human-readable reason ("" if none).
	Text string
	// ChannelID is the affected channel (0 if none).
	ChannelID uint32
	// Session is the affected user (0 if none).
	Session uint32
	// Permission is the denied permission bit (0 if not applicable).
	Permission int
}

// ServerConfigUpdated is emitted when the server announced or changed its
// configuration. Nil pointer fields were not sent by the server.
type ServerConfigUpdated struct {
	Welcome               *string
	MaxBitrate            *int
	AllowHTML             *bool
	MaxMessageLength      *int
	MaxImageMessageLength *int
	MaxUsers              *int
	CodecAlpha            *int32
	CodecBeta             *int32
	CodecPreferAlpha      *bool
	CodecOpus             *bool
}

// VoiceActivity is a voice-activity-detection decision. It is emitted by the
// audio pipeline, not by the gumble bridge; the event type and
// subscription support land here so consumers can be written first.
type VoiceActivity struct {
	Session uint32
	Active  bool
}

// InputLevel reports the local microphone level for the voice meter. The
// voice engine emits it about 30 times per second while capturing.
type InputLevel struct {
	// LevelDB is the loudest frame RMS level in this window (dBFS, ≤ 0).
	LevelDB float64
	// ThresholdDB is the VAD open threshold currently in effect (dBFS).
	ThresholdDB float64
	// Transmitting is true while the VAD has the voice stream open.
	Transmitting bool
	// Clipping is true when a sample in this window hit full scale.
	Clipping bool
}

// ConnectionInfo describes the live connection for the status indicator.
// The connection controller emits it on connect and every few seconds while
// connected.
type ConnectionInfo struct {
	Address       string // host:port as dialed
	Username      string
	Channel       string // the local user's channel
	ServerVersion string
	Transport     string // "udp" or "tcp" (voice)
	Ping          time.Duration
	MaxBandwidth  int // server per-user limit in bit/s (0 = unknown)
}

// AudioDeviceProblem reports that an audio device stopped working while
// connected — e.g. an output that accepts no audio. The UI tells the user
// to pick another device.
type AudioDeviceProblem struct {
	// Output is true for the playback device, false for capture.
	Output bool
}

// ChatTargetChanged announces a new chat target: where newly composed
// messages go. Emitters are the channel tree selection, per-user
// "send message" actions, and the chat view's own target menu.
//
// Contract: exactly one field is non-zero, except the zero value which means
// "follow the local user's current channel" (the default target). A non-zero
// Session selects a private-message target; a non-zero ChannelID selects a
// fixed channel. Emitters must route the change through Emit so every
// consumer (banner, composer, future tree highlight) agrees on the target.
type ChatTargetChanged struct {
	// ChannelID is the selected channel target (0 if none).
	ChannelID uint32
	// Session is the selected private-message target user (0 if none).
	Session uint32
}

// LatencyStats carries round-trip and jitter measurements. It is emitted by
// the audio/connection metrics pipeline; the event type and
// subscription support land here so consumers can be written first.
type LatencyStats struct {
	// RTT is the current round-trip time estimate.
	RTT time.Duration
	// Jitter is the current RTT variation estimate.
	Jitter time.Duration
}

// ACLUserInfo is a registered user snapshot for use in ACL structures.
type ACLUserInfo struct {
	UserID uint32
	Name   string
}

// ACLGroupInfo is an immutable snapshot of one ACL group.
type ACLGroupInfo struct {
	Name           string
	Inherited      bool
	InheritUsers   bool
	Inheritable    bool
	UsersAdd       []ACLUserInfo
	UsersRemove    []ACLUserInfo
	UsersInherited []ACLUserInfo
}

// ACLRuleInfo is an immutable snapshot of one ACL rule.
type ACLRuleInfo struct {
	AppliesCurrent  bool
	AppliesChildren bool
	Inherited       bool
	Granted         int
	Denied          int
	UserID          uint32 // 0 if group rule
	UserName        string
	GroupName       string // "" if user rule
}

// ACLFetched is emitted when the server delivers a channel's ACL in response
// to a RequestACL call.
type ACLFetched struct {
	ChannelID uint32
	Inherits  bool
	Groups    []ACLGroupInfo
	Rules     []ACLRuleInfo
}

// BanEntry is an immutable snapshot of one server ban entry.
type BanEntry struct {
	Address  net.IP
	Mask     net.IPMask
	Name     string
	Hash     string
	Reason   string
	Start    time.Time
	Duration time.Duration
}

// BanListFetched is emitted when the server delivers the ban list in response
// to a RequestBanList call.
type BanListFetched struct {
	Entries []BanEntry
}

// RegisteredUserInfo is an immutable snapshot of one registered server user.
type RegisteredUserInfo struct {
	UserID      uint32
	Name        string
	LastSeen    time.Time
	LastChannel uint32
}

// UserListFetched is emitted when the server delivers the registered user list
// in response to a RequestUserList call.
type UserListFetched struct {
	Users []RegisteredUserInfo
}

func (Connected) isBusEvent()           {}
func (ConnectionStarting) isBusEvent()  {}
func (Disconnected) isBusEvent()        {}
func (ConnectError) isBusEvent()        {}
func (ChannelAdded) isBusEvent()        {}
func (ChannelRemoved) isBusEvent()      {}
func (ChannelUpdated) isBusEvent()      {}
func (UserAdded) isBusEvent()           {}
func (UserRemoved) isBusEvent()         {}
func (UserUpdated) isBusEvent()         {}
func (TextMessage) isBusEvent()         {}
func (PermissionDenied) isBusEvent()    {}
func (ServerConfigUpdated) isBusEvent() {}
func (VoiceActivity) isBusEvent()       {}
func (InputLevel) isBusEvent()          {}
func (AudioDeviceProblem) isBusEvent()  {}
func (ConnectionInfo) isBusEvent()      {}
func (LatencyStats) isBusEvent()        {}
func (ChatTargetChanged) isBusEvent()   {}
func (BanListFetched) isBusEvent()      {}
func (UserListFetched) isBusEvent()     {}
func (ACLFetched) isBusEvent()          {}

// UserStatsFetched carries a user's connection statistics, the server's
// reply to UserStatsRequested. Fields the server did not send (it limits
// details for non-admins) are zero.
type UserStatsFetched struct {
	Session uint32

	Version, Release, OS, OSVersion string
	Connected                       time.Time
	Idle                            time.Duration
	Bandwidth                       int // bit/s, 0 if unknown
	IP                              string
	Opus                            bool
	CertificateSubject              string
	StrongCertificate               bool

	TCPPackets, UDPPackets uint32
	TCPPingAvg, TCPPingVar float32 // ms
	UDPPingAvg, UDPPingVar float32 // ms
	FromClient, FromServer PacketStats
}

// PacketStats counts the encrypted UDP voice packets in one direction.
type PacketStats struct {
	Good, Late, Lost, Resync uint32
}

func (UserStatsFetched) isBusEvent() {}

// UnreadChanged reports the number of unread messages of a conversation:
// Session is the private conversation partner, 0 for the channel chat.
// Emitted by the chat view for the channel tree's badges.
type UnreadChanged struct {
	Session uint32
	Count   int
}

func (UnreadChanged) isBusEvent() {}
