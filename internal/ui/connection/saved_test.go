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
	if len(list) != 2 || list[0].Username != "b" {
		t.Fatalf("most recent first: %+v", list)
	}
	a2 := a
	a2.IdentityID = "id-1"
	list = upsertServer(list, a2)
	if len(list) != 2 || list[0].IdentityID != "id-1" {
		t.Fatalf("same account must be replaced and moved to front: %+v", list)
	}
	list = removeServer(list, "X", 64738, "a") // host compare is case-insensitive
	if len(list) != 1 || list[0].Username != "b" {
		t.Fatalf("remove: %+v", list)
	}
}
