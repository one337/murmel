package chat

import (
	"fmt"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/ui/presence"
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
		return i18n.Tr("User joins your channel")
	case LogChannelLeave:
		return i18n.Tr("User leaves your channel")
	case LogSelfMove:
		return i18n.Tr("You switch channels")
	case LogServerConnect:
		return i18n.Tr("User connects to the server")
	case LogServerDisconnect:
		return i18n.Tr("User disconnects from the server")
	case LogKickBan:
		return i18n.Tr("User is kicked or banned")
	case LogServerMute:
		return i18n.Tr("User is muted or deafened by the server")
	case LogSelfMute:
		return i18n.Tr("User mutes or deafens themselves")
	}
	return string(e)
}

// logLines turns a presence event into the channel chat's log lines
// (none when the matching events are switched off). self is the local
// session, channel resolves channel names.
func logLines(ev presence.Event, enabled func(LogEvent) bool, self uint32, channel func(uint32) string) []string {
	name := ev.User.Name
	if name == "" && ev.User.Session != self && ev.Kind != presence.SelfMoved {
		return nil // a line about someone needs their name
	}
	switch ev.Kind {
	case presence.SelfConnected:
		if ev.User.Suppressed && enabled(LogServerMute) {
			return []string{selfSpeakLine(false)}
		}
	case presence.SelfSpeakChanged:
		if enabled(LogServerMute) {
			return []string{selfSpeakLine(ev.Allowed)}
		}
	case presence.SelfMoved:
		if enabled(LogSelfMove) {
			return []string{fmt.Sprintf(i18n.Tr("You moved to %s"), channel(ev.To))}
		}
	case presence.UserJoinedChannel:
		if enabled(LogChannelJoin) {
			return []string{fmt.Sprintf(i18n.Tr("%s joined the channel"), name)}
		}
	case presence.UserLeftChannel:
		if enabled(LogChannelLeave) {
			return []string{fmt.Sprintf(i18n.Tr("%s moved to %s"), name, channel(ev.To))}
		}
	case presence.UserConnected:
		var out []string
		switch {
		case enabled(LogServerConnect):
			out = append(out, fmt.Sprintf(i18n.Tr("%s connected to the server"), name))
		case ev.InMyChannel && enabled(LogChannelJoin):
			out = append(out, fmt.Sprintf(i18n.Tr("%s joined the channel"), name))
		}
		if ev.InMyChannel && ev.User.Suppressed && enabled(LogServerMute) {
			out = append(out, fmt.Sprintf(i18n.Tr("%s may not speak here"), name))
		}
		return out
	case presence.UserDisconnected:
		switch {
		case ev.Reason != "" && enabled(LogKickBan):
			return []string{fmt.Sprintf(i18n.Tr("%s left the server (%s)"), name, ev.Reason)}
		case enabled(LogServerDisconnect):
			return []string{fmt.Sprintf(i18n.Tr("%s disconnected from the server"), name)}
		case ev.InMyChannel && enabled(LogChannelLeave):
			return []string{fmt.Sprintf(i18n.Tr("%s left the channel"), name)}
		}
	case presence.UserAudioChanged:
		if !ev.InMyChannel && ev.User.Session != self {
			return nil
		}
		return audioLines(ev.Before, ev.User, enabled, ev.User.Session == self, ev.Moved)
	}
	return nil
}

// selfSpeakLine says whether one may speak (see presence.SelfSpeakChanged).
func selfSpeakLine(allowed bool) string {
	if allowed {
		return i18n.Tr("You're allowed to speak again")
	}
	return i18n.Tr("You're not allowed to speak in this channel")
}

// ownSpeakLine is whether ev's log line, if any, says whether one may
// speak: that line is also announced to screen readers.
func ownSpeakLine(ev presence.Event) bool {
	return ev.Kind == presence.SelfSpeakChanged || ev.Kind == presence.SelfConnected
}

// audioLines describes mute/deafen changes between two states. After a
// move (moved), only a new suppression of another user is told: its
// lifting only means the old channel's limit no longer applies. Whether
// one may speak oneself is told by selfSpeakLine.
func audioLines(before, after bus.UserInfo, enabled func(LogEvent) bool, isSelf, moved bool) []string {
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
		i18n.Tr("%s was muted by the server"), i18n.Tr("%s was unmuted by the server"),
		i18n.Tr("You were muted by the server"), i18n.Tr("You were unmuted by the server"))
	add(before.Deafened != after.Deafened, after.Deafened, LogServerMute,
		i18n.Tr("%s was deafened by the server"), i18n.Tr("%s was undeafened by the server"),
		i18n.Tr("You were deafened by the server"), i18n.Tr("You were undeafened by the server"))
	if !isSelf {
		add(before.Suppressed != after.Suppressed && (after.Suppressed || !moved), after.Suppressed, LogServerMute,
			i18n.Tr("%s may not speak here"), i18n.Tr("%s may speak again"), "", "")
	}
	add(before.SelfMuted != after.SelfMuted, after.SelfMuted, LogSelfMute,
		i18n.Tr("%s muted themselves"), i18n.Tr("%s unmuted themselves"),
		i18n.Tr("You muted yourself"), i18n.Tr("You unmuted yourself"))
	add(before.SelfDeafened != after.SelfDeafened, after.SelfDeafened, LogSelfMute,
		i18n.Tr("%s deafened themselves"), i18n.Tr("%s undeafened themselves"),
		i18n.Tr("You deafened yourself"), i18n.Tr("You undeafened yourself"))
	return out
}
