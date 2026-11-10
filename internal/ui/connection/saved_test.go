package connection

import (
	"testing"

	"io.github/murmel/internal/settings"
)

func TestSavedName(t *testing.T) {
	cases := []struct {
		user, host string
		port       int
		want       string
	}{
		{"simon", "mumble.example.org", 64738, "simon@mumble.example.org"},
		{"simon", "example.org", 1234, "simon@example.org:1234"},
		{"", "example.org", 64738, "example.org"},
	}
	for _, c := range cases {
		if got := savedName(c.user, c.host, c.port); got != c.want {
			t.Errorf("savedName(%q,%q,%d) = %q, want %q", c.user, c.host, c.port, got, c.want)
		}
	}
}

func TestUpsertAndRemoveServer(t *testing.T) {
	a := settings.ServerEntry{Name: "a@x", Host: "x", Port: 64738, Username: "a"}
	b := settings.ServerEntry{Name: "b@y", Host: "y", Port: 64738, Username: "b"}
	list := upsertServer(nil, a)
	list = upsertServer(list, b)
	if len(list) != 2 || list[0].Username != "a" || list[1].Username != "b" {
		t.Fatalf("new connections go to the end: %+v", list)
	}
	b2 := b
	b2.IdentityID = "id-1"
	list = upsertServer(list, b2)
	if len(list) != 2 || list[1].IdentityID != "id-1" || list[0].Username != "a" {
		t.Fatalf("same account must be replaced in place: %+v", list)
	}
	list = removeServer(list, "X", 64738, "a") // host compare is case-insensitive
	if len(list) != 1 || list[0].Username != "b" {
		t.Fatalf("remove: %+v", list)
	}
}

func TestMoveServer(t *testing.T) {
	mk := func(names ...string) []settings.ServerEntry {
		var l []settings.ServerEntry
		for _, n := range names {
			l = append(l, settings.ServerEntry{Name: n, Host: n, Port: 64738, Username: n})
		}
		return l
	}
	names := func(l []settings.ServerEntry) string {
		s := ""
		for _, e := range l {
			s += e.Name
		}
		return s
	}
	cases := []struct {
		from, to int
		want     string
	}{
		{0, 3, "bcda"},
		{3, 0, "dabc"},
		{1, 2, "acbd"},
		{2, 1, "acbd"},
		{1, 1, "abcd"},
		{-1, 2, "abcd"},
		{0, 4, "abcd"},
	}
	for _, c := range cases {
		in := mk("a", "b", "c", "d")
		if got := names(moveServer(in, c.from, c.to)); got != c.want {
			t.Errorf("moveServer(%d, %d) = %s, want %s", c.from, c.to, got, c.want)
		}
		if names(in) != "abcd" {
			t.Errorf("moveServer(%d, %d) changed its input: %s", c.from, c.to, names(in))
		}
	}
	if i := serverIndex(mk("a", "b"), "B", 64738, "b"); i != 1 {
		t.Errorf("serverIndex = %d, want 1", i)
	}
}
