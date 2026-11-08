// Package chat renders the conversation view: a plain-text message list fed
// by bus TextMessage events and a multi-line composer that sends through the
// protocol Sender seam.
//
// Mumble messages may carry arbitrary HTML (including <img> with external
// URLs). Security model: every message body passes gumbleutil.PlainText
// before it reaches any widget, and message content is only ever set via
// SetText on labels/text views with markup disabled — no Pango markup, no
// URL loading, no rich text.
//
// History is session-only with a bounded scrollback (maxRows); the view
// auto-scrolls to the newest row while the user is anchored at the bottom
// and shows a new-message indicator while they scrolled up.
package chat
