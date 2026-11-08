package chat

import (
	"fmt"

	"io.github/murmel/internal/bus"
)

// targetMode describes what the composer addresses.
type targetMode uint8

const (
	// targetSelf follows the local user's current channel (default).
	targetSelf targetMode = iota
	// targetChannel is an explicitly chosen channel.
	targetChannel
	// targetUser is a private-message target.
	targetUser
)

// target is the resolved chat target. The zero value addresses the local
// user's current channel.
type target struct {
	mode      targetMode
	channelID uint32
	session   uint32
}

func targetFromEvent(ev bus.ChatTargetChanged) target {
	switch {
	case ev.Session != 0:
		return target{mode: targetUser, session: ev.Session}
	case ev.ChannelID != 0:
		return target{mode: targetChannel, channelID: ev.ChannelID}
	default:
		return target{mode: targetSelf}
	}
}

// resolvedChannelID returns the channel the composer should address: the
// explicit channel, or in follow mode the local user's channel.
// selfChannel is 0 when unknown/disconnected.
func (t target) resolvedChannelID(selfChannel uint32) uint32 {
	if t.mode == targetChannel {
		return t.channelID
	}
	return selfChannel
}

// bannerText renders the target for the banner: "#Channel" for channel
// targets, "@User (private)" for private targets. Empty names fall back to
// stable placeholders.
func (t target) bannerText(channelName, userName string) string {
	switch t.mode {
	case targetUser:
		if userName == "" {
			userName = tr("Unknown user")
		}
		return fmt.Sprintf(tr("Private message to @%s"), userName)
	case targetChannel:
		if channelName == "" {
			channelName = tr("channel")
		}
		return "#" + channelName
	default:
		if channelName == "" {
			channelName = tr("Current channel")
		}
		return "#" + channelName
	}
}
