package ui

import "testing"

// FuzzParseConnectURL checks that every URL ParseConnectURL accepts names
// a host and a valid port.
func FuzzParseConnectURL(f *testing.F) {
	f.Add("mumble://user:secret@example.org:64738/Lobby/Sub?version=1.2.0")
	f.Add("mumbles://[2001:db8::1]:1/")
	f.Add("mumble://example.org:65536")
	f.Add("mumble://example.org:0")
	f.Add("mumble://:64738")
	f.Fuzz(func(t *testing.T, raw string) {
		req, ok := ParseConnectURL(raw)
		if !ok {
			return
		}
		if req.Host == "" {
			t.Fatalf("%q accepted without a host", raw)
		}
		if req.Port < 1 || req.Port > 65535 {
			t.Fatalf("%q accepted with port %d", raw, req.Port)
		}
	})
}
