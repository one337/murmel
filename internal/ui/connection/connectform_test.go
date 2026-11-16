package connection

import "testing"

func TestRememberIdentity(t *testing.T) {
	for _, tc := range []struct {
		listed bool
		id     string
		want   bool
	}{
		{true, "abc", true},  // a certificate
		{true, "", true},     // anonymous, chosen from a complete list
		{false, "abc", true}, // imported or created despite the read error
		{false, "", false},   // the list was unreadable: keep the last one
	} {
		if got := rememberIdentity(tc.listed, tc.id); got != tc.want {
			t.Errorf("rememberIdentity(%v, %q) = %v, want %v", tc.listed, tc.id, got, tc.want)
		}
	}
}
