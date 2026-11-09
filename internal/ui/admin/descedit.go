package admin

import (
	"context"
	"log/slog"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gdkpixbuf/v2"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	gioglib "github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"io.github/murmel/internal/ui/chat"
	"io.github/murmel/internal/ui/richtext"
)

const (
	// descImagePx bounds images inside the editor (the preview and the
	// channel header show them larger).
	descImagePx = 320
	// defaultDescLimit is murmur's default for text with images.
	defaultDescLimit = 131072
	bullet           = "• "
	previewDelayMs   = 250
)

// descEditor edits a channel description with the toolbar's formatting
// only (see descdoc.go): bold, italic, heading, bullet list, link, image.
// A live preview renders the result as the channel header will.
type descEditor struct {
	root    *gtk.Box
	view    *gtk.TextView
	buffer  *gtk.TextBuffer
	preview *gtk.Box
	status  *gtk.Label

	bold, italic, heading, item, bulletTag *gtk.TextTag
	links                                  map[uintptr]string // link tag → href
	images                                 map[uintptr][]byte // texture → image file

	boldBtn, italicBtn *gtk.ToggleButton
	typing             struct{ bold, italic bool }
	pending            struct {
		active    bool
		offset, n int
	}
	busy        bool // the editor itself is changing the buffer
	limit       int
	original    string
	previewTick glib.SourceHandle
}

