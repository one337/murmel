package chat

import (
	"fmt"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"io.github/murmel/internal/ui/richtext"
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

// rowStyle is the widget payload of one scrollback entry. All text is set
// with SetText on markup-disabled labels; sanitization happened upstream.
type rowStyle struct {
	kind    rowKind
	sender  string
	at      time.Time
	content string
	// html, when set, is the message as Mumble HTML: rendered with
	// formatting and embedded images instead of content.
	html string
}

// Displayed image bounds inside chat rows.
const (
	chatImageMaxWidth  = 360
	chatImageMaxHeight = 280
)

// newRow builds one ListBoxRow. The sender label gets the theme's accent
// color via a Pango foreground attribute (never markup) so a name like
// "<b>evil</b>" can never be interpreted.
func newRow(st rowStyle) *gtk.ListBoxRow {
	if st.kind == rowLog {
		return newLogRow(st)
	}
	sender := gtk.NewLabel(st.sender)
	sender.AddCSSClass("heading")
	if rgba, ok := sender.StyleContext().LookupColor("accent_color"); ok && rgba != nil {
		attrs := pango.NewAttrList()
		attrs.Insert(pango.NewAttrForeground(
			uint16(rgba.Red()*65535), uint16(rgba.Green()*65535), uint16(rgba.Blue()*65535)))
		sender.SetAttributes(attrs)
	}

	stamp := gtk.NewLabel(st.at.Format("15:04"))
	stamp.AddCSSClass("caption")
	stamp.AddCSSClass("dim-label")

	header := gtk.NewBox(gtk.OrientationHorizontal, 6)
	header.Append(sender)
	header.Append(stamp)

	if st.kind == rowPrivate {
		tag := gtk.NewLabel(tr("private"))
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
	return row
}

// rowCSSClass maps row kinds to built-in style classes: errors carry the
// destructive color, notices the dim style. Message rows stay unclassed —
// the sender's accent attribute provides the only color.
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
		return tr("Message not delivered.")
	}
	return fmt.Sprintf(tr("Message not delivered: %s"), reason)
}

// newLogRow is a single faded line: time and event text.
func newLogRow(st rowStyle) *gtk.ListBoxRow {
	stamp := gtk.NewLabel(st.at.Format("15:04"))
	stamp.AddCSSClass("numeric")
	text := gtk.NewLabel(st.content)
	text.SetWrap(true)
	text.SetWrapMode(pango.WrapWordChar)
	text.SetXAlign(0)
	text.SetHExpand(true)
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.AddCSSClass("log-line")
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
	return row
}
