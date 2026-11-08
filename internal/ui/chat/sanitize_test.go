package chat

import (
	"strings"
	"testing"
)

func TestPlainTextStripsTagsAndEntities(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"plain untouched", "hello world", "hello world"},
		{"bold", "<b>BOLD</b>", "BOLD"},
		{
			"htmlstrip scenario message",
			"<b>BOLD</b><img src='http://localhost/x.png'>plain",
			"BOLDplain",
		},
		{
			"image with attributes and nested entities",
			`<a href="http://evil/&amp;">link</a><img alt="x&gt;y" src="http://localhost/e.png"/>tail`,
			"linktail",
		},
		{"entities decoded", "&amp;&lt;&gt;&quot;", "&<>\""},
		{"italic and underline stripped", "<i>it</i> <u>un</u>", "it un"},
		{"span with style stripped", `<span style="color:red">r</span>`, "r"},
		{"br becomes newline", "a<br>b", "a\nb"},
		{"paragraph becomes newline", "<p>a</p><p>b</p>", "\na\nb"},
		{"unclosed tag tolerated", "<b>bold", "bold"},
		// Tag stripped, inner text survives as INERT characters: rows
		// render via SetText on markup-disabled labels, so "alert(1)"
		// cannot execute or style anything.
		{"script tag stripped, content inert", "x<script>alert(1)</script>y", "xalert(1)y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PlainText(tc.in); got != tc.want {
				t.Fatalf("PlainText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestPlainTextDropsImageOnlyMessage: a message that is ONLY an <img> must
// sanitize to the empty string — there is no text left to render and no URL
// is ever handed to a widget.
func TestPlainTextDropsImageOnlyMessage(t *testing.T) {
	got := PlainText(`<img src="http://localhost/x.png" width="1" height="1">`)
	if got != "" {
		t.Fatalf("image-only message sanitized to %q, want empty", got)
	}
}

// TestPlainTextRTLEmojiRoundtrip: bidirectional text and astral-plane
// codepoints must survive sanitization byte-identically.
func TestPlainTextRTLEmojiRoundtrip(t *testing.T) {
	const in = "مرحبا 🌍 <b>bold</b> שלום 👨‍👩‍👧‍👦"
	const want = "مرحبا 🌍 bold שלום 👨‍👩‍👧‍👦"
	if got := PlainText(in); got != want {
		t.Fatalf("PlainText RTL/emoji = %q, want %q", got, want)
	}
}

// TestPlainTextDecodesEntitiesToInertText: entity-encoded angle brackets
// decode to literal characters ("<b>" from "&lt;b&gt;"). This is safe by
// construction: chat rows render via SetText on markup-disabled labels
// (grep-verified: no SetMarkup/SetUseMarkup(true) anywhere in chat/), so
// decoded text is never interpreted as markup.
func TestPlainTextDecodesEntitiesToInertText(t *testing.T) {
	if got := PlainText("&lt;b&gt;"); got != "<b>" {
		t.Fatalf("PlainText(\"&lt;b&gt;\") = %q, want %q", got, "<b>")
	}
	// Raw tags never survive as tags — only their inner text does.
	inputs := []string{
		"<b>", "</b>", "<img src=x>", "<<b>nested</b>>",
		"<foo bar='<b>'>text</foo>",
	}
	for _, in := range inputs {
		if got := PlainText(in); strings.Contains(got, "<b>") || strings.Contains(got, "<img") {
			t.Fatalf("PlainText(%q) = %q retains markup", in, got)
		}
	}
}
