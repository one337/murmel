package chat

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Sender is the outgoing-chat seam. T19 implements it over the live gumble
// connection (User.Send / Channel.Send); a nil Sender means no connection.
type Sender interface {
	// SendText delivers message. session != 0 addresses that user
	// privately; otherwise channelID addresses a channel.
	SendText(channelID, session uint32, message string) error
}

// defaultMaxMessageLength is murmur's documented text-message default,
// used until the server's ServerConfig announces the real limit.
const defaultMaxMessageLength = 5000

// composerMaxHeightPx caps the composer's growth before it scrolls.
const composerMaxHeightPx = 120

// composer is the multi-line entry: Enter sends, Shift+Enter inserts a
// newline, a live counter tracks the server's message-length cap.
type composer struct {
	root    *gtk.Box
	view    *gtk.TextView
	buffer  *gtk.TextBuffer
	counter *gtk.Label
	sendBtn *gtk.Button

	maxLen int
	send   func(text string)
}

func newComposer() *composer {
	c := &composer{maxLen: defaultMaxMessageLength}

	c.buffer = gtk.NewTextBuffer(nil)
	c.buffer.ConnectChanged(c.updateCounter)

	c.view = gtk.NewTextViewWithBuffer(c.buffer)
	c.view.SetWrapMode(gtk.WrapWordChar)
	c.view.SetAcceptsTab(false)
	c.view.SetTopMargin(8)
	c.view.SetBottomMargin(8)
	c.view.SetLeftMargin(10)
	c.view.SetRightMargin(10)
	c.view.AddCSSClass("composer-view")
	c.view.SetTooltipText(tr("Message — Enter sends, Shift+Enter adds a line"))

	keys := gtk.NewEventControllerKey()
	keys.ConnectKeyPressed(func(keyval, keycode uint, state gdk.ModifierType) bool {
		if keyval != gdk.KEY_Return && keyval != gdk.KEY_KP_Enter {
			return false
		}
		if state&gdk.ShiftMask == 0 {
			c.Submit()
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
	scroll.AddCSSClass("composer")

	c.counter = gtk.NewLabel(counterText(0, c.maxLen))
	c.counter.AddCSSClass("caption")
	c.counter.AddCSSClass("dim-label")
	c.counter.SetHAlign(gtk.AlignEnd)

	c.sendBtn = gtk.NewButtonFromIconName("mail-send-symbolic")
	c.sendBtn.AddCSSClass("suggested-action")
	c.sendBtn.AddCSSClass("circular")
	c.sendBtn.SetTooltipText(tr("Send message"))
	c.sendBtn.SetVAlign(gtk.AlignEnd)
	c.sendBtn.ConnectClicked(func() { c.Submit() })

	entryRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	entryRow.Append(scroll)
	entryRow.Append(c.sendBtn)

	c.root = gtk.NewBox(gtk.OrientationVertical, 2)
	c.root.SetMarginTop(6)
	c.root.SetMarginBottom(6)
	c.root.SetMarginStart(6)
	c.root.SetMarginEnd(6)
	c.root.Append(entryRow)
	c.root.Append(c.counter)
	return c
}

// SetSend installs the submit handler (the view).
func (c *composer) SetSend(f func(text string)) { c.send = f }

// SetMaxMessageLength applies the server-announced cap and refreshes the
// counter.
func (c *composer) SetMaxMessageLength(n int) {
	if n <= 0 {
		n = defaultMaxMessageLength
	}
	c.maxLen = n
	c.updateCounter()
}

// MaxMessageLength is the currently enforced cap.
func (c *composer) MaxMessageLength() int { return c.maxLen }

// Submit sends the buffer content (trailing newlines trimmed) and clears
// the composer when a handler is installed and the text is non-empty.
func (c *composer) Submit() {
	text := c.text()
	if text == "" || c.send == nil {
		return
	}
	c.send(text)
	c.buffer.SetText("")
}

// text returns the buffer content with surrounding whitespace trimmed.
func (c *composer) text() string {
	start, end := c.buffer.Bounds()
	return strings.TrimSpace(c.buffer.Text(start, end, true))
}

func (c *composer) updateCounter() {
	n := len([]rune(c.text()))
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
