package ui

import (
	"bytes"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

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
			want: ConnectRequest{Host: "example.com", Port: 64738},
			ok:   true,
		},
		{
			name: "username, port and channel path",
			raw:  "mumble://alice@example.com:64738/Lobby/Sub",
			want: ConnectRequest{
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
			want: ConnectRequest{Host: "example.com", Port: 1234},
			ok:   true,
		},
		{
			name: "tls scheme alias",
			raw:  "mumbles://example.com",
			want: ConnectRequest{Host: "example.com", Port: 64738},
			ok:   true,
		},
		{
			name: "query string ignored",
			raw:  "mumble://example.com?version=1.2.0",
			want: ConnectRequest{Host: "example.com", Port: 64738},
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
		t.Errorf("tr identity without a translation, got %q", got)
	}
}

func TestRequestConnectDoesNotLogCredentials(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	req, ok := ParseConnectURL("mumble://alice:geheim@example.com/")
	if !ok {
		t.Fatal("ParseConnectURL failed")
	}
	(&App{}).RequestConnect(req)

	out := buf.String()
	if !strings.Contains(out, "example.com") {
		t.Fatalf("log line missing host: %q", out)
	}
	if strings.Contains(out, "geheim") {
		t.Fatalf("log line contains the password: %q", out)
	}
}
