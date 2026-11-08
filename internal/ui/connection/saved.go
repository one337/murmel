package connection

import (
	"fmt"
	"strings"

	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui"
)

// savedName is the display name of a saved connection: username@address,
// with the port appended only when it is not the Mumble default so two
// servers on one host stay distinguishable.
func savedName(username, host string, port int) string {
	addr := host
	if port != 0 && port != ui.DefaultPort {
		addr = fmt.Sprintf("%s:%d", host, port)
	}
	if username == "" {
		return addr
	}
	return username + "@" + addr
}

// sameAccount reports whether e describes the same server account.
func sameAccount(e settings.ServerEntry, host string, port int, username string) bool {
	return strings.EqualFold(e.Host, host) && e.Port == port && e.Username == username
}

// upsertServer stores entry, replacing a saved entry for the same account,
// and moves it to the front (most recently used first).
func upsertServer(list []settings.ServerEntry, entry settings.ServerEntry) []settings.ServerEntry {
	out := make([]settings.ServerEntry, 0, len(list)+1)
	out = append(out, entry)
	for _, e := range list {
		if !sameAccount(e, entry.Host, entry.Port, entry.Username) {
			out = append(out, e)
		}
	}
	return out
}

// removeServer drops the saved entry for the given account.
func removeServer(list []settings.ServerEntry, host string, port int, username string) []settings.ServerEntry {
	out := make([]settings.ServerEntry, 0, len(list))
	for _, e := range list {
		if !sameAccount(e, host, port, username) {
			out = append(out, e)
		}
	}
	return out
}