func newDescEditor(html string, limit int) *descEditor {
	if limit <= 0 {
		limit = defaultDescLimit
	}
	e := &descEditor{
		limit:    limit,
		original: html,
		links:    map[uintptr]string{},
		images:   map[uintptr][]byte{},
	}
	e.buffer = gtk.NewTextBuffer(nil)
	e.bold = e.newTag("bold", "weight", 700)
	e.italic = e.newTag("italic", "style", pango.StyleItalic)
	e.heading = e.newTag("heading", "weight", 700)
	glib.InternObject(e.heading).SetObjectProperty("scale", 1.3)
	glib.InternObject(e.heading).SetObjectProperty("pixels-above-lines", 6)
	e.item = e.newTag("item", "left-margin", 28)
	glib.InternObject(e.item).SetObjectProperty("indent", -14)
	e.bulletTag = e.newTag("bullet", "editable", false)

	e.buffer.ConnectInsertText(func(at *gtk.TextIter, text string, _ int) {
		if e.busy {
			return
		}
		e.pending.active = true
		e.pending.offset = at.Offset()
		e.pending.n = len([]rune(text))
	})
	e.buffer.ConnectChanged(func() {
		if e.pending.active && !e.busy {
			e.pending.active = false
			e.afterInsert(e.pending.offset, e.pending.n)
		}
		e.schedulePreview()
	})
	e.buffer.ConnectMarkSet(func(_ *gtk.TextIter, mark *gtk.TextMark) {
		if mark.Name() == "insert" {
			e.syncButtons()
		}
	})

	e.view = gtk.NewTextViewWithBuffer(e.buffer)
	e.view.SetWrapMode(gtk.WrapWordChar)
	e.view.SetTopMargin(12)
	e.view.SetBottomMargin(12)
	e.view.SetLeftMargin(12)
	e.view.SetRightMargin(12)
	e.view.SetVExpand(true)
	keys := gtk.NewEventControllerKey()
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		if state&gdk.ControlMask == 0 {
			return false
		}
		switch keyval {
		case gdk.KEY_b, gdk.KEY_B:
			e.boldBtn.SetActive(!e.boldBtn.Active())
		case gdk.KEY_i, gdk.KEY_I:
			e.italicBtn.SetActive(!e.italicBtn.Active())
		default:
			return false
		}
		return true
	})
	e.view.AddController(keys)

	e.load(parseDescription(html))

	// --- toolbar
	toolbar := gtk.NewBox(gtk.OrientationHorizontal, 2)
	toolbar.AddCSSClass("toolbar")
	e.boldBtn = e.styleToggle("format-text-bold-symbolic", tr("Bold (Ctrl+B)"), e.bold, func(on bool) { e.typing.bold = on })
	e.italicBtn = e.styleToggle("format-text-italic-symbolic", tr("Italic (Ctrl+I)"), e.italic, func(on bool) { e.typing.italic = on })
	toolbar.Append(e.boldBtn)
	toolbar.Append(e.italicBtn)
	toolbar.Append(gtk.NewSeparator(gtk.OrientationVertical))
	headingBtn := gtk.NewButton()
	headingBtn.AddCSSClass("flat")
	hLabel := gtk.NewLabel("")
	hLabel.SetMarkup("<b>H</b>")
	headingBtn.SetChild(hLabel)
	headingBtn.SetTooltipText(tr("Heading"))
	headingBtn.ConnectClicked(func() {
		e.toggleLines(e.heading)
		e.view.GrabFocus()
	})
	toolbar.Append(headingBtn)
	toolbar.Append(e.button("view-list-bullet-symbolic", "view-list-symbolic", tr("Bullet list"), func() { e.toggleLines(e.item) }))
	toolbar.Append(e.linkButton())
	toolbar.Append(e.button("insert-image-symbolic", "image-x-generic-symbolic", tr("Insert image…"), e.pickImage))

	edScroll := gtk.NewScrolledWindow()
	edScroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	edScroll.SetChild(e.view)
	edScroll.SetVExpand(true)

	editor := gtk.NewBox(gtk.OrientationVertical, 0)
	editor.AddCSSClass("card")
	editor.AddCSSClass("desc-editor")
	editor.SetOverflow(gtk.OverflowHidden)
	editor.SetHExpand(true)
	editor.Append(toolbar)
	editor.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	editor.Append(edScroll)

	e.preview = gtk.NewBox(gtk.OrientationVertical, 0)
	e.preview.SetMarginTop(12)
	e.preview.SetMarginBottom(12)
	e.preview.SetMarginStart(12)
	e.preview.SetMarginEnd(12)
	pvScroll := gtk.NewScrolledWindow()
	pvScroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	pvScroll.SetChild(e.preview)
	pvScroll.SetVExpand(true)
	pvTitle := gtk.NewLabel(tr("Preview"))
	pvTitle.AddCSSClass("heading")
	pvTitle.SetXAlign(0)
	pvTitle.SetMarginStart(12)
	pvTitle.SetMarginTop(10)
	pvTitle.SetMarginBottom(6)
	preview := gtk.NewBox(gtk.OrientationVertical, 0)
	preview.AddCSSClass("card")
	preview.SetHExpand(true)
	preview.Append(pvTitle)
	preview.Append(gtk.NewSeparator(gtk.OrientationHorizontal))
	preview.Append(pvScroll)

	panes := gtk.NewBox(gtk.OrientationHorizontal, 12)
	panes.SetHomogeneous(true)
	panes.SetVExpand(true)
	panes.Append(editor)
	panes.Append(preview)

	hint := gtk.NewLabel(tr("An image at the very start that is at least 480 px wide is shown as a banner across the channel header."))
	hint.AddCSSClass("dim-label")
	hint.AddCSSClass("caption")
	hint.SetWrap(true)
	hint.SetXAlign(0)
	e.status = gtk.NewLabel("")
	e.status.AddCSSClass("caption")
	e.status.SetXAlign(1)
	foot := gtk.NewBox(gtk.OrientationHorizontal, 12)
	hint.SetHExpand(true)
	foot.Append(hint)
	foot.Append(e.status)

	e.root = gtk.NewBox(gtk.OrientationVertical, 10)
	e.root.SetMarginTop(12)
	e.root.SetMarginBottom(12)
	e.root.SetMarginStart(12)
	e.root.SetMarginEnd(12)
	e.root.Append(panes)
	e.root.Append(foot)
	e.renderPreview()
	return e
}

func (e *descEditor) newTag(name, prop string, value any) *gtk.TextTag {
	t := gtk.NewTextTag(name)
	glib.InternObject(t).SetObjectProperty(prop, value)
	e.buffer.TagTable().Add(t)
	return t
}

func key(o any) uintptr { return glib.InternObject(o.(glib.Objector)).Native() }

// button is a flat toolbar button; fallback is used when the icon theme
// lacks icon.
func (e *descEditor) button(icon, fallback, tooltip string, onClick func()) *gtk.Button {
	b := gtk.NewButton()
	b.AddCSSClass("flat")
	b.SetChild(themedIcon(icon, fallback))
	b.SetTooltipText(tooltip)
	b.ConnectClicked(func() {
		onClick()
		e.view.GrabFocus()
	})
	return b
}

func themedIcon(names ...string) *gtk.Image {
	return gtk.NewImageFromGIcon(gio.NewThemedIconFromNames(names))
}

