package channeltree

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
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
		return i18n.Tr("deafened themselves")
	case stateSelfMuted:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name; also the status icon's tooltip, there
		// with a capital first letter.
		return i18n.Tr("muted themselves")
	case stateServerDeafened:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name; also the status icon's tooltip, there
		// with a capital first letter.
		return i18n.Tr("deafened on the server")
	case stateServerMuted:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name; also the status icon's tooltip, there
		// with a capital first letter.
		return i18n.Tr("muted on the server")
	case stateSuppressed:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name: the channel does not let the user speak.
		// Also the status icon's tooltip, there with a capital first letter.
		return i18n.Tr("not allowed to speak here")
	case statePrioritySpeaker:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name; same wording as the "Priority speaker"
		// menu entry. Also the status icon's tooltip, there with a capital
		// first letter.
		return i18n.Tr("priority speaker")
	case stateRecording:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name; also the status icon's tooltip, there
		// with a capital first letter.
		return i18n.Tr("recording")
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

// part is one entry of a row's description; key names the state it
// stands for (see changeText).
type part struct {
	key  string
	text string
}

const (
	keySelfDeafened   = "self-deafened"
	keySelfMuted      = "self-muted"
	keyServerDeafened = "server-deafened"
	keyServerMuted    = "server-muted"
	keySuppressed     = "suppressed"
	keyPriority       = "priority"
	keyRecording      = "recording"
	keyLocalMuted     = "local-muted"
	keyLocalVolume    = "local-volume"
	keyUsers          = "users"
	keyEmpty          = "empty"
	keyHere           = "here"
)

// userParts lists a user's state for assistive technologies. Being
// deafened includes being muted the same way, so then only the first is
// named.
func userParts(u bus.UserInfo, a localAudio) []part {
	var parts []part
	switch {
	case u.SelfDeafened:
		parts = append(parts, part{keySelfDeafened, stateSelfDeafened.text()})
	case u.SelfMuted:
		parts = append(parts, part{keySelfMuted, stateSelfMuted.text()})
	}
	switch {
	case u.Deafened:
		parts = append(parts, part{keyServerDeafened, stateServerDeafened.text()})
	case u.Muted:
		parts = append(parts, part{keyServerMuted, stateServerMuted.text()})
	}
	if u.Suppressed {
		parts = append(parts, part{keySuppressed, stateSuppressed.text()})
	}
	if u.PrioritySpeaker {
		parts = append(parts, part{keyPriority, statePrioritySpeaker.text()})
	}
	if u.Recording {
		parts = append(parts, part{keyRecording, stateRecording.text()})
	}
	switch {
	case a.muted:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name.
		parts = append(parts, part{keyLocalMuted, i18n.Tr("muted for you")})
	case percent(a.volume) != 100:
		parts = append(parts, part{keyLocalVolume, localVolumeText(percent(a.volume))})
	}
	return parts
}

func localVolumeText(percent int) string {
	// TRANSLATORS: Part of a comma-separated list that screen readers read
	// after a user's name.
	return fmt.Sprintf(i18n.Tr("volume for you: %d %%"), percent)
}

// userDescription is a user row's description: the user's state, then
// unread messages.
func userDescription(u bus.UserInfo, a localAudio, unread int) string {
	return describe(userParts(u, a), unread)
}

// channelParts lists a channel's state for assistive technologies: how
// many users are in it (or that it has no content at all) and whether
// one is in it oneself.
func channelParts(users int, kids, here bool) []part {
	var parts []part
	switch {
	case users > 0:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a channel's name.
		parts = append(parts, part{keyUsers, fmt.Sprintf(i18n.TrN("%d user", "%d users", users), users)})
	case !kids:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a channel's name: no users and no sub-channels.
		parts = append(parts, part{keyEmpty, i18n.Tr("empty")})
	}
	if here {
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a channel's name: one is in this channel oneself.
		parts = append(parts, part{keyHere, i18n.Tr("you are here")})
	}
	return parts
}

