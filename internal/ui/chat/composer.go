package chat

import (
	"fmt"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// Sender is the outgoing-chat seam. T19 implements it over the live gumble
// connection (User.Send / Channel.Send); a nil Sender means no connection.
type Sender interface {
	// SendText delivers message (HTML). session != 0 addresses that user
	// privately; otherwise channelID addresses a channel.
	SendText(channelID, session uint32, message string) error
}

// defaultMaxMessageLength is murmur's documented text-message default,
// used until the server's ServerConfig announces the real limit.
const defaultMaxMessageLength = 5000

// composerMaxHeightPx caps the composer's growth before it scrolls.
const composerMaxHeightPx = 160

// counterShowFraction: the length counter appears once a message uses this
// share of the server's limit.
const counterShowFraction = 0.8

// composer is the message field: a full-width rounded frame with the text
// on top and a toolbar below — formatting on the left, send on the right.
// Enter sends, Shift+Enter inserts a line, Ctrl+B / Ctrl+I toggle bold and
// italic. The message is sent as Mumble HTML (<b>, <i>, <br>).
type composer struct {
	root    *gtk.Box
	view    *gtk.TextView
	buffer  *gtk.TextBuffer
	counter *gtk.Label
	sendBtn *gtk.Button
	toolbar *gtk.Box

	boldTag, italicTag *gtk.TextTag
	boldBtn, italicBtn *gtk.ToggleButton
	// typing is the formatting applied to newly typed text.
	typing struct{ bold, italic bool }
	// pending is the insertion awaiting the typing formatting (inserted
	// text only exists in the buffer once "changed" fires).
	pending struct {
		active bool
		offset int
		n      int
	}
	syncing bool

	maxLen int
	send   func(htmlText, plain string)
}

func newComposer() *composer {
	c := &composer{maxLen: defaultMaxMessageLength}

	c.buffer = gtk.NewTextBuffer(nil)
	c.boldTag = gtk.NewTextTag("bold")
	coreglib.InternObject(c.boldTag).SetObjectProperty("weight", 700)
	c.italicTag = gtk.NewTextTag("italic")
	coreglib.InternObject(c.italicTag).SetObjectProperty("style", pango.StyleItalic)
	c.buffer.TagTable().Add(c.boldTag)
	c.buffer.TagTable().Add(c.italicTag)
	c.buffer.ConnectInsertText(func(at *gtk.TextIter, text string, _ int) {
		c.pending.active = true
		c.pending.offset = at.Offset()
		c.pending.n = len([]rune(text))
	})
	c.buffer.ConnectChanged(func() {
		if c.pending.active {
			c.pending.active = false
			c.applyTyping(c.pending.offset, c.pending.offset+c.pending.n)
		}
		c.update()
	})

	c.view = gtk.NewTextViewWithBuffer(c.buffer)
	c.view.SetWrapMode(gtk.WrapWordChar)
	c.view.SetAcceptsTab(false)
	c.view.SetTopMargin(10)
	c.view.SetBottomMargin(4)
	c.view.SetLeftMargin(12)
	c.view.SetRightMargin(12)
	c.view.AddCSSClass("composer-view")
	c.view.SetTooltipText(tr("Message — Enter sends, Shift+Enter adds a line, Ctrl+B / Ctrl+I format"))

	keys := gtk.NewEventControllerKey()
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		switch {
		case (keyval == gdk.KEY_Return || keyval == gdk.KEY_KP_Enter) && state&gdk.ShiftMask == 0:
			c.Submit()
			return true
		case state&gdk.ControlMask != 0 && (keyval == gdk.KEY_b || keyval == gdk.KEY_B):
			c.boldBtn.SetActive(!c.boldBtn.Active())
			return true
		case state&gdk.ControlMask != 0 && (keyval == gdk.KEY_i || keyval == gdk.KEY_I):
			c.italicBtn.SetActive(!c.italicBtn.Active())
			return true
		}
		return false
	})
	c.view.AddController(keys)

	// The entry grows with its content up to a few lines, then scrolls.
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(c.view)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetPropagateNaturalHeight(true)
	scroll.SetMaxContentHeight(composerMaxHeightPx)
	scroll.SetHExpand(true)

	c.boldBtn = c.formatToggle("format-text-bold-symbolic", tr("Bold (Ctrl+B)"), c.boldTag, func(on bool) { c.typing.bold = on })
	c.italicBtn = c.formatToggle("format-text-italic-symbolic", tr("Italic (Ctrl+I)"), c.italicTag, func(on bool) { c.typing.italic = on })
	formatBox := gtk.NewBox(gtk.OrientationHorizontal, 0)
	formatBox.AddCSSClass("linked")
	formatBox.Append(c.boldBtn)
	formatBox.Append(c.italicBtn)
	formatPop := gtk.NewPopover()
	formatPop.SetChild(formatBox)
	formatBtn := gtk.NewMenuButton()
	formatBtn.SetIconName("format-text-rich-symbolic")
	formatBtn.SetAlwaysShowArrow(true)
	formatBtn.SetPopover(formatPop)
	formatBtn.SetDirection(gtk.ArrowUp)
	formatBtn.AddCSSClass("flat")
	formatBtn.SetTooltipText(tr("Formatting"))

	c.counter = gtk.NewLabel("")
	c.counter.AddCSSClass("caption")
	c.counter.AddCSSClass("dim-label")
	c.counter.AddCSSClass("numeric")
	c.counter.SetVisible(false)

	c.sendBtn = gtk.NewButtonFromIconName("mail-send-symbolic")
	c.sendBtn.AddCSSClass("suggested-action")
	c.sendBtn.AddCSSClass("composer-send")
	c.sendBtn.SetTooltipText(tr("Send message"))
	c.sendBtn.SetSensitive(false)
	c.sendBtn.ConnectClicked(func() { c.Submit() })

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	c.toolbar = gtk.NewBox(gtk.OrientationHorizontal, 6)
	c.toolbar.AddCSSClass("composer-toolbar")
	c.toolbar.Append(formatBtn)
	c.toolbar.Append(spacer)
	c.toolbar.Append(c.counter)
	c.toolbar.Append(c.sendBtn)

	c.root = gtk.NewBox(gtk.OrientationVertical, 0)
	c.root.AddCSSClass("composer-frame")
	c.root.Append(scroll)
	c.root.Append(c.toolbar)

	// Clicking anywhere in the frame focuses the text.
	click := gtk.NewGestureClick()
	click.ConnectPressed(func(int, float64, float64) { c.view.GrabFocus() })
	scroll.AddController(click)
	return c
}

