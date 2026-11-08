package chat

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/ui/richtext"
)

const (
	// infoCollapsedLines is how much of the description shows collapsed
	// (images are cut at the same height).
	infoCollapsedLines = 4
	// infoExpandedMaxPx caps the expanded description; longer ones scroll.
	infoExpandedMaxPx = 360
	// Displayed image bounds inside the description.
	infoImageMaxWidth  = 480
	infoImageMaxHeight = 320
	// heroMinWidth: a description starting with an image at least this
	// wide shows it as a banner (smaller ones would blur when stretched).
	heroMinWidth = 480
	// heroExpandedMaxPx caps the expanded banner (taller images are
	// cropped) so the chat stays usable.
	heroExpandedMaxPx = 480
)

// infoMode is how the description is shown.
type infoMode int

const (
	infoNone   infoMode = iota // no description
	infoNormal                 // text under the header
	infoHero                   // banner image behind the header
)

// channelInfo shows the channel description above the channel chat:
// collapsed to a few lines with the rest fading out, expandable with a
// button. Hidden while the channel has no description.
type channelInfo struct {
	root   *gtk.Box
	scroll *gtk.ScrolledWindow
	fade   *gtk.Box
	toggle *gtk.Button

	html     string
	content  *gtk.Box
	expanded bool
	// onMode reports how the description is shown, so the page can
	// restyle its header (banner mode runs the content under it).
	onMode func(infoMode)
	// headerHeight is the page header's height (banner sizing).
	headerHeight func() int
	mode         infoMode

	// Banner mode: the first image across the full width, starting at the
	// window's top edge, the rest of the description below it.
	heroRoot   *gtk.Box
	heroPic    *gtk.Picture
	heroSizer  *gtk.Box // sets the banner height; the picture covers it
	heroTex    *gdk.Texture
	heroText   *gtk.Box
	heroToggle *gtk.Button
	// The toggle sits on the image's faded bottom (heroOnImage), or below
	// the text when an expanded banner has more description.
	heroOnImage *gtk.Box
	heroBelow   *gtk.Box
	toggleBelow bool
	// bottomFade continues the area's colour into the chat: an overlay at
	// the top of the chat (the toolbar puts a gap between top bars and
	// content, which would otherwise show as a stripe).
	bottomFade *gtk.Box
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

	// The colour of the area fades into the chat below it.
	i.bottomFade = gtk.NewBox(gtk.OrientationVertical, 0)
	i.bottomFade.AddCSSClass("channel-info-bottom")
	i.bottomFade.SetVAlign(gtk.AlignStart)
	i.bottomFade.SetCanTarget(false)
	i.bottomFade.SetVisible(false)

	inner := gtk.NewBox(gtk.OrientationVertical, 0)
	inner.AddCSSClass("channel-info")
	inner.Append(overlay)
	inner.Append(i.toggle)

	i.root = gtk.NewBox(gtk.OrientationVertical, 0)
	i.root.Append(inner)
	i.root.SetVisible(false)

	// Banner: picture with a dark scrim behind the header for legibility.
	i.heroPic = gtk.NewPicture()
	i.heroPic.SetContentFit(gtk.ContentFitCover)
	i.heroPic.SetCanShrink(true)
	i.heroPic.SetHExpand(true)
	scrim := gtk.NewBox(gtk.OrientationVertical, 0)
	scrim.AddCSSClass("channel-hero-scrim")
	scrim.SetVAlign(gtk.AlignStart)
	scrim.SetCanTarget(false)
	// The picture is an overlay child: the overlay takes its height from
	// the sizer alone, and the picture covers (and is cropped to) that.
	i.heroSizer = gtk.NewBox(gtk.OrientationVertical, 0)
	heroImage := gtk.NewOverlay()
	heroImage.SetChild(i.heroSizer)
	// The picture stops short of the bottom, where the fade is opaque:
	// with fractional scaling its last pixel row would otherwise peek out
	// below the fade as a thin line.
	i.heroPic.SetMarginBottom(heroPicInset)
	heroImage.AddOverlay(i.heroPic)
	heroImage.AddOverlay(scrim)
	heroImage.SetOverflow(gtk.OverflowHidden)
	// The image's bottom, behind the toggle, fades into the chat colour.
	heroFade := gtk.NewBox(gtk.OrientationVertical, 0)
	heroFade.AddCSSClass("channel-hero-fade")
	heroFade.SetVAlign(gtk.AlignEnd)
	heroFade.SetCanTarget(false)
	heroImage.AddOverlay(heroFade)
	i.heroOnImage = gtk.NewBox(gtk.OrientationVertical, 0)
	i.heroOnImage.SetVAlign(gtk.AlignEnd)
	i.heroOnImage.SetHAlign(gtk.AlignCenter)
	heroImage.AddOverlay(i.heroOnImage)
	i.heroText = gtk.NewBox(gtk.OrientationVertical, 0)
	i.heroText.AddCSSClass("channel-hero-text")
	i.heroToggle = gtk.NewButton()
	i.heroToggle.AddCSSClass("flat")
	i.heroToggle.AddCSSClass("channel-info-toggle")
	i.heroToggle.SetHAlign(gtk.AlignCenter)
	i.heroToggle.ConnectClicked(func() {
		i.expanded = !i.expanded
		i.layout()
	})
	heroClick := gtk.NewGestureClick()
	heroClick.ConnectReleased(func(int, float64, float64) {
		if !i.expanded && i.heroToggle.Visible() {
			i.expanded = true
			i.layout()
		}
	})
	i.heroPic.AddController(heroClick)
	i.heroBelow = gtk.NewBox(gtk.OrientationVertical, 0)
	i.heroBelow.AddCSSClass("channel-hero-text")
	i.heroOnImage.Append(i.heroToggle)
	i.heroRoot = gtk.NewBox(gtk.OrientationVertical, 0)
	i.heroRoot.Append(heroImage)
	i.heroRoot.Append(i.heroText)
	i.heroRoot.Append(i.heroBelow)
	i.heroRoot.SetVisible(false)

	// Re-check the overflow when the width changes (text rewraps).
	i.root.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		if w := i.scroll.Width(); w != i.lastWidth && w > 0 {
			i.lastWidth = w
			i.layout()
		}
		return true
	})
	i.heroRoot.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		if w := i.heroRoot.Width() + 1000*i.header(); w != i.lastWidth && i.heroRoot.Width() > 0 {
			i.lastWidth = w
			i.layout()
		}
		return true
	})
	return i
}