// styleToggle formats the selection, or what is typed next without one.
func (e *descEditor) styleToggle(icon, tooltip string, tag *gtk.TextTag, setTyping func(bool)) *gtk.ToggleButton {
	b := gtk.NewToggleButton()
	b.AddCSSClass("flat")
	b.SetIconName(icon)
	b.SetTooltipText(tooltip)
	b.ConnectToggled(func() {
		if e.busy {
			return
		}
		on := b.Active()
		setTyping(on)
		if start, end, ok := e.buffer.SelectionBounds(); ok {
			if on {
				e.buffer.ApplyTag(tag, start, end)
			} else {
				e.buffer.RemoveTag(tag, start, end)
			}
		}
		e.view.GrabFocus()
	})
	return b
}

// syncButtons shows the formatting at the cursor.
func (e *descEditor) syncButtons() {
	if e.boldBtn == nil {
		return
	}
	it := e.buffer.IterAtMark(e.buffer.GetInsert())
	if !it.StartsLine() {
		it.BackwardChar()
	}
	e.busy = true
	e.typing.bold, e.typing.italic = it.HasTag(e.bold), it.HasTag(e.italic)
	e.boldBtn.SetActive(e.typing.bold)
	e.italicBtn.SetActive(e.typing.italic)
	e.busy = false
}

// afterInsert formats typed text like its surroundings: the pending
// bold/italic, the line's heading/list tag, and a new list item after
// Enter in a list (Enter on an empty item ends the list).
func (e *descEditor) afterInsert(offset, n int) {
	e.busy = true
	defer func() { e.busy = false }()
	start := e.buffer.IterAtOffset(offset)
	end := e.buffer.IterAtOffset(offset + n)
	for _, f := range []struct {
		tag *gtk.TextTag
		on  bool
	}{{e.bold, e.typing.bold}, {e.italic, e.typing.italic}} {
		if f.on {
			e.buffer.ApplyTag(f.tag, start, end)
		} else {
			e.buffer.RemoveTag(f.tag, start, end)
		}
	}
	text := e.buffer.Text(start, end, true)
	firstLine := start.Line()
	kind := e.lineTag(firstLine)
	if kind != nil {
		e.applyLine(firstLine, kind)
	}
	if !strings.Contains(text, "\n") {
		return
	}
	lastLine := e.buffer.IterAtOffset(offset + n).Line()
	switch kind {
	case e.item:
		if e.lineText(firstLine) == "" {
			// Enter on an empty item: the list ends.
			e.clearLine(firstLine)
			e.clearLine(lastLine)
			return
		}
		for l := firstLine + 1; l <= lastLine; l++ {
			e.makeItem(l)
		}
	case e.heading:
		for l := firstLine + 1; l <= lastLine; l++ {
			e.clearLine(l)
		}
	}
}

// lineBounds returns the start and end (before the newline) of line.
func (e *descEditor) lineBounds(line int) (*gtk.TextIter, *gtk.TextIter) {
	start, _ := e.buffer.IterAtLine(line)
	end := start.Copy()
	if !end.EndsLine() {
		end.ForwardToLineEnd()
	}
	return start, end
}

// lineTag is the line's heading or item tag (nil for a paragraph).
func (e *descEditor) lineTag(line int) *gtk.TextTag {
	start, end := e.lineBounds(line)
	for it := start.Copy(); it.Compare(end) < 0; it.ForwardChar() {
		if it.HasTag(e.heading) {
			return e.heading
		}
		if it.HasTag(e.item) {
			return e.item
		}
	}
	return nil
}

// lineText is the line's text without the bullet.
func (e *descEditor) lineText(line int) string {
	start, end := e.lineBounds(line)
	return strings.TrimSpace(strings.TrimPrefix(e.buffer.Text(start, end, true), bullet))
}

func (e *descEditor) applyLine(line int, tag *gtk.TextTag) {
	start, end := e.lineBounds(line)
	end.ForwardChar() // include the newline so the margin covers the line
	e.buffer.ApplyTag(tag, start, end)
}

