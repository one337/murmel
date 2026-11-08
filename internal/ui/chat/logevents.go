package chat

import (
	"fmt"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/ui/presence"
)

// LogEvent is a kind of log line in the channel chat. The string is the
// key in the chat-log setting.
type LogEvent string

// Channel chat log events, in the order the preferences list them.
const (
	LogChannelJoin      LogEvent = "channel-join"
	LogChannelLeave     LogEvent = "channel-leave"
	LogSelfMove         LogEvent = "self-move"
	LogServerConnect    LogEvent = "server-connect"
	LogServerDisconnect LogEvent = "server-disconnect"
	LogKickBan          LogEvent = "kick-ban"
	LogServerMute       LogEvent = "server-mute"
	LogSelfMute         LogEvent = "self-mute"
)

// LogEvents lists every log event in display order.
var LogEvents = []LogEvent{
	LogChannelJoin, LogChannelLeave, LogSelfMove,
	LogServerConnect, LogServerDisconnect, LogKickBan,
	LogServerMute, LogSelfMute,
}

// LogDefault is whether an event is logged unless the user changed it:
// what happens in your own channel is on; server-wide comings and goings
// and self-muting are off (noisy on busy servers).
func LogDefault(e LogEvent) bool {
	switch e {
	case LogServerConnect, LogServerDisconnect, LogSelfMute:
		return false
	}
	return true
}

// LogEnabled resolves an event against the stored overrides.
func LogEnabled(stored map[string]bool, e LogEvent) bool {
	if v, ok := stored[string(e)]; ok {
		return v
	}
	return LogDefault(e)
}

// LogLabel is the preferences title of an event.
func LogLabel(e LogEvent) string {
	switch e {
	case LogChannelJoin:
		return tr("User joins your channel")
	case LogChannelLeave:
		return tr("User leaves your channel")
	case LogSelfMove:
		return tr("You switch channels")
	case LogServerConnect:
		return tr("User connects to the server")
	case LogServerDisconnect:
		return tr("User disconnects from the server")
	case LogKickBan:
		return tr("User is kicked or banned")
	case LogServerMute:
		return tr("User is muted or deafened by the server")
	case LogSelfMute:
		return tr("User mutes or deafens themselves")
	}
	return string(e)
}

// logLines turns a presence event into the channel chat's log lines
// (none when the matching events are switched off). self is the local
// session, channel resolves channel names.
func logLines(ev presence.Event, enabled func(LogEvent) bool, self uint32, channel func(uint32) string) []string {
	name := ev.User.Name
	switch ev.Kind {
	case presence.SelfMoved:
		if enabled(LogSelfMove) {
			return []string{fmt.Sprintf(tr("You moved to %s"), channel(ev.To))}
		}
	case presence.UserJoinedChannel:
		if enabled(LogChannelJoin) {
			return []string{fmt.Sprintf(tr("%s joined the channel"), name)}
		}
	case presence.UserLeftChannel:
		if enabled(LogChannelLeave) {
			return []string{fmt.Sprintf(tr("%s moved to %s"), name, channel(ev.To))}
		}
	case presence.UserConnected:
		switch {
		case enabled(LogServerConnect):
			return []string{fmt.Sprintf(tr("%s connected to the server"), name)}
		case ev.InMyChannel && enabled(LogChannelJoin):
			return []string{fmt.Sprintf(tr("%s joined the channel"), name)}
		}
	case presence.UserDisconnected:
		switch {
		case ev.Reason != "" && enabled(LogKickBan):
			return []string{fmt.Sprintf(tr("%s left the server (%s)"), name, ev.Reason)}
		case enabled(LogServerDisconnect):
			return []string{fmt.Sprintf(tr("%s disconnected from the server"), name)}
		case ev.InMyChannel && enabled(LogChannelLeave):
			return []string{fmt.Sprintf(tr("%s left the channel"), name)}
		}
	case presence.UserAudioChanged:
		if !ev.InMyChannel && ev.User.Session != self {
			return nil
		}
		return audioLines(ev.Before, ev.User, enabled, ev.User.Session == self)
	}
	return nil
}

// audioLines describes mute/deafen changes between two states.
func audioLines(before, after bus.UserInfo, enabled func(LogEvent) bool, isSelf bool) []string {
	var out []string
	add := func(changed, now bool, e LogEvent, on, off, selfOn, selfOff string) {
		if !changed || !enabled(e) {
			return
		}
		switch {
		case isSelf && now:
			out = append(out, selfOn)
		case isSelf:
			out = append(out, selfOff)
		case now:
			out = append(out, fmt.Sprintf(on, after.Name))
		default:
			out = append(out, fmt.Sprintf(off, after.Name))
		}
	}
	add(before.Muted != after.Muted, after.Muted, LogServerMute,
		tr("%s was muted by the server"), tr("%s was unmuted by the server"),
		tr("You were muted by the server"), tr("You were unmuted by the server"))
	add(before.Deafened != after.Deafened, after.Deafened, LogServerMute,
		tr("%s was deafened by the server"), tr("%s was undeafened by the server"),
		tr("You were deafened by the server"), tr("You were undeafened by the server"))
	add(before.Suppressed != after.Suppressed, after.Suppressed, LogServerMute,
		tr("%s was suppressed"), tr("%s is no longer suppressed"),
		tr("You were suppressed"), tr("You are no longer suppressed"))
	add(before.SelfMuted != after.SelfMuted, after.SelfMuted, LogSelfMute,
		tr("%s muted themselves"), tr("%s unmuted themselves"),
		tr("You muted yourself"), tr("You unmuted yourself"))
	add(before.SelfDeafened != after.SelfDeafened, after.SelfDeafened, LogSelfMute,
		tr("%s deafened themselves"), tr("%s undeafened themselves"),
		tr("You deafened yourself"), tr("You undeafened yourself"))
	return out
}
