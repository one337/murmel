// Package permname names Mumble's channel permissions for people, as the
// permission editor lists them and as refusals from the server mention them.
package permname

import (
	"github.com/one337/murmel/internal/protocol/gumble"

	"github.com/one337/murmel/internal/i18n"
)

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
		return i18n.Tr("Write ACL")
	case gumble.PermissionTraverse:
		// TRANSLATORS: Permission: pass through the channel to its sub-channels.
		return i18n.Tr("Traverse")
	case gumble.PermissionEnter:
		// TRANSLATORS: Permission: enter (join) the channel.
		return i18n.Tr("Enter")
	case gumble.PermissionSpeak:
		// TRANSLATORS: Permission: talk in the channel.
		return i18n.Tr("Speak")
	case gumble.PermissionWhisper:
		return i18n.Tr("Whisper")
	case gumble.PermissionMuteDeafen:
		return i18n.Tr("Mute/Deafen")
	case gumble.PermissionMove:
		// TRANSLATORS: Permission: move other users between channels.
		return i18n.Tr("Move")
	case gumble.PermissionMakeChannel:
		return i18n.Tr("Make Channel")
	case gumble.PermissionMakeTemporaryChannel:
		return i18n.Tr("Make Temporary Channel")
	case gumble.PermissionLinkChannel:
		return i18n.Tr("Link Channel")
	case gumble.PermissionTextMessage:
		return i18n.Tr("Text Message")
	case gumble.PermissionKick:
		return i18n.Tr("Kick")
	case gumble.PermissionBan:
		return i18n.Tr("Ban")
	case gumble.PermissionRegister:
		return i18n.Tr("Register User")
	case gumble.PermissionRegisterSelf:
		return i18n.Tr("Register Self")
	}
	return ""
}
