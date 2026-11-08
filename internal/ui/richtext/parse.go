// Package richtext renders the HTML subset Mumble uses for messages and
// channel descriptions as GTK widgets: formatted paragraphs (Pango
// markup) and embedded images. Only inline formatting, links with safe
// schemes and data: images survive; remote images are never fetched (a
// privacy leak to whoever hosts them) and everything else becomes text.
package richtext

import (
	"encoding/base64"
	"encoding/xml"
	"html"
	"log/slog"
	"net/url"
	"strings"
)

// maxImageBytes bounds one decoded embedded image.
const maxImageBytes = 4 << 20

// Block is one paragraph (Markup) or one embedded image (Image).
type Block struct {
	Markup string // Pango markup; "" for an image
	Image  []byte // decoded image file (PNG, JPEG, …)
}

// inline maps HTML inline elements to Pango markup tags.
var inline = map[string]string{
	"b": "b", "strong": "b",
	"i": "i", "em": "i", "cite": "i", "var": "i",
	"u": "u", "ins": "u",
	"s": "s", "strike": "s", "del": "s",
	"code": "tt", "tt": "tt", "kbd": "tt", "samp": "tt",
	"sub": "sub", "sup": "sup",
	"big": "big", "small": "small",
	"h1": "b", "h2": "b", "h3": "b", "h4": "b", "h5": "b", "h6": "b",
}

// block elements start and end on their own line.
var block = map[string]bool{
	"p": true, "div": true, "ul": true, "ol": true, "li": true, "table": true,
	"tr": true, "blockquote": true, "pre": true, "hr": true, "center": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"dl": true, "dt": true, "dd": true, "address": true, "section": true,
}

// skipped elements contribute nothing (not even their text).
var skipped = map[string]bool{"script": true, "style": true, "head": true, "title": true}

// Parse converts Mumble HTML into blocks. Text without any markup keeps
// its line breaks (some clients send plain text).
func Parse(s string) []Block {
	if !strings.Contains(s, "<") {
		return plainBlocks(html.UnescapeString(s))
	}
	p := &parser{}
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
			p.end(t.Name.Local)
		}
	}
	p.flush()
	return p.blocks
}

func plainBlocks(s string) []Block {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return []Block{{Markup: html.EscapeString(s)}}
}

// open is an open Pango tag: its HTML element and the markup that opened it.
type open struct {
	element string
	tag     string // pango tag name
	start   string // full opening markup
}

type parser struct {
	blocks []Block
	b      strings.Builder
	stack  []open
	skip   int  // depth inside skipped elements
	pre    int  // depth inside <pre>
	space  bool // a collapsed space is pending
	bol    bool // at the beginning of a line
}

func (p *parser) text(s string) {
	if p.pre > 0 {
		p.write(s)
		return
	}
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			p.space = true
			continue
		}
		if p.space && !p.bol && p.b.Len() > 0 {
			p.b.WriteByte(' ')
		}
		p.space = false
		p.write(string(r))
	}
}

func (p *parser) write(s string) {
	p.b.WriteString(html.EscapeString(s))
	p.bol = strings.HasSuffix(s, "\n")
}

func (p *parser) newline() {
	if p.b.Len() > 0 && !p.bol {
		p.b.WriteString("\n")
		p.bol = true
	}
	p.space = false
}

func (p *parser) start(t xml.StartElement) {
	name := strings.ToLower(t.Name.Local)
	if skipped[name] {
		p.skip++
		return
	}
	if p.skip > 0 {
		return
	}
	if block[name] {
		p.newline()
	}
	switch name {
	case "br":
		p.b.WriteString("\n")
		p.bol = true
		p.space = false
		return
	case "li":
		p.b.WriteString("• ")
		p.bol = false
	case "pre":
		p.pre++
	case "img":
		p.image(attr(t, "src"))
		return
	case "a":
		if href := attr(t, "href"); safeLink(href) {
			p.push(name, "a", `<a href="`+html.EscapeString(href)+`">`)
		}
		return
	}
	if tag, ok := inline[name]; ok {
		p.push(name, tag, "<"+tag+">")
	}
}

