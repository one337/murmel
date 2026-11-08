package chat

import (
	"io.github/murmel/internal/protocol/gumble"
	"io.github/murmel/internal/protocol/gumbleutil"
)

// PlainText strips every HTML tag and entity from a message body via the
// fork's gumbleutil.PlainText sanitizer (xml decoder, non-strict, HTML
// entities, block tags become newlines). It is the ONLY path by which
// message text may reach a widget.
func PlainText(s string) string {
	if s == "" {
		return ""
	}
	return gumbleutil.PlainText(&gumble.TextMessage{Message: s})
}