// formatToggle builds a bold/italic toggle: with a selection it formats
// the selection, without one it sets the formatting for what is typed next.
func (c *composer) formatToggle(icon, tooltip string, tag *gtk.TextTag, setTyping func(bool)) *gtk.ToggleButton {
	btn := gtk.NewToggleButton()
	btn.SetIconName(icon)
	btn.SetTooltipText(tooltip)
	btn.ConnectToggled(func() {
		if c.syncing {
			return
		}
		on := btn.Active()
		setTyping(on)
		if start, end, ok := c.buffer.SelectionBounds(); ok {
			if on {
				c.buffer.ApplyTag(tag, start, end)
			} else {
				c.buffer.RemoveTag(tag, start, end)
			}
		}
		c.view.GrabFocus()
	})
	return btn
}

// applyTyping formats the just inserted characters [from, to).
func (c *composer) applyTyping(from, to int) {
	start := c.buffer.IterAtOffset(from)
	end := c.buffer.IterAtOffset(to)
	if c.typing.bold {
		c.buffer.ApplyTag(c.boldTag, start, end)
	} else {
		c.buffer.RemoveTag(c.boldTag, start, end)
	}
	if c.typing.italic {
		c.buffer.ApplyTag(c.italicTag, start, end)
	} else {
		c.buffer.RemoveTag(c.italicTag, start, end)
	}
}

// SetSend installs the submit handler: html is the message to send, plain
// its text for local display.
func (c *composer) SetSend(f func(html, plain string)) { c.send = f }

// SetEnabled turns the field on or off (offline conversation partner).
func (c *composer) SetEnabled(on bool, reason string) {
	c.view.SetEditable(on)
	c.view.SetCursorVisible(on)
	c.toolbar.SetSensitive(on)
	if on {
		c.view.SetTooltipText(tr("Message — Enter sends, Shift+Enter adds a line, Ctrl+B / Ctrl+I format"))
	} else {
		c.view.SetTooltipText(reason)
	}
	c.update()
}

// SetMaxMessageLength applies the server-announced cap.
func (c *composer) SetMaxMessageLength(n int) {
	if n <= 0 {
		n = defaultMaxMessageLength
	}
	c.maxLen = n
	c.update()
}

// MaxMessageLength is the currently enforced cap.
func (c *composer) MaxMessageLength() int { return c.maxLen }

// Submit sends the message and clears the field when it is non-empty and
// within the server's limit.
func (c *composer) Submit() {
	html, plain := c.message()
	if plain == "" || c.send == nil || !c.view.Editable() || len([]rune(html)) > c.maxLen {
		return
	}
	c.send(html, plain)
	c.buffer.SetText("")
}

// message returns the field as Mumble HTML and as plain text.
func (c *composer) message() (html, plain string) {
	var spans []span
	it := c.buffer.StartIter()
	cur := span{}
	var text []rune
	flush := func() {
		if len(text) > 0 {
			cur.text = string(text)
			spans = append(spans, cur)
			text = text[:0]
		}
	}
	for !it.IsEnd() {
		b, i := it.HasTag(c.boldTag), it.HasTag(c.italicTag)
		if b != cur.bold || i != cur.italic {
			flush()
			cur = span{bold: b, italic: i}
		}
		text = append(text, rune(it.Char()))
		it.ForwardChar()
	}
	flush()
	spans = trimSpans(spans)
	for _, s := range spans {
		plain += s.text
	}
	return spansToHTML(spans), plain
}

// update refreshes the send button and the length counter.
func (c *composer) update() {
	html, plain := c.message()
	n := len([]rune(html))
	c.sendBtn.SetSensitive(plain != "" && n <= c.maxLen && c.view.Editable())
	c.counter.SetVisible(float64(n) >= counterShowFraction*float64(c.maxLen))
	c.counter.SetText(counterText(n, c.maxLen))
	if n > c.maxLen {
		c.counter.AddCSSClass("error")
	} else {
		c.counter.RemoveCSSClass("error")
	}
}

func counterText(n, max int) string {
	return fmt.Sprintf("%d / %d", n, max)
}