// channelDescription is a channel row's description: the channel's
// state, then unread messages.
func channelDescription(users int, kids, here bool, unread int) string {
	return describe(channelParts(users, kids, here), unread)
}

func describe(parts []part, unread int) string {
	texts := make([]string, 0, len(parts)+1)
	for _, p := range parts {
		texts = append(texts, p.text)
	}
	if unread > 0 {
		texts = append(texts, unreadText(unread))
	}
	return strings.Join(texts, ", ")
}

func unreadText(n int) string {
	// TRANSLATORS: Part of a comma-separated list that screen readers read
	// after a user's or channel's name.
	return fmt.Sprintf(i18n.TrN("%d unread message", "%d unread messages", n), n)
}

// changeText says what changed from old to now in one line: first the
// states that ended, then the ones that began or changed. Being deafened
// includes being muted the same way: going from muted to deafened is told
// as being deafened, back as no longer being deafened.
func changeText(old, now []part) string {
	has := func(parts []part, key string) (part, bool) {
		for _, p := range parts {
			if p.key == key {
				return p, true
			}
		}
		return part{}, false
	}
	// implied names, for a state, the state that includes it.
	implied := map[string]string{keySelfMuted: keySelfDeafened, keyServerMuted: keyServerDeafened}
	var texts []string
	for _, p := range old {
		if _, ok := has(now, p.key); ok {
			continue
		}
		if k, ok := implied[p.key]; ok {
			if _, ok := has(now, k); ok {
				continue // muted, now deafened: told below
			}
		}
		if t := endedText(p.key); t != "" {
			texts = append(texts, t)
		}
	}
	for _, p := range now {
		if q, ok := has(old, p.key); ok && q.text == p.text {
			continue
		}
		if k, ok := implied[p.key]; ok {
			if _, ok := has(old, k); ok {
				continue // deafened, now only muted: told above
			}
		}
		texts = append(texts, p.text)
	}
	return strings.Join(texts, ", ")
}

// endedText says that a state ended ("" where the new state says it).
func endedText(key string) string {
	switch key {
	case keySelfDeafened:
		// TRANSLATORS: Screen readers say this when the user that has the
		// keyboard focus ends a state; several are joined by commas.
		return i18n.Tr("undeafened themselves")
	case keySelfMuted:
		// TRANSLATORS: Screen readers say this when the user that has the
		// keyboard focus ends a state; several are joined by commas.
		return i18n.Tr("unmuted themselves")
	case keyServerDeafened:
		// TRANSLATORS: Screen readers say this when the user that has the
		// keyboard focus ends a state; several are joined by commas.
		return i18n.Tr("undeafened on the server")
	case keyServerMuted:
		// TRANSLATORS: Screen readers say this when the user that has the
		// keyboard focus ends a state; several are joined by commas.
		return i18n.Tr("unmuted on the server")
	case keySuppressed:
		// TRANSLATORS: Screen readers say this when the user that has the
		// keyboard focus ends a state; several are joined by commas.
		return i18n.Tr("allowed to speak here again")
	case keyPriority:
		// TRANSLATORS: Screen readers say this when the user that has the
		// keyboard focus ends a state; several are joined by commas.
		return i18n.Tr("no longer priority speaker")
	case keyRecording:
		// TRANSLATORS: Screen readers say this when the user that has the
		// keyboard focus ends a state; several are joined by commas.
		return i18n.Tr("stopped recording")
	case keyLocalMuted:
		// TRANSLATORS: Screen readers say this when the user that has the
		// keyboard focus ends a state; several are joined by commas.
		return i18n.Tr("unmuted for you")
	case keyLocalVolume:
		return localVolumeText(100)
	case keyHere:
		// TRANSLATORS: Screen readers say this when one leaves the channel
		// that has the keyboard focus.
		return i18n.Tr("you are no longer in this channel")
	}
	return ""
}
