package chat

import (
	"encoding/base64"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"io.github/murmel/internal/bus"
)

func TestSpansToHTML(t *testing.T) {
	for _, tt := range []struct {
		name  string
		spans []span
		want  string
	}{
		{"plain escaped", []span{{text: "a < b & c"}}, "a &lt; b &amp; c"},
		{"newline", []span{{text: "one\ntwo"}}, "one<br/>two"},
		{"bold run", []span{{text: "hi "}, {text: "there", bold: true}, {text: "!"}}, "hi <b>there</b>!"},
		{"merge", []span{{text: "a", bold: true}, {text: "b", bold: true}}, "<b>ab</b>"},
		{"nested", []span{{text: "x", bold: true, italic: true}, {text: "y", italic: true}}, "<b><i>x</i></b><i>y</i>"},
		{"markup stays text", []span{{text: "<b>no</b>"}}, "&lt;b&gt;no&lt;/b&gt;"},
	} {
		if got := spansToHTML(tt.spans); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestTrimSpans(t *testing.T) {
	got := trimSpans([]span{{text: "  \n"}, {text: " hi ", bold: true}, {text: "\n "}})
	if len(got) != 1 || got[0].text != "hi" || !got[0].bold {
		t.Fatalf("got %+v", got)
	}
	if len(trimSpans([]span{{text: " \n "}})) != 0 {
		t.Fatal("whitespace-only must trim to nothing")
	}
}

func TestConversationKeys(t *testing.T) {
	reg := bus.UserInfo{Session: 5, UserID: 12, Name: "anna"}
	guest := bus.UserInfo{Session: 6, Name: "bob"}
	if keyForUser(reg) != "u:12" || keyForUser(guest) != "n:bob" {
		t.Fatalf("keys: %q %q", keyForUser(reg), keyForUser(guest))
	}
	st := bus.State{SelfSession: 1, Users: map[uint32]bus.UserInfo{
		1: {Session: 1, Name: "me"},
		9: {Session: 9, UserID: 12, Name: "anna"}, // reconnected: new session
	}}
	if s, ok := sessionFor(st, "u:12"); !ok || s != 9 {
		t.Fatalf("sessionFor registered = %d %v", s, ok)
	}
	if _, ok := sessionFor(st, "n:bob"); ok {
		t.Fatal("offline user must not resolve")
	}
}

func TestScaleTo(t *testing.T) {
	for _, tt := range []struct{ w, h, side, ww, wh int }{
		{1600, 1200, 800, 800, 600},
		{1200, 1600, 800, 600, 800},
		{300, 200, 800, 300, 200},
	} {
		if w, h := scaleTo(tt.w, tt.h, tt.side); w != tt.ww || h != tt.wh {
			t.Errorf("scaleTo(%d,%d,%d) = %d,%d", tt.w, tt.h, tt.side, w, h)
		}
	}
}

func TestEncodeImageFitsLimit(t *testing.T) {
	const path = "../../../assets/screenshots/0.3.2/server-connected.png"
	for _, limit := range []int{defaultImageMessageLength, 40000} {
		uri, err := encodeImage(path, limit)
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if len(uri)+imageMarkupOverhead > limit || !strings.HasPrefix(uri, "data:image/jpeg;base64,") {
			t.Fatalf("limit %d: %d bytes", limit, len(uri))
		}
		data, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:image/jpeg;base64,"))
		if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 {
			t.Fatalf("limit %d: not a JPEG", limit)
		}
		t.Logf("limit %d → %d bytes", limit, len(uri))
	}
	if _, err := encodeImage(path, 2000); err == nil {
		t.Fatal("an impossible limit must fail")
	}
}

// TestSpansToHTMLIsXML: servers parse long (image) messages as XML.
func TestSpansToHTMLIsXML(t *testing.T) {
	out := spansToHTML([]span{{text: "a & b\n<c> 'd' \"e\"", bold: true}, {text: "\nf", italic: true}})
	d := xml.NewDecoder(strings.NewReader("<document>" + out + "</document>"))
	for {
		_, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("not well-formed XML: %v (%s)", err, out)
		}
	}
}