func (p *parser) end(element string) {
	name := strings.ToLower(element)
	if skipped[name] {
		if p.skip > 0 {
			p.skip--
		}
		return
	}
	if p.skip > 0 {
		return
	}
	if name == "pre" && p.pre > 0 {
		p.pre--
	}
	p.pop(name)
	if block[name] {
		p.newline()
	}
}

func (p *parser) push(element, tag, start string) {
	// A space pending from the text before belongs in front of the tag.
	if p.space && !p.bol && p.b.Len() > 0 {
		p.b.WriteByte(' ')
	}
	p.space = false
	p.b.WriteString(start)
	p.stack = append(p.stack, open{element: element, tag: tag, start: start})
}

// pop closes the innermost open tag of element, closing and reopening the
// tags opened inside it so the markup stays well nested.
func (p *parser) pop(element string) {
	i := len(p.stack) - 1
	for i >= 0 && p.stack[i].element != element {
		i--
	}
	if i < 0 {
		return
	}
	for j := len(p.stack) - 1; j >= i; j-- {
		p.b.WriteString("</" + p.stack[j].tag + ">")
	}
	reopen := append([]open(nil), p.stack[i+1:]...)
	p.stack = p.stack[:i]
	for _, o := range reopen {
		p.b.WriteString(o.start)
		p.stack = append(p.stack, o)
	}
}

// flush ends the current paragraph (closing its open tags).
func (p *parser) flush() {
	for j := len(p.stack) - 1; j >= 0; j-- {
		p.b.WriteString("</" + p.stack[j].tag + ">")
	}
	m := strings.TrimRight(strings.TrimLeft(p.b.String(), "\n "), "\n ")
	if visible(m) {
		p.blocks = append(p.blocks, Block{Markup: m})
	}
	p.b.Reset()
	p.bol = true
	p.space = false
	// Formatting continues in the next paragraph.
	for _, o := range p.stack {
		p.b.WriteString(o.start)
	}
}

// image adds an embedded image as its own block. A remote image is not
// fetched (that would tell its host who reads the message); it becomes a
// link to open in the browser.
func (p *parser) image(src string) {
	data, ok := decodeDataURI(src)
	if ok {
		p.flush()
		p.blocks = append(p.blocks, Block{Image: data})
		return
	}
	if src == "" {
		return
	}
	if safeLink(src) {
		p.push("img", "a", `<a href="`+html.EscapeString(src)+`">`)
		p.text("[image]")
		p.pop("img")
		return
	}
	slog.Debug("richtext: image source not supported", "src", truncate(src, 48))
	p.text(" [image] ")
}

// decodeDataURI decodes a data: image URI, tolerating the variants found
// in the wild: any case, extra parameters before ";base64", URL-encoded
// or wrapped payloads, missing padding and URL-safe base64.
func decodeDataURI(src string) ([]byte, bool) {
	src = strings.TrimSpace(src)
	if len(src) < 11 || !strings.EqualFold(src[:11], "data:image/") {
		return nil, false
	}
	meta, payload, ok := strings.Cut(src[5:], ",")
	if !ok || !strings.Contains(strings.ToLower(meta), ";base64") {
		return nil, false
	}
	if strings.Contains(payload, "%") {
		if u, err := url.PathUnescape(payload); err == nil {
			payload = u
		}
	}
	payload = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, payload)
	if base64.StdEncoding.DecodedLen(len(payload)) > maxImageBytes {
		return nil, false
	}
	trimmed := strings.TrimRight(payload, "=")
	for _, dec := range []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		func(s string) ([]byte, error) { return base64.RawStdEncoding.DecodeString(trimmed) },
		func(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(trimmed) },
	} {
		if data, err := dec(payload); err == nil && len(data) > 0 {
			return data, true
		}
	}
	return nil, false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// safeLink allows only schemes a click may open.
func safeLink(href string) bool {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto", "mumble":
		return true
	}
	return false
}

func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if strings.EqualFold(a.Name.Local, name) {
			return a.Value
		}
	}
	return ""
}

// visible reports whether markup contains any text outside its tags.
func visible(markup string) bool {
	inTag := false
	for _, r := range markup {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag && r != ' ' && r != '\n':
			return true
		}
	}
	return false
}
