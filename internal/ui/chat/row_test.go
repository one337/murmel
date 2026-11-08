package chat

import "testing"

func TestRowCSSClass(t *testing.T) {
	cases := []struct {
		kind rowKind
		want string
	}{
		{rowMessage, ""},
		{rowPrivate, ""},
		{rowError, "error"},
		{rowNotice, "dim-label"},
	}
	for _, tc := range cases {
		if got := rowCSSClass(tc.kind); got != tc.want {
			t.Fatalf("rowCSSClass(%d) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestErrorRowText(t *testing.T) {
	if got := errorRowText(""); got != "Message not delivered." {
		t.Fatalf("empty reason = %q", got)
	}
	if got := errorRowText("message too long"); got != "Message not delivered: message too long" {
		t.Fatalf("reason = %q", got)
	}
}
