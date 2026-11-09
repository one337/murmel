package admin

import (
	"encoding/base64"
	"encoding/xml"
	"html"
	"net/http"
	"strings"

	"io.github/murmel/internal/ui/richtext"
)

// The description editor works on a small document model: what its
// toolbar can produce. Paragraphs, headings and list items hold runs of
// text that may be bold, italic and/or a link; images stand on their own
// line. Existing descriptions are imported into it (anything else becomes
// plain text) and it is written back as well-formed XML, which murmur
// requires for long or image-bearing text.

type lineKind uint8

const (
	linePara lineKind = iota
	lineHeading
	lineItem
	lineImage
)

type descRun struct {
	Text         string
	Bold, Italic bool
	Link         string
}

type descLine struct {
	Kind  lineKind
	Runs  []descRun
	Image []byte // lineImage: the encoded image file
}

// parseDescription imports Mumble HTML into lines.
func parseDescription(s string) []descLine {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if !strings.Contains(s, "<") {
		var lines []descLine
		for _, l := range strings.Split(html.UnescapeString(s), "\n") {
			lines = append(lines, descLine{Runs: []descRun{{Text: l}}})
		}
		return trimLines(lines)
	}
	p := &descParser{}
	d := xml.NewDecoder(strings.NewReader(s))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	for {
		tok, err := d.Token()
		if tok == nil || err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.CharData:
			if p.skip == 0 {
				p.text(string(t))
			}
		case xml.StartElement:
			p.start(t)
		case xml.EndElement:
			p.end(strings.ToLower(t.Name.Local))
		}
	}
	p.breakLine()
	return trimLines(p.lines)
}

type descParser struct {
	lines  []descLine
	cur    descLine
	bold   int
	italic int
	links  []string
	skip   int
	space  bool // a collapsed whitespace is pending
}

func (p *descParser) link() string {
	if len(p.links) == 0 {
		return ""
	}
	return p.links[len(p.links)-1]
}

func (p *descParser) text(s string) {
	// HTML whitespace collapsing.
	var b strings.Builder
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			p.space = true
			continue
		}
		if p.space {
			// The collapsed space stays with the text before it.
			if b.Len() > 0 {
				b.WriteByte(' ')
			} else if n := len(p.cur.Runs); n > 0 {
				p.cur.Runs[n-1].Text += " "
			}
		}
		p.space = false
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return
	}
	run := descRun{Text: b.String(), Bold: p.bold > 0, Italic: p.italic > 0, Link: p.link()}
	if n := len(p.cur.Runs); n > 0 {
		last := &p.cur.Runs[n-1]
		if last.Bold == run.Bold && last.Italic == run.Italic && last.Link == run.Link {
			last.Text += run.Text
			return
		}
	}
	p.cur.Runs = append(p.cur.Runs, run)
}

// breakLine ends the current line (if it has content) and starts a
// paragraph.
func (p *descParser) breakLine() {
	if len(p.cur.Runs) > 0 {
		p.lines = append(p.lines, p.cur)
	}
	p.cur = descLine{}
	p.space = false
}

func (p *descParser) start(t xml.StartElement) {
	switch name := strings.ToLower(t.Name.Local); name {
	case "script", "style", "head", "title":
		p.skip++
	case "b", "strong":
		p.bold++
	case "i", "em":
		p.italic++
	case "a":
		href := strings.TrimSpace(attrOf(t, "href"))
		if !richtext.SafeLink(href) {
			href = ""
		}
		p.links = append(p.links, href)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		p.breakLine()
		p.cur.Kind = lineHeading
	case "li":
		p.breakLine()
		p.cur.Kind = lineItem
	case "br":
		kind := p.cur.Kind
		p.breakLine()
		if kind == lineItem {
			p.cur.Kind = lineItem
		}
	case "p", "div", "ul", "ol", "table", "tr", "blockquote", "pre", "center", "hr":
		p.breakLine()
	case "img":
		if data, ok := richtext.DecodeDataURI(attrOf(t, "src")); ok {
			p.breakLine()
			p.lines = append(p.lines, descLine{Kind: lineImage, Image: data})
		}
	}
}

func (p *descParser) end(name string) {
	switch name {
	case "script", "style", "head", "title":
		if p.skip > 0 {
			p.skip--
		}
	case "b", "strong":
		if p.bold > 0 {
			p.bold--
		}
	case "i", "em":
		if p.italic > 0 {
			p.italic--
		}
	case "a":
		if len(p.links) > 0 {
			p.links = p.links[:len(p.links)-1]
		}
	case "h1", "h2", "h3", "h4", "h5", "h6", "li", "p", "div", "ul", "ol", "table", "tr", "blockquote", "pre", "center":
		p.breakLine()
	}
}

func attrOf(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if strings.EqualFold(a.Name.Local, name) {
			return a.Value
		}
	}
	return ""
}

// trimLines drops empty lines at both ends and trailing spaces in runs.
func trimLines(lines []descLine) []descLine {
	for len(lines) > 0 && lines[0].empty() {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1].empty() {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func (l descLine) empty() bool {
	if l.Kind == lineImage {
		return false
	}
	for _, r := range l.Runs {
		if strings.TrimSpace(r.Text) != "" {
			return false
		}
	}
	return true
}

// descriptionHTML writes lines as well-formed HTML/XML.
func descriptionHTML(lines []descLine) string {
	var b strings.Builder
	inList := false
	for _, l := range lines {
		if l.Kind != lineItem && inList {
			b.WriteString("</ul>")
			inList = false
		}
		switch l.Kind {
		case lineImage:
			mime := http.DetectContentType(l.Image)
			if !strings.HasPrefix(mime, "image/") {
				mime = "image/png"
			}
			b.WriteString(`<img src="data:` + mime + `;base64,` + base64.StdEncoding.EncodeToString(l.Image) + `"/>`)
		case lineHeading:
			b.WriteString("<h2>" + runsHTML(l.Runs) + "</h2>")
		case lineItem:
			if !inList {
				b.WriteString("<ul>")
				inList = true
			}
			b.WriteString("<li>" + runsHTML(l.Runs) + "</li>")
		default:
			if l.empty() {
				b.WriteString("<br/>")
			} else {
				b.WriteString("<p>" + runsHTML(l.Runs) + "</p>")
			}
		}
	}
	if inList {
		b.WriteString("</ul>")
	}
	return b.String()
}

func runsHTML(runs []descRun) string {
	var b strings.Builder
	for _, r := range runs {
		if r.Text == "" {
			continue
		}
		t := html.EscapeString(r.Text)
		if r.Italic {
			t = "<i>" + t + "</i>"
		}
		if r.Bold {
			t = "<b>" + t + "</b>"
		}
		if r.Link != "" {
			t = `<a href="` + html.EscapeString(r.Link) + `">` + t + "</a>"
		}
		b.WriteString(t)
	}
	return b.String()
}
