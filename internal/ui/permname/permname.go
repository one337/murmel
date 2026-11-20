// Package permname names Mumble's channel permissions for people, as the
// permission editor lists them and as refusals from the server mention them.
package permname

import "io.github/murmel/internal/protocol/gumble"

// Name returns the translated name of permission p, "" for a permission
// without a name here.
//
// TRANSLATORS (for the permission names below): these are Mumble channel
// permissions, shown as rows with Inherit/Allow/Deny. "Enter" means
// entering the channel, "Traverse" passing through it to sub-channels,
// "Move" moving other users, "Write ACL" editing the channel's permissions.
func Name(p gumble.Permission) string {
	switch p {
	case gumble.PermissionWrite:
		// TRANSLATORS: Permission: edit the channel's groups and permissions.
		return tr("Write ACL")
	case gumble.PermissionTraverse:
		// TRANSLATORS: Permission: pass through the channel to its sub-channels.
		return tr("Traverse")
	case gumble.PermissionEnter:
		// TRANSLATORS: Permission: enter (join) the channel.
		return tr("Enter")
	case gumble.PermissionSpeak:
		// TRANSLATORS: Permission: talk in the channel.
		return tr("Speak")
	case gumble.PermissionWhisper:
		return tr("Whisper")
	case gumble.PermissionMuteDeafen:
		return tr("Mute/Deafen")
	case gumble.PermissionMove:
		// TRANSLATORS: Permission: move other users between channels.
		return tr("Move")
	case gumble.PermissionMakeChannel:
		return tr("Make Channel")
	case gumble.PermissionMakeTemporaryChannel:
		return tr("Make Temporary Channel")
	case gumble.PermissionLinkChannel:
		return tr("Link Channel")
	case gumble.PermissionTextMessage:
		return tr("Text Message")
	case gumble.PermissionKick:
		return tr("Kick")
	case gumble.PermissionBan:
		return tr("Ban")
	case gumble.PermissionRegister:
		return tr("Register User")
	case gumble.PermissionRegisterSelf:
		return tr("Register Self")
	}
	return ""
}
