package chat

import (
	"fmt"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/ui/clock"
	"github.com/one337/murmel/internal/ui/richtext"
)

// rowKind distinguishes message rows from system rows (errors, notices).
type rowKind uint8

const (
	rowMessage rowKind = iota
	rowPrivate
	rowError
	rowNotice
	// rowServer is a message from the server itself (welcome text, notices
	// sent with no user as sender).
	rowServer
	// rowLog is an event line (user joined, muted, …): small and faded.
	rowLog
)

// rowStyle is the widget payload of one scrollback entry. A message with
// HTML is rendered by richtext (see the package comment); every other
// text is set with SetText.
type rowStyle struct {
	kind    rowKind
	sender  string
	at      time.Time
	content string
	// html, when set, is the message as Mumble HTML: rendered with
	// formatting and embedded images instead of content.
	html string
}

// chatRow is one scrollback entry: the row and its time stamp, which is
// written anew when the clock format changes (refreshTime).
type chatRow struct {
	row   *gtk.ListBoxRow
	stamp *gtk.Label
	at    time.Time
}

func (r *chatRow) refreshTime() { r.stamp.SetText(clock.Time(r.at)) }

// Displayed image bounds inside chat rows.
const (
	chatImageMaxWidth  = 360
	chatImageMaxHeight = 280
)

// newRow builds one ListBoxRow. The sender label shows the theme's accent
// color through the accent style class, so it follows a change of the
// accent color or the light/dark style; its text is set as plain text
// (never markup) so a name like "<b>evil</b>" can never be interpreted.
func newRow(st rowStyle) *chatRow {
	if st.kind == rowLog || st.kind == rowError {
		return newLogRow(st)
	}
	sender := gtk.NewLabel(st.sender)
	sender.AddCSSClass("heading")
	sender.AddCSSClass("accent")

	stamp := gtk.NewLabel(clock.Time(st.at))
	stamp.AddCSSClass("caption")
	stamp.AddCSSClass("dim-label")

	header := gtk.NewBox(gtk.OrientationHorizontal, 6)
	header.Append(sender)
	header.Append(stamp)

	if st.kind == rowPrivate {
		tag := gtk.NewLabel(i18n.Tr("private"))
		tag.AddCSSClass("caption")
		tagBox := gtk.NewBox(gtk.OrientationHorizontal, 0)
		tagBox.SetHExpand(true)
		tagBox.SetHAlign(gtk.AlignEnd)
		tagBox.Append(tag)
		header.Append(tagBox)
	} else {
		spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
		spacer.SetHExpand(true)
		header.Append(spacer)
	}

	body := gtk.NewBox(gtk.OrientationVertical, 2)
	body.Append(header)
	if st.html != "" {
		body.Append(richtext.Build(richtext.Parse(st.html), richtext.Options{
			MaxImageWidth: chatImageMaxWidth, MaxImageHeight: chatImageMaxHeight, Selectable: true}))
	} else {
		content := gtk.NewLabel(st.content)
		content.SetWrap(true)
		content.SetWrapMode(pango.WrapWordChar)
		content.SetXAlign(0.0)
		content.SetSelectable(true)
		body.Append(content)
	}

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	if class := rowCSSClass(st.kind); class != "" {
		box.AddCSSClass(class)
	}
	body.SetMarginStart(6)
	body.SetMarginEnd(6)
	body.SetMarginTop(4)
	body.SetMarginBottom(4)
	box.Append(body)

	row := gtk.NewListBoxRow()
	row.SetChild(box)
	row.SetActivatable(false)
	row.SetSelectable(false)
	return &chatRow{row: row, stamp: stamp, at: st.at}
}

// rowCSSClass maps row kinds to built-in style classes: errors carry the
// destructive color, notices the dim style. Message rows stay unclassed —
// the sender's accent style class provides the only color.
func rowCSSClass(k rowKind) string {
	switch k {
	case rowError:
		return "error"
	case rowNotice:
		return "dim-label"
	case rowServer:
		return "server-message"
	default:
		return ""
	}
}

// errorRowText formats an inline send-failure row.
func errorRowText(reason string) string {
	if reason == "" {
		return i18n.Tr("Message not delivered.")
	}
	return fmt.Sprintf(i18n.Tr("Message not delivered: %s"), reason)
}

// newLogRow is a single faded line: time and event text. An error is the
// same line in the error color, not faded.
func newLogRow(st rowStyle) *chatRow {
	stamp := gtk.NewLabel(clock.Time(st.at))
	stamp.AddCSSClass("numeric")
	text := gtk.NewLabel(st.content)
	text.SetWrap(true)
	text.SetWrapMode(pango.WrapWordChar)
	text.SetXAlign(0)
	text.SetHExpand(true)
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.AddCSSClass("log-line")
	if class := rowCSSClass(st.kind); class != "" {
		box.AddCSSClass(class)
	}
	box.SetMarginStart(6)
	box.SetMarginEnd(6)
	box.SetMarginTop(1)
	box.SetMarginBottom(1)
	box.Append(stamp)
	box.Append(text)
	row := gtk.NewListBoxRow()
	row.SetChild(box)
	row.SetActivatable(false)
	row.SetSelectable(false)
	return &chatRow{row: row, stamp: stamp, at: st.at}
}
