package richtext

import (
	"encoding/xml"
	"strings"
)

// blockElements start a new line in PlainText, like a paragraph does.
var blockElements = map[string]bool{
	"address": true, "article": true, "aside": true, "audio": true,
	"blockquote": true, "canvas": true, "dd": true, "div": true, "dl": true,
	"fieldset": true, "figcaption": true, "figure": true, "footer": true,
	"form": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
	"h6": true, "header": true, "hgroup": true, "hr": true, "noscript": true,
	"ol": true, "output": true, "p": true, "pre": true, "section": true,
	"table": true, "tfoot": true, "ul": true, "video": true,
}

// PlainText returns the text of message HTML or of Pango markup: tags are
// dropped, entities decoded, and <br> and block elements start a new line.
// Text after markup the decoder cannot read is left out.
func PlainText(markup string) string {
	if markup == "" {
		return ""
	}
	d := xml.NewDecoder(strings.NewReader(markup))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity

	var b strings.Builder
	atBreak := false
	for {
		tok, err := d.Token()
		if err != nil {
			return b.String()
		}
		switch t := tok.(type) {
		case xml.CharData:
			if len(t) > 0 {
				b.Write(t)
				atBreak = false
			}
		case xml.StartElement:
			if t.Name.Local == "br" || (blockElements[t.Name.Local] && !atBreak) {
				b.WriteByte('\n')
				atBreak = true
			}
		}
	}
}