// clearLine makes line a plain paragraph (removing a bullet).
func (e *descEditor) clearLine(line int) {
	start, end := e.lineBounds(line)
	if e.buffer.Text(start, end, true) != "" && start.HasTag(e.bulletTag) {
		bEnd := start.Copy()
		bEnd.ForwardChars(len([]rune(bullet)))
		e.buffer.Delete(start, bEnd)
		start, end = e.lineBounds(line)
	}
	end.ForwardChar()
	e.buffer.RemoveTag(e.heading, start, end)
	e.buffer.RemoveTag(e.item, start, end)
}

func (e *descEditor) makeItem(line int) {
	start, _ := e.lineBounds(line)
	if !start.HasTag(e.bulletTag) {
		off := start.Offset()
		e.buffer.Insert(start, bullet)
		bs := e.buffer.IterAtOffset(off)
		be := e.buffer.IterAtOffset(off + len([]rune(bullet)))
		e.buffer.ApplyTag(e.bulletTag, bs, be)
	}
	ls, le := e.lineRange(line)
	e.buffer.RemoveTag(e.heading, ls, le)
	e.applyLine(line, e.item)
}

func (e *descEditor) lineRange(line int) (*gtk.TextIter, *gtk.TextIter) {
	start, end := e.lineBounds(line)
	end.ForwardChar()
	return start, end
}

// toggleLines switches the selected lines (or the cursor's line) to tag,
// or back to paragraphs if they all have it.
func (e *descEditor) toggleLines(tag *gtk.TextTag) {
	e.busy = true
	defer func() { e.busy = false }()
	start, end, ok := e.buffer.SelectionBounds()
	if !ok {
		start = e.buffer.IterAtMark(e.buffer.GetInsert())
		end = start
	}
	first, last := start.Line(), end.Line()
	all := true
	for l := first; l <= last; l++ {
		if e.lineTag(l) != tag {
			all = false
		}
	}
	for l := first; l <= last; l++ {
		if e.isImageLine(l) {
			continue
		}
		e.clearLine(l)
		if all {
			continue
		}
		if tag == e.item {
			e.makeItem(l)
		} else if e.lineText(l) != "" {
			e.applyLine(l, tag)
		}
	}
	e.schedulePreview()
}

func (e *descEditor) isImageLine(line int) bool {
	start, _ := e.lineBounds(line)
	return start.Paintable() != nil
}

// --- links ---------------------------------------------------------------

func (e *descEditor) linkButton() *gtk.MenuButton {
	entry := gtk.NewEntry()
	entry.SetPlaceholderText("https://")
	entry.SetWidthChars(30)
	apply := gtk.NewButtonWithLabel(tr("Apply"))
	apply.AddCSSClass("suggested-action")
	remove := gtk.NewButtonWithLabel(tr("Remove Link"))
	hint := gtk.NewLabel(tr("Select the text to link first."))
	hint.AddCSSClass("dim-label")
	hint.AddCSSClass("caption")

	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.SetMarginTop(6)
	box.SetMarginBottom(6)
	box.SetMarginStart(6)
	box.SetMarginEnd(6)
	row := gtk.NewBox(gtk.OrientationHorizontal, 6)
	row.Append(entry)
	row.Append(apply)
	box.Append(row)
	box.Append(remove)
	box.Append(hint)

	pop := gtk.NewPopover()
	pop.SetChild(box)
	btn := gtk.NewMenuButton()
	btn.AddCSSClass("flat")
	btn.SetChild(themedIcon("insert-link-symbolic", "emblem-symbolic-link"))
	btn.SetTooltipText(tr("Link"))
	btn.SetPopover(pop)

	pop.ConnectShow(func() {
		start, _, ok := e.buffer.SelectionBounds()
		apply.SetSensitive(ok)
		hint.SetVisible(!ok)
		entry.SetText("")
		remove.SetVisible(false)
		if ok {
			if href, tag := e.linkAt(start); tag != nil {
				entry.SetText(href)
				remove.SetVisible(true)
			}
		}
		entry.GrabFocus()
	})
	setLink := func(href string) {
		start, end, ok := e.buffer.SelectionBounds()
		if !ok {
			return
		}
		e.removeLinks(start, end)
		if href != "" {
			tag := gtk.NewTextTag("")
			glib.InternObject(tag).SetObjectProperty("underline", pango.UnderlineSingle)
			glib.InternObject(tag).SetObjectProperty("foreground", "#3584e4")
			e.buffer.TagTable().Add(tag)
			e.links[key(tag)] = href
			e.buffer.ApplyTag(tag, start, end)
		}
		pop.Popdown()
		e.schedulePreview()
	}
	apply.ConnectClicked(func() {
		href := strings.TrimSpace(entry.Text())
		if href != "" && !strings.Contains(href, ":") {
			href = "https://" + href
		}
		if !richtext.SafeLink(href) {
			entry.AddCSSClass("error")
			return
		}
		setLink(href)
	})
	entry.ConnectActivate(func() { apply.Activate() })
	entry.ConnectChanged(func() { entry.RemoveCSSClass("error") })
	remove.ConnectClicked(func() { setLink("") })
	return btn
}

