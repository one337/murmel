package chat

import (
	"fmt"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/protocol/gumble"
	"github.com/one337/murmel/internal/ui/permname"
)

// denialText explains a refusal from the server for the chat, or returns
// false where the refusal is answered elsewhere: one's own Enter (the
// controller asks for the channel password) and a missing certificate
// (the controller offers to create one).
func denialText(ev bus.PermissionDenied, st bus.State) (string, bool) {
	switch ev.Reason {
	case bus.DenyPermission:
		return permissionText(ev, st)
	case bus.DenyMissingCertificate:
		return "", false
	case bus.DenyTextTooLong:
		return errorRowText(i18n.Tr("message too long")), true
	case bus.DenySuperUser:
		return i18n.Tr("The SuperUser cannot be changed."), true
	case bus.DenyInvalidChannelName:
		return i18n.Tr("Invalid channel name."), true
	case bus.DenyInvalidUserName:
		if ev.Text == "" {
			return i18n.Tr("Invalid user name."), true
		}
		return fmt.Sprintf(i18n.Tr("Invalid user name: %s"), ev.Text), true
	case bus.DenyTemporaryChannel:
		return i18n.Tr("Not allowed in a temporary channel."), true
	case bus.DenyChannelFull:
		return i18n.Tr("The channel is full."), true
	case bus.DenyNestingLimit:
		return i18n.Tr("Channels cannot be nested any deeper on this server."), true
	case bus.DenyChannelCountLimit:
		return i18n.Tr("This server allows no more channels."), true
	case bus.DenyChannelListenerLimit:
		return i18n.Tr("This channel allows no more listeners."), true
	case bus.DenyUserListenerLimit:
		return i18n.Tr("You cannot listen to any more channels."), true
	case bus.DenyH9K:
		// TRANSLATORS: The server's answer to the text message "Open the pod
		// bay doors, HAL." in the root channel; the reply from the film.
		return i18n.Tr("I'm sorry, Dave. I'm afraid I can't do that."), true
	}
	// A plain text from the server (Mumble itself sends none to this
	// client), or a kind of refusal this version does not know.
	if ev.Text == "" {
		return i18n.Tr("Refused by the server."), true
	}
	return fmt.Sprintf(i18n.Tr("Refused by the server: %s"), ev.Text), true
}

// permissionText explains a missing permission: one's own, or another
// user's (moved into a channel they may not enter).
func permissionText(ev bus.PermissionDenied, st bus.State) (string, bool) {
	self := ev.Session == 0 || ev.Session == st.SelfSession
	perm := gumble.Permission(ev.Permission)
	if self && perm&gumble.PermissionEnter != 0 {
		return "", false
	}
	channel := i18n.Tr("another channel")
	if ch, ok := st.Channels[ev.ChannelID]; ok {
		channel = ch.Name
	}
	name := permname.Name(perm)
	if self {
		if name == "" {
			return fmt.Sprintf(i18n.Tr("You are not allowed to do that in %s."), channel), true
		}
		return fmt.Sprintf(i18n.Tr("You do not have the “%s” permission in %s."), name, channel), true
	}
	user, known := st.Users[ev.Session]
	switch {
	case !known && name == "":
		return fmt.Sprintf(i18n.Tr("Another user is not allowed to do that in %s."), channel), true
	case !known:
		return fmt.Sprintf(i18n.Tr("Another user does not have the “%s” permission in %s."), name, channel), true
	case name == "":
		return fmt.Sprintf(i18n.Tr("%s is not allowed to do that in %s."), user.Name, channel), true
	}
	return fmt.Sprintf(i18n.Tr("%s does not have the “%s” permission in %s."), user.Name, name, channel), true
}
