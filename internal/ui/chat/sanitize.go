package chat

import (
	"io.github/murmel/internal/protocol/gumble"
	"io.github/murmel/internal/protocol/gumbleutil"
)

// PlainText strips every HTML tag and entity from a message body via the
// fork's gumbleutil.PlainText sanitizer (xml decoder, non-strict, HTML
// entities, block tags become newlines). Messages are shown through
// richtext; this plain text is kept with each row and shown only for a
// row without HTML.
func PlainText(s string) string {
	if s == "" {
		return ""
	}
	return gumbleutil.PlainText(&gumble.TextMessage{Message: s})
}
