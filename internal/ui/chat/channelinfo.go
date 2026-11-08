package chat

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/ui/richtext"
)

const (
	// infoCollapsedLines is how much of the description shows collapsed
	// (images are cut at the same height).
	infoCollapsedLines = 2
	// infoExpandedMaxPx caps the expanded description; longer ones scroll.
	infoExpandedMaxPx = 360
	// Displayed image bounds inside the description.
	infoImageMaxWidth  = 480
	infoImageMaxHeight = 320
)

// channelInfo shows the channel description above the channel chat:
// collapsed to two lines with the rest fading out, expandable with a
// button. Hidden while the channel has no description.
type channelInfo struct {
	root   *gtk.Box
	scroll *gtk.ScrolledWindow
	fade   *gtk.Box
	toggle *gtk.Button

	html     string
	content  *gtk.Box
	expanded bool
	// lastWidth is the width the overflow was last measured for.
	lastWidth int
}

func newChannelInfo() *channelInfo {
	i := &channelInfo{}
	i.scroll = gtk.NewScrolledWindow()
	i.scroll.SetPropagateNaturalHeight(true)
	i.scroll.AddCSSClass("channel-info-text")

	i.fade = gtk.NewBox(gtk.OrientationVertical, 0)
	i.fade.AddCSSClass("channel-info-fade")
	i.fade.SetVAlign(gtk.AlignEnd)
	i.fade.SetCanTarget(false)

	overlay := gtk.NewOverlay()
	overlay.SetChild(i.scroll)
	overlay.AddOverlay(i.fade)

	i.toggle = gtk.NewButton()
	i.toggle.AddCSSClass("flat")
	i.toggle.AddCSSClass("channel-info-toggle")
	i.toggle.SetHAlign(gtk.AlignCenter)
	i.toggle.ConnectClicked(func() {
		i.expanded = !i.expanded
		i.layout()
	})

	// A click on the collapsed text expands it as well.
	click := gtk.NewGestureClick()
	click.ConnectReleased(func(int, float64, float64) {
		if !i.expanded && i.toggle.Visible() {
			i.expanded = true
			i.layout()
		}
	})
	i.scroll.AddController(click)

	i.root = gtk.NewBox(gtk.OrientationVertical, 0)
	i.root.AddCSSClass("channel-info")
	i.root.Append(overlay)
	i.root.Append(i.toggle)
	i.root.SetVisible(false)

	// Re-check the overflow when the width changes (text rewraps).
	i.root.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		if w := i.scroll.Width(); w != i.lastWidth && w > 0 {
			i.lastWidth = w
			i.layout()
		}
		return true
	})
	return i
}

// set shows a description (Mumble HTML; "" hides the area).
func (i *channelInfo) set(html string) {
	if html == i.html {
		return
	}
	i.html = html
	i.expanded = false
	blocks := richtext.Parse(html)
	i.root.SetVisible(len(blocks) > 0)
	if len(blocks) == 0 {
		i.scroll.SetChild(nil)
		i.content = nil
		return
	}
	i.content = richtext.Build(blocks, richtext.Options{
		MaxImageWidth: infoImageMaxWidth, MaxImageHeight: infoImageMaxHeight, Selectable: true})
	i.scroll.SetChild(i.content)
	i.lastWidth = 0
	i.layout()
}

// lineSample is two lines of text, measured (never shown) for the height
// of the collapsed description.
const lineSample = "X\nX"

// collapsedHeight is the height of infoCollapsedLines lines of text.
func (i *channelInfo) collapsedHeight() int {
	l := gtk.NewLabel(lineSample)
	_, nat, _, _ := l.Measure(gtk.OrientationVertical, -1)
	return nat * infoCollapsedLines / 2
}

// layout applies the collapsed/expanded state: collapsed clips the text
// to two lines and fades it out; the toggle appears only when the text is
// longer than that.
func (i *channelInfo) layout() {
	if i.content == nil {
		return
	}
	limit := i.collapsedHeight()
	width := i.scroll.Width()
	if width <= 0 {
		width = 400
	}
	_, natural, _, _ := i.content.Measure(gtk.OrientationVertical, width)
	overflow := natural > limit
	i.toggle.SetVisible(overflow)
	if !overflow {
		i.expanded = false
	}
	if i.expanded {
		i.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
		i.scroll.SetMaxContentHeight(infoExpandedMaxPx)
		i.scroll.SetMinContentHeight(-1)
		i.fade.SetVisible(false)
		i.toggle.SetChild(toggleLabel(tr("Show less"), "pan-up-symbolic"))
	} else {
		// External: no scrollbar, the content is clipped at the height.
		i.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyExternal)
		i.scroll.SetMaxContentHeight(limit)
		i.scroll.SetMinContentHeight(min(limit, natural))
		i.scroll.VAdjustment().SetValue(0)
		i.fade.SetVisible(overflow)
		i.toggle.SetChild(toggleLabel(tr("Show more"), "pan-down-symbolic"))
	}
}

func toggleLabel(text, icon string) *gtk.Box {
	b := gtk.NewBox(gtk.OrientationHorizontal, 4)
	l := gtk.NewLabel(text)
	l.AddCSSClass("caption")
	b.Append(l)
	b.Append(gtk.NewImageFromIconName(icon))
	return b
}
