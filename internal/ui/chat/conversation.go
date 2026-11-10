package chat

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// conversation is one chat page: the channel chat or a private
// conversation. A header with the title, the scrollback, and the composer
// reaching down to the window edge.
type conversation struct {
	page     *adw.NavigationPage
	header   *adw.HeaderBar
	title    *adw.WindowTitle
	scroll   *gtk.ScrolledWindow
	list     *gtk.ListBox
	jump     *gtk.Revealer
	composer *composer
	// info is the channel description (channel chat only; nil otherwise).
	info *channelInfo

	hist *history[*gtk.ListBoxRow]
	// pin keeps the view scrolled to the bottom across allocation-driven
	// adjustment changes while the user is anchored there.
	pin bool
}

func newConversation(tag, title, emptyText string) *conversation {
	c := &conversation{hist: newHistory[*gtk.ListBoxRow](maxRows), pin: true}

	c.list = gtk.NewListBox()
	c.list.SetSelectionMode(gtk.SelectionNone)
	c.list.AddCSSClass("chat-list")
	empty := gtk.NewLabel(emptyText)
	empty.AddCSSClass("dim-label")
	empty.SetMarginTop(24)
	c.list.SetPlaceholder(empty)

	c.scroll = gtk.NewScrolledWindow()
	c.scroll.SetChild(c.list)
	c.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	c.scroll.SetVExpand(true)
	c.scroll.AddCSSClass("chat-scroll")
	adj := c.scroll.VAdjustment()
	adj.ConnectValueChanged(func() {
		c.pin = c.atBottom()
		if c.pin {
			c.jump.SetRevealChild(false)
		}
	})
	adj.ConnectChanged(func() {
		if c.pin {
			c.scrollToBottom()
		}
	})

	jumpBtn := gtk.NewButtonWithLabel(tr("Jump to latest"))
	jumpBtn.AddCSSClass("suggested-action")
	jumpBtn.AddCSSClass("pill")
	jumpBtn.SetTooltipText(tr("Scroll to the newest message"))
	jumpBtn.ConnectClicked(func() {
		c.pin = true
		c.scrollToBottom()
		c.jump.SetRevealChild(false)
	})
	c.jump = gtk.NewRevealer()
	c.jump.SetChild(jumpBtn)
	c.jump.SetTransitionType(gtk.RevealerTransitionTypeSlideUp)
	c.jump.SetHAlign(gtk.AlignCenter)
	c.jump.SetVAlign(gtk.AlignEnd)
	c.jump.SetMarginBottom(12)

	overlay := gtk.NewOverlay()
	overlay.SetChild(c.scroll)
	overlay.AddOverlay(c.jump)
	overlay.SetVExpand(true)

	c.composer = newComposer()

	c.title = adw.NewWindowTitle(title, "")
	c.header = adw.NewHeaderBar()
	c.header.SetTitleWidget(c.title)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(c.header)
	if tag != channelTag {
		toolbar.SetContent(withComposerBar(overlay, c.composer.root))
	} else {
		// The description sits right under the header in the header's
		// colour (no separator line). A banner image instead starts at the
		// window's top edge: the content runs under the transparent header.
		c.info = newChannelInfo()
		c.info.headerHeight = func() int { return c.header.Height() }
		toolbar.AddTopBar(c.info.root)
		overlay.AddOverlay(c.info.bottomFade)
		content := gtk.NewBox(gtk.OrientationVertical, 0)
		content.Append(c.info.heroRoot)
		content.Append(overlay)
		toolbar.SetContent(withComposerBar(content, c.composer.root))
		c.info.onMode = func(m infoMode) {
			toolbar.SetExtendContentToTopEdge(m == infoHero)
			if m == infoHero {
				c.header.AddCSSClass("hero-header")
			} else {
				c.header.RemoveCSSClass("hero-header")
			}
			if m == infoNone {
				toolbar.SetTopBarStyle(adw.ToolbarRaisedBorder)
			} else {
				toolbar.SetTopBarStyle(adw.ToolbarFlat)
			}
		}
	}

	c.page = adw.NewNavigationPage(toolbar, title)
	c.page.SetTag(tag)
	return c
}

// setTitle updates the header (and the page title used by back buttons).
func (c *conversation) setTitle(title, subtitle string) {
	c.title.SetTitle(title)
	c.title.SetSubtitle(subtitle)
	c.page.SetTitle(title)
}

// append adds one row, enforces the scrollback cap and keeps the view
// pinned to the bottom while anchored. fromOthers reveals the
// jump-to-latest button when the user scrolled up.
func (c *conversation) append(st rowStyle, fromOthers bool) {
	wasAnchored := c.atBottom()
	row := newRow(st)
	c.list.Append(row)
	if dropped, ok := c.hist.push(row); ok {
		c.list.Remove(dropped)
	}
	c.pin = wasAnchored
	if wasAnchored {
		c.scrollToBottom()
	} else if fromOthers {
		c.jump.SetRevealChild(true)
	}
}

// clear removes every message.
func (c *conversation) clear() {
	for _, row := range c.hist.clear() {
		c.list.Remove(row)
	}
	c.jump.SetRevealChild(false)
	c.pin = true
}

func (c *conversation) atBottom() bool {
	adj := c.scroll.VAdjustment()
	return anchored(adj.Value(), adj.Upper(), adj.PageSize())
}

func (c *conversation) scrollToBottom() {
	adj := c.scroll.VAdjustment()
	adj.SetValue(adj.Upper() - adj.PageSize())
}

// withComposerBar puts the composer under content. The margin around the
// field moves the window when dragged and maximizes it on a double click,
// like a title bar; the field itself does not. An AdwToolbarView bottom bar
// would make the whole bar, field included, a window handle, and the text
// view claims a press only once a drag changes the selection: selecting
// text moved the window and a double click on an empty line maximized it.
// So the handle fills the bar behind the field, and the field lies on top
// of it in an overlay instead of inside it.
func withComposerBar(content, field gtk.Widgetter) *gtk.Box {
	handle := gtk.NewWindowHandle()
	bar := gtk.NewOverlay()
	bar.AddCSSClass("composer-bar") // the margin in the chat's colour
	bar.SetChild(handle)
	bar.AddOverlay(field)
	bar.SetMeasureOverlay(field, true)

	box := gtk.NewBox(gtk.OrientationVertical, 0)
	gtk.BaseWidget(content).SetVExpand(true)
	box.Append(content)
	box.Append(bar)
	return box
}
