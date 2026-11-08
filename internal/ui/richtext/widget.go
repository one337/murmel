package richtext

import (
	"html"
	"regexp"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// Options tunes how blocks become widgets.
type Options struct {
	// MaxImageWidth and MaxImageHeight bound displayed images (px); larger
	// images scale down, keeping their aspect ratio.
	MaxImageWidth, MaxImageHeight int
	// Selectable makes text selectable (chat messages).
	Selectable bool
}

// Build renders blocks into a vertical box.
func Build(blocks []Block, opt Options) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 4)
	for _, b := range blocks {
		if b.Image != nil {
			if pic := picture(b.Image, opt); pic != nil {
				box.Append(pic)
			}
			continue
		}
		box.Append(paragraph(b.Markup, opt))
	}
	return box
}

// paragraph is a wrapping label. Markup the parser produced is valid by
// construction; should Pango still reject it, the text is shown plain.
func paragraph(markup string, opt Options) *gtk.Label {
	l := gtk.NewLabel("")
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar)
	l.SetXAlign(0)
	l.SetSelectable(opt.Selectable)
	// Pango's parser does not know <a> (GtkLabel handles links itself),
	// so links are taken out for the check only.
	if _, _, _, err := pango.ParseMarkup(linkTags.ReplaceAllString(markup, ""), 0); err != nil {
		l.SetText(html.UnescapeString(stripTags(markup)))
	} else {
		l.SetMarkup(markup)
	}
	return l
}

// picture shows an embedded image scaled into the bounds; a click opens
// it at full size. nil when the data is not a loadable image.
func picture(data []byte, opt Options) gtk.Widgetter {
	tex, err := gdk.NewTextureFromBytes(glib.NewBytes(data))
	if err != nil || tex == nil {
		return nil
	}
	w, h := fit(tex.Width(), tex.Height(), opt.MaxImageWidth, opt.MaxImageHeight)
	pic := gtk.NewPictureForPaintable(tex)
	pic.SetContentFit(gtk.ContentFitContain)
	pic.SetCanShrink(true)
	pic.SetSizeRequest(w, h)
	pic.SetHAlign(gtk.AlignStart)
	pic.SetVAlign(gtk.AlignStart)
	pic.SetHExpand(false)
	pic.SetVExpand(false)
	// A fixed frame keeps the picture at w×h: GtkPicture otherwise grows
	// to the width it is offered.
	frame := gtk.NewBox(gtk.OrientationHorizontal, 0)
	frame.SetHAlign(gtk.AlignStart)
	frame.SetSizeRequest(w, h)
	pic.AddCSSClass("rich-image")
	pic.SetCursorFromName("zoom-in")
	click := gtk.NewGestureClick()
	click.ConnectReleased(func(int, float64, float64) { showFull(pic, tex) })
	pic.AddController(click)
	frame.Append(pic)
	return frame
}

// fit scales w×h down into maxW×maxH (0 = unbounded).
func fit(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return w, h
	}
	scale := 1.0
	if maxW > 0 && w > maxW {
		scale = float64(maxW) / float64(w)
	}
	if maxH > 0 && float64(h)*scale > float64(maxH) {
		scale = float64(maxH) / float64(h)
	}
	return max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
}

// showFull opens the image in a dialog at up to the window size.
func showFull(parent gtk.Widgetter, tex *gdk.Texture) {
	pic := gtk.NewPictureForPaintable(tex)
	pic.SetContentFit(gtk.ContentFitContain)
	pic.SetCanShrink(true)
	hdr := adw.NewHeaderBar()
	tv := adw.NewToolbarView()
	tv.AddTopBar(hdr)
	tv.SetContent(pic)
	d := adw.NewDialog()
	d.SetTitle("")
	w, h := fit(tex.Width(), tex.Height(), 1200, 900)
	d.SetContentWidth(max(w, 200))
	d.SetContentHeight(max(h+46, 150))
	d.SetChild(tv)
	d.Present(parent)
}

// linkTags matches <a …> and </a>.
var linkTags = regexp.MustCompile(`</?a\b[^>]*>`)

// stripTags removes markup tags (fallback for rejected markup).
func stripTags(m string) string {
	out := make([]rune, 0, len(m))
	in := false
	for _, r := range m {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			out = append(out, r)
		}
	}
	return string(out)
}