func (e *descEditor) linkAt(it *gtk.TextIter) (string, *gtk.TextTag) {
	for _, t := range it.Tags() {
		if href, ok := e.links[key(t)]; ok {
			return href, t
		}
	}
	return "", nil
}

func (e *descEditor) removeLinks(start, end *gtk.TextIter) {
	for k := range e.links {
		e.buffer.TagTable().ForEach(func(t *gtk.TextTag) {
			if key(t) == k {
				e.buffer.RemoveTag(t, start, end)
			}
		})
	}
}

// --- images ----------------------------------------------------------------

func (e *descEditor) pickImage() {
	filter := gtk.NewFileFilter()
	filter.SetName(tr("Images"))
	filter.AddPixbufFormats()
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(filter.Object)
	fd := gtk.NewFileDialog()
	fd.SetTitle(tr("Insert Image"))
	fd.SetModal(true)
	fd.SetFilters(filters)
	fd.SetDefaultFilter(filter)
	fd.Open(context.Background(), windowOf(e.root), func(res gio.AsyncResulter) {
		file, err := fd.OpenFinish(res)
		if err != nil || file == nil {
			return
		}
		path := file.Path()
		budget := e.limit - len(e.HTML())
		e.status.SetText(tr("Preparing image…"))
		go func() {
			uri, err := chat.EncodeImage(path, budget)
			gioglib.IdleAdd(func() {
				e.status.SetText("")
				if err != nil {
					slog.Info("description: image not inserted", "file", path, "err", err)
					e.status.SetText(tr("The image does not fit into the description."))
					return
				}
				if data, ok := richtext.DecodeDataURI(uri); ok {
					e.insertImageAtCursor(data)
				}
			})
		}()
	})
}

// insertImageAtCursor puts the image on its own line at the cursor.
func (e *descEditor) insertImageAtCursor(data []byte) {
	e.busy = true
	defer func() { e.busy = false }()
	it := e.buffer.IterAtMark(e.buffer.GetInsert())
	if !it.StartsLine() {
		it.ForwardToLineEnd()
		e.buffer.Insert(it, "\n")
	}
	if !e.insertImage(it, data) {
		e.status.SetText(tr("This image could not be read."))
		return
	}
	if !it.EndsLine() || it.IsEnd() {
		e.buffer.Insert(it, "\n")
	}
	e.schedulePreview()
}

// insertImage inserts a scaled-down view of data at it (advancing it).
func (e *descEditor) insertImage(it *gtk.TextIter, data []byte) bool {
	loader := gdkpixbuf.NewPixbufLoader()
	if err := loader.Write(data); err != nil {
		return false
	}
	if err := loader.Close(); err != nil {
		return false
	}
	pb := loader.Pixbuf()
	if pb == nil {
		return false
	}
	if w, h := pb.Width(), pb.Height(); w > descImagePx {
		pb = pb.ScaleSimple(descImagePx, max(1, h*descImagePx/w), gdkpixbuf.InterpBilinear)
	}
	tex := gdk.NewTextureForPixbuf(pb)
	e.images[key(tex)] = data
	e.buffer.InsertPaintable(it, tex)
	return true
}

// --- model <-> buffer ------------------------------------------------------

