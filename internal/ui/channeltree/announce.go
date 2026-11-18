package channeltree

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"io.github/murmel/internal/bus"
)

// The rows tell assistive technologies their state as text: the name is
// the user's or channel's name, the description lists the rest. Whether
// a user is speaking is not part of it: screen readers read out a changed
// description of the focused row, which would happen on every word.

// userState is a state of a user that has a text of its own.
type userState int

const (
	stateSelfDeafened userState = iota
	stateSelfMuted
	stateServerDeafened
	stateServerMuted
	stateSuppressed
	statePrioritySpeaker
	stateRecording
)

func (s userState) text() string {
	switch s {
	case stateSelfDeafened:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name; also the status icon's tooltip, there
		// with a capital first letter.
		return tr("deafened themselves")
	case stateSelfMuted:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name; also the status icon's tooltip, there
		// with a capital first letter.
		return tr("muted themselves")
	case stateServerDeafened:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name; also the status icon's tooltip, there
		// with a capital first letter.
		return tr("deafened on the server")
	case stateServerMuted:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name; also the status icon's tooltip, there
		// with a capital first letter.
		return tr("muted on the server")
	case stateSuppressed:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name: the channel does not let the user speak.
		// Also the status icon's tooltip, there with a capital first letter.
		return tr("not allowed to speak here")
	case statePrioritySpeaker:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name; same wording as the "Priority speaker"
		// menu entry. Also the status icon's tooltip, there with a capital
		// first letter.
		return tr("priority speaker")
	case stateRecording:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name; also the status icon's tooltip, there
		// with a capital first letter.
		return tr("recording")
	}
	return ""
}

// stateIf is a state and whether the user is in it.
type stateIf struct {
	on    bool
	state userState
}

// iconTooltip lists the states a status icon stands for. Unlike the
// description it names being muted next to being deafened: both icons are
// shown then, and each explains itself.
func iconTooltip(states ...stateIf) string {
	var parts []string
	for _, s := range states {
		if s.on {
			parts = append(parts, s.state.text())
		}
	}
	text := strings.Join(parts, ", ")
	first, size := utf8.DecodeRuneInString(text)
	if size == 0 {
		return ""
	}
	return string(unicode.ToUpper(first)) + text[size:]
}

// userDescription lists a user's state for assistive technologies. Being
// deafened includes being muted the same way, so then only the first is
// named.
func userDescription(u bus.UserInfo, a localAudio, unread int) string {
	var parts []string
	switch {
	case u.SelfDeafened:
		parts = append(parts, stateSelfDeafened.text())
	case u.SelfMuted:
		parts = append(parts, stateSelfMuted.text())
	}
	switch {
	case u.Deafened:
		parts = append(parts, stateServerDeafened.text())
	case u.Muted:
		parts = append(parts, stateServerMuted.text())
	}
	if u.Suppressed {
		parts = append(parts, stateSuppressed.text())
	}
	if u.PrioritySpeaker {
		parts = append(parts, statePrioritySpeaker.text())
	}
	if u.Recording {
		parts = append(parts, stateRecording.text())
	}
	switch {
	case a.muted:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name.
		parts = append(parts, tr("muted for you"))
	case percent(a.volume) != 100:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name.
		parts = append(parts, fmt.Sprintf(tr("volume for you: %d %%"), percent(a.volume)))
	}
	if unread > 0 {
		parts = append(parts, unreadText(unread))
	}
	return strings.Join(parts, ", ")
}

// channelDescription lists a channel's state for assistive technologies:
// how many users are in it, whether one is in it oneself, unread messages.
func channelDescription(users int, here bool, unread int) string {
	var parts []string
	if users > 0 {
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a channel's name.
		parts = append(parts, fmt.Sprintf(trn("%d user", "%d users", users), users))
	}
	if here {
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a channel's name: one is in this channel oneself.
		parts = append(parts, tr("you are here"))
	}
	if unread > 0 {
		parts = append(parts, unreadText(unread))
	}
	return strings.Join(parts, ", ")
}

func unreadText(n int) string {
	// TRANSLATORS: Part of a comma-separated list that screen readers read
	// after a user's or channel's name.
	return fmt.Sprintf(trn("%d unread message", "%d unread messages", n), n)
}