func (i *channelInfo) header() int {
	if i.headerHeight != nil {
		if h := i.headerHeight(); h > 0 {
			return h
		}
	}
	return 47
}

// set shows a description (Mumble HTML; "" hides the area).
func (i *channelInfo) set(html string) {
	if html == i.html {
		return
	}
	i.html = html
	i.expanded = false
	blocks := richtext.Parse(html)
	i.mode = infoNone
	i.heroTex = nil
	if len(blocks) > 0 {
		i.mode = infoNormal
		if blocks[0].Image != nil {
			if tex, err := gdk.NewTextureFromBytes(glib.NewBytes(blocks[0].Image)); err == nil && tex.Width() >= heroMinWidth {
				i.mode = infoHero
				i.heroTex = tex
				blocks = blocks[1:]
			}
		}
	}
	i.root.SetVisible(i.mode == infoNormal)
	i.heroRoot.SetVisible(i.mode == infoHero)
	i.bottomFade.SetVisible(i.mode == infoNormal)
	if i.onMode != nil {
		i.onMode(i.mode)
	}
	opts := richtext.Options{MaxImageWidth: infoImageMaxWidth, MaxImageHeight: infoImageMaxHeight, Selectable: true}
	switch i.mode {
	case infoNone:
		i.scroll.SetChild(nil)
		i.content = nil
		return
	case infoHero:
		i.heroPic.SetPaintable(i.heroTex)
		for c := i.heroText.FirstChild(); c != nil; c = i.heroText.FirstChild() {
			i.heroText.Remove(c)
		}
		if len(blocks) > 0 {
			i.heroText.Append(richtext.Build(blocks, opts))
		}
		i.content = nil
	default:
		i.content = richtext.Build(blocks, opts)
		i.scroll.SetChild(i.content)
	}
	i.lastWidth = 0
	i.layout()
}

