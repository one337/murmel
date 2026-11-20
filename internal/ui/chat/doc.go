// Package chat renders the conversation view: the channel chat and private
// conversations, fed by bus TextMessage events, and a multi-line composer
// that sends through the connection's Sender.
//
// Mumble messages may carry arbitrary HTML. Security model: a message is
// shown through richtext, which turns only a known subset into Pango markup
// it builds itself (richtext.Parse; text is escaped, so the sender's own
// markup never reaches Pango):
//   - links survive only with the schemes http, https, mailto and mumble
//     (richtext.safeLink); others are dropped and leave their text;
//   - images are shown only from data:image URIs (richtext.decodeDataURI,
//     at most 4 MiB decoded); a remote image is never fetched: it becomes
//     a link if its scheme is allowed, otherwise the text "[image]"
//     (parser.image);
//   - an embedded image is decoded only if its header names PNG, JPEG, GIF
//     or WebP of at most 2048×2048 pixels (richtext.CheckImage, before
//     gdk decodes it), and at most 8 per message (richtext.Build);
//   - should Pango reject the markup anyway, the paragraph is shown as
//     plain text (richtext.paragraph).
//
// Rows without HTML (log lines, errors) set their text with SetText; the
// plain text of a message (PlainText) is kept with its row.
//
// History is session-only with a bounded scrollback (maxRows); the view
// auto-scrolls to the newest row while the user is anchored at the bottom
// and shows a new-message indicator while they scrolled up.
package chat
