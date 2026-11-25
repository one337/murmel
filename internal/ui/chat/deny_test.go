package chat

import (
	"testing"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/protocol/gumble"
)

func denyState() bus.State {
	return bus.State{
		SelfSession: 1,
		Channels: map[uint32]bus.ChannelInfo{
			0: {ID: 0, Name: "Root"},
			4: {ID: 4, Name: "Quiet"},
		},
		Users: map[uint32]bus.UserInfo{
			1: {Session: 1, Name: "me"},
			7: {Session: 7, Name: "anna"},
		},
	}
}

// Every kind of refusal from the server has its own line, except those
// answered elsewhere: one's own Enter and a missing certificate.
func TestDenialText(t *testing.T) {
	enter, muteDeafen := int(gumble.PermissionEnter), int(gumble.PermissionMuteDeafen)
	unnamed := 0x800 // Listen: Murmel does not offer it
	for _, tt := range []struct {
		name string
		ev   bus.PermissionDenied
		want string // "" = no line
	}{
		{"own permission", bus.PermissionDenied{Reason: bus.DenyPermission, Session: 1, ChannelID: 4, Permission: muteDeafen},
			"You do not have the “Mute/Deafen” permission in Quiet."},
		{"own permission, no session", bus.PermissionDenied{Reason: bus.DenyPermission, ChannelID: 0, Permission: muteDeafen},
			"You do not have the “Mute/Deafen” permission in Root."},
		{"own permission without a name", bus.PermissionDenied{Reason: bus.DenyPermission, Session: 1, ChannelID: 4, Permission: unnamed},
			"You are not allowed to do that in Quiet."},
		{"own Enter", bus.PermissionDenied{Reason: bus.DenyPermission, Session: 1, ChannelID: 4, Permission: enter}, ""},
		{"another user's Enter", bus.PermissionDenied{Reason: bus.DenyPermission, Session: 7, ChannelID: 4, Permission: enter},
			"anna does not have the “Enter” permission in Quiet."},
		{"another user, unnamed permission", bus.PermissionDenied{Reason: bus.DenyPermission, Session: 7, ChannelID: 4, Permission: unnamed},
			"anna is not allowed to do that in Quiet."},
		{"a user who left", bus.PermissionDenied{Reason: bus.DenyPermission, Session: 9, ChannelID: 4, Permission: enter},
			"Another user does not have the “Enter” permission in Quiet."},
		{"a user who left, unnamed permission", bus.PermissionDenied{Reason: bus.DenyPermission, Session: 9, ChannelID: 4, Permission: unnamed},
			"Another user is not allowed to do that in Quiet."},
		{"unknown channel", bus.PermissionDenied{Reason: bus.DenyPermission, Session: 1, ChannelID: 99, Permission: muteDeafen},
			"You do not have the “Mute/Deafen” permission in another channel."},
		{"missing certificate", bus.PermissionDenied{Reason: bus.DenyMissingCertificate}, ""},
		{"text too long", bus.PermissionDenied{Reason: bus.DenyTextTooLong}, "Message not delivered: message too long"},
		{"SuperUser", bus.PermissionDenied{Reason: bus.DenySuperUser}, "The SuperUser cannot be changed."},
		{"channel name", bus.PermissionDenied{Reason: bus.DenyInvalidChannelName}, "Invalid channel name."},
		{"user name", bus.PermissionDenied{Reason: bus.DenyInvalidUserName, Text: "a b"}, "Invalid user name: a b"},
		{"user name not given", bus.PermissionDenied{Reason: bus.DenyInvalidUserName}, "Invalid user name."},
		{"temporary channel", bus.PermissionDenied{Reason: bus.DenyTemporaryChannel}, "Not allowed in a temporary channel."},
		{"channel full", bus.PermissionDenied{Reason: bus.DenyChannelFull}, "The channel is full."},
		{"nesting limit", bus.PermissionDenied{Reason: bus.DenyNestingLimit}, "Channels cannot be nested any deeper on this server."},
		{"channel count", bus.PermissionDenied{Reason: bus.DenyChannelCountLimit}, "This server allows no more channels."},
		{"channel listeners", bus.PermissionDenied{Reason: bus.DenyChannelListenerLimit}, "This channel allows no more listeners."},
		{"own listeners", bus.PermissionDenied{Reason: bus.DenyUserListenerLimit}, "You cannot listen to any more channels."},
		{"H9K", bus.PermissionDenied{Reason: bus.DenyH9K}, "I'm sorry, Dave. I'm afraid I can't do that."},
		{"text", bus.PermissionDenied{Reason: bus.DenyText, Text: "Not in this channel"}, "Refused by the server: Not in this channel"},
		{"text without text", bus.PermissionDenied{Reason: bus.DenyText}, "Refused by the server."},
		{"unknown kind", bus.PermissionDenied{Reason: bus.DenyUnknown, Text: "Try later"}, "Refused by the server: Try later"},
		{"unknown kind without text", bus.PermissionDenied{Reason: bus.DenyUnknown}, "Refused by the server."},
	} {
		got, ok := denialText(tt.ev, denyState())
		if !ok {
			got = ""
		}
		if got != tt.want || ok != (tt.want != "") {
			t.Errorf("%s: got %q (line %v), want %q", tt.name, got, ok, tt.want)
		}
	}
}