// heroPicInset is how far the banner picture ends above its faded bottom.
const heroPicInset = 8

// stackedHeaderShrink: with the text description as a second top bar,
// libadwaita collapses the header's spacing, so the text starts this much
// higher than a lone header (the banner's case) ends.
const stackedHeaderShrink = 3

// infoTopPadding is the description's top padding (style.css
// .channel-info), part of the collapsed height in both modes.
const infoTopPadding = 2

// lineSample is two lines of text, measured (never shown) for the height
// of the collapsed description.
const lineSample = "X\nX"

// collapsedHeight is the height of infoCollapsedLines lines of text.
func (i *channelInfo) collapsedHeight() int {
	l := gtk.NewLabel(lineSample)
	_, nat, _, _ := l.Measure(gtk.OrientationVertical, -1)
	return nat * infoCollapsedLines / 2 // lineSample has two lines
}

// layout applies the collapsed/expanded state: collapsed clips the text
// to two lines and fades it out; the toggle appears only when the text is
// longer than that.
func (i *channelInfo) layout() {
	if i.mode == infoHero {
		i.layoutHero()
		return
	}
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

// layoutHero sizes the banner. Collapsed it reaches from the window's top
// edge exactly to where a collapsed text description ends (header height
// plus the collapsed lines), so "Show more" sits at the same height for
// every channel; expanded it shows the whole image, the rest of the
// description below it.
func (i *channelInfo) layoutHero() {
	if i.heroTex == nil {
		return
	}
	width := i.heroRoot.Width()
	if width <= 0 {
		width = 600
	}
	collapsed := i.header() - stackedHeaderShrink + infoTopPadding + i.collapsedHeight()
	full := min(heroExpandedMaxPx, width*i.heroTex.Height()/max(1, i.heroTex.Width()))
	hasText := i.heroText.FirstChild() != nil
	overflow := full > collapsed || hasText
	i.heroToggle.SetVisible(overflow)
	if !overflow {
		i.expanded = false
	}
	// The image continues behind the toggle and fades out there; the
	// toggle starts where a text description's toggle starts.
	_, toggleH, _, _ := i.heroToggle.Measure(gtk.OrientationVertical, -1)
	below := i.expanded && hasText
	i.placeHeroToggle(below)
	i.heroBelow.SetVisible(below)
	if i.expanded {
		i.heroSizer.SetSizeRequest(-1, full+toggleH)
		i.heroText.SetVisible(hasText)
		i.heroToggle.SetChild(toggleLabel(tr("Show less"), "pan-up-symbolic"))
	} else {
		i.heroSizer.SetSizeRequest(-1, min(collapsed, full)+toggleH)
		i.heroText.SetVisible(false)
		i.heroToggle.SetChild(toggleLabel(tr("Show more"), "pan-down-symbolic"))
	}
}

// placeHeroToggle moves the banner's toggle below the text (or back
// onto the image).
func (i *channelInfo) placeHeroToggle(below bool) {
	if below == i.toggleBelow {
		return
	}
	if i.toggleBelow {
		i.heroBelow.Remove(i.heroToggle)
		i.heroOnImage.Append(i.heroToggle)
	} else {
		i.heroOnImage.Remove(i.heroToggle)
		i.heroBelow.Append(i.heroToggle)
	}
	i.toggleBelow = below
}
