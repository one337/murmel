package chat

import (
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
		{"newline", []span{{text: "one\ntwo"}}, "one<br>two"},
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
