package chat

import (
	"html"
	"strings"
)

// span is a run of composer text with uniform formatting.
type span struct {
	text         string
	bold, italic bool
}

// spansToHTML serializes composer spans into the HTML subset Mumble
// clients render: escaped text, <b>/<i> for formatting, <br/> for line
// breaks. Adjacent spans with the same formatting merge. The output is
// well-formed XML: servers parse long messages (images) as XML and drop
// them when that fails.
func spansToHTML(spans []span) string {
	var b strings.Builder
	open := span{}
	closeTags := func() {
		if open.italic {
			b.WriteString("</i>")
		}
		if open.bold {
			b.WriteString("</b>")
		}
		open = span{}
	}
	for _, s := range spans {
		if s.text == "" {
			continue
		}
		if s.bold != open.bold || s.italic != open.italic {
			closeTags()
			if s.bold {
				b.WriteString("<b>")
			}
			if s.italic {
				b.WriteString("<i>")
			}
			open = span{bold: s.bold, italic: s.italic}
		}
		lines := strings.Split(s.text, "\n")
		for i, line := range lines {
			if i > 0 {
				b.WriteString("<br/>")
			}
			b.WriteString(html.EscapeString(line))
		}
	}
	closeTags()
	return b.String()
}

// trimSpans removes leading and trailing whitespace across the spans.
func trimSpans(spans []span) []span {
	for len(spans) > 0 {
		t := strings.TrimLeft(spans[0].text, " \t\n")
		if t != "" {
			spans[0].text = t
			break
		}
		spans = spans[1:]
	}
	for len(spans) > 0 {
		last := len(spans) - 1
		t := strings.TrimRight(spans[last].text, " \t\n")
		if t != "" {
			spans[last].text = t
			break
		}
		spans = spans[:last]
	}
	return spans
}
