package channeltree

import (
	"fmt"
	"strings"

	"io.github/murmel/internal/bus"
)

// The rows tell assistive technologies their state as text: the name is
// the user's or channel's name, the description lists the rest. Whether
// a user is speaking is not part of it: screen readers read out a changed
// description of the focused row, which would happen on every word.

// userDescription lists a user's state for assistive technologies. Being
// deafened includes being muted the same way, so then only the first is
// named.
func userDescription(u bus.UserInfo, a localAudio, unread int) string {
	var parts []string
	switch {
	case u.SelfDeafened:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name.
		parts = append(parts, tr("deafened themselves"))
	case u.SelfMuted:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name.
		parts = append(parts, tr("muted themselves"))
	}
	switch {
	case u.Deafened:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name.
		parts = append(parts, tr("deafened on the server"))
	case u.Muted:
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name.
		parts = append(parts, tr("muted on the server"))
	}
	if u.Suppressed {
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name: the channel does not let the user speak.
		parts = append(parts, tr("not allowed to speak here"))
	}
	if u.PrioritySpeaker {
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name; same wording as the "Priority speaker"
		// menu entry.
		parts = append(parts, tr("priority speaker"))
	}
	if u.Recording {
		// TRANSLATORS: Part of a comma-separated list that screen readers
		// read after a user's name.
		parts = append(parts, tr("recording"))
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