func (e *descEditor) load(lines []descLine) {
	e.busy = true
	defer func() { e.busy = false }()
	for i, l := range lines {
		if i > 0 {
			e.buffer.Insert(e.buffer.EndIter(), "\n")
		}
		line := e.buffer.EndIter().Line()
		if l.Kind == lineImage {
			e.insertImage(e.buffer.EndIter(), l.Image)
			continue
		}
		for _, r := range l.Runs {
			off := e.buffer.EndIter().Offset()
			e.buffer.Insert(e.buffer.EndIter(), r.Text)
			start, end := e.buffer.IterAtOffset(off), e.buffer.EndIter()
			if r.Bold {
				e.buffer.ApplyTag(e.bold, start, end)
			}
			if r.Italic {
				e.buffer.ApplyTag(e.italic, start, end)
			}
			if r.Link != "" {
				tag := gtk.NewTextTag("")
				glib.InternObject(tag).SetObjectProperty("underline", pango.UnderlineSingle)
				glib.InternObject(tag).SetObjectProperty("foreground", "#3584e4")
				e.buffer.TagTable().Add(tag)
				e.links[key(tag)] = r.Link
				e.buffer.ApplyTag(tag, start, end)
			}
		}
		switch l.Kind {
		case lineHeading:
			e.applyLine(line, e.heading)
		case lineItem:
			e.makeItem(line)
		}
	}
	e.buffer.SetModified(false)
}

// lines reads the buffer back into the document model.
func (e *descEditor) lines() []descLine {
	var out []descLine
	for l := 0; l < e.buffer.LineCount(); l++ {
		start, end := e.lineBounds(l)
		line := descLine{}
		switch e.lineTag(l) {
		case e.heading:
			line.Kind = lineHeading
		case e.item:
			line.Kind = lineItem
		}
		var cur descRun
		var text []rune
		flush := func() {
			if len(text) > 0 {
				cur.Text = string(text)
				line.Runs = append(line.Runs, cur)
				text = text[:0]
			}
		}
		for it := start.Copy(); it.Compare(end) < 0; it.ForwardChar() {
			if p := it.Paintable(); p != nil {
				flush()
				if len(line.Runs) > 0 {
					out = append(out, line)
				}
				if data, ok := e.images[key(p)]; ok {
					out = append(out, descLine{Kind: lineImage, Image: data})
				}
				line = descLine{Kind: line.Kind}
				continue
			}
			if it.HasTag(e.bulletTag) {
				continue
			}
			href, _ := e.linkAt(it)
			r := descRun{Bold: it.HasTag(e.bold), Italic: it.HasTag(e.italic), Link: href}
			if r != (descRun{Bold: cur.Bold, Italic: cur.Italic, Link: cur.Link}) {
				flush()
				cur = r
			}
			text = append(text, rune(it.Char()))
		}
		flush()
		if len(line.Runs) > 0 || line.Kind == linePara && !e.isImageLine(l) {
			out = append(out, line)
		}
	}
	return trimLines(out)
}

// HTML is the edited description.
func (e *descEditor) HTML() string { return descriptionHTML(e.lines()) }

// Changed reports whether the user edited the description.
func (e *descEditor) Changed() bool { return e.buffer.Modified() }

func (e *descEditor) schedulePreview() {
	if e.previewTick != 0 {
		return
	}
	e.previewTick = gioglib.TimeoutAdd(previewDelayMs, func() bool {
		e.previewTick = 0
		e.renderPreview()
		return false
	})
}

func (e *descEditor) renderPreview() {
	for c := e.preview.FirstChild(); c != nil; c = e.preview.FirstChild() {
		e.preview.Remove(c)
	}
	html := e.HTML()
	if html == "" {
		empty := gtk.NewLabel(tr("No description"))
		empty.AddCSSClass("dim-label")
		e.preview.Append(empty)
	} else {
		e.preview.Append(richtext.Build(richtext.Parse(html), richtext.Options{MaxImageWidth: 360, MaxImageHeight: 360}))
	}
	e.status.RemoveCSSClass("error")
	if n := len(html); n > e.limit {
		e.status.AddCSSClass("error")
		e.status.SetText(tr("Too long for this server"))
	} else if e.status.Text() == tr("Too long for this server") {
		e.status.SetText("")
	}
}

// TooLong reports whether the description exceeds the server's limit.
func (e *descEditor) TooLong() bool { return len(e.HTML()) > e.limit }

// windowOf finds the toplevel window of w (nil when not shown).
func windowOf(w gtk.Widgetter) *gtk.Window {
	root := gtk.BaseWidget(w).Root()
	if root == nil {
		return nil
	}
	switch win := glib.InternObject(root).Cast().(type) {
	case *adw.ApplicationWindow:
		return &win.ApplicationWindow.Window
	case *adw.Window:
		return &win.Window
	case *gtk.ApplicationWindow:
		return &win.Window
	case *gtk.Window:
		return win
	}
	return nil
}
