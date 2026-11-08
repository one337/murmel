package ui

import "reflect"
import "testing"

func TestParseConnectURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want ConnectRequest
		ok   bool
	}{
		{
			name: "host only",
			raw:  "mumble://example.com",
			want: ConnectRequest{URL: "mumble://example.com", Host: "example.com", Port: 64738},
			ok:   true,
		},
		{
			name: "username, port and channel path",
			raw:  "mumble://alice@example.com:64738/Lobby/Sub",
			want: ConnectRequest{
				URL:         "mumble://alice@example.com:64738/Lobby/Sub",
				Host:        "example.com",
				Port:        64738,
				Username:    "alice",
				ChannelPath: []string{"Lobby", "Sub"},
			},
			ok: true,
		},
		{
			name: "password in userinfo",
			raw:  "mumble://alice:secret@example.com",
			want: ConnectRequest{
				URL:      "mumble://alice:secret@example.com",
				Host:     "example.com",
				Port:     64738,
				Username: "alice",
				Password: "secret",
			},
			ok: true,
		},
		{
			name: "explicit port overrides default",
			raw:  "mumble://example.com:1234",
			want: ConnectRequest{URL: "mumble://example.com:1234", Host: "example.com", Port: 1234},
			ok:   true,
		},
		{
			name: "tls scheme alias",
			raw:  "mumbles://example.com",
			want: ConnectRequest{URL: "mumbles://example.com", Host: "example.com", Port: 64738},
			ok:   true,
		},
		{
			name: "query string ignored",
			raw:  "mumble://example.com?version=1.2.0",
			want: ConnectRequest{URL: "mumble://example.com?version=1.2.0", Host: "example.com", Port: 64738},
			ok:   true,
		},
		{name: "wrong scheme", raw: "https://example.com", ok: false},
		{name: "no host", raw: "mumble://", ok: false},
		{name: "not a url", raw: "example.com", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseConnectURL(tt.raw)
			if ok != tt.ok {
				t.Fatalf("ParseConnectURL(%q) ok = %v, want %v", tt.raw, ok, tt.ok)
			}
			if !ok {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseConnectURL(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestTRReturnsInput(t *testing.T) {
	if got := tr("Not connected"); got != "Not connected" {
		t.Errorf("tr identity until T27 wires gettext, got %q", got)
	}
}
