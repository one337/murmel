package connection

import (
	"fmt"
	"slices"
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

// upsertServer stores entry: a saved entry for the same account is
// replaced where it is, a new one is added at the end. The order is the
// user's (see moveServer); connecting does not change it.
func upsertServer(list []settings.ServerEntry, entry settings.ServerEntry) []settings.ServerEntry {
	out := slices.Clone(list)
	for i, e := range out {
		if sameAccount(e, entry.Host, entry.Port, entry.Username) {
			out[i] = entry
			return out
		}
	}
	return append(out, entry)
}

// moveServer moves the entry at index from so that it ends up at index to.
// Out-of-range indexes leave the list unchanged.
func moveServer(list []settings.ServerEntry, from, to int) []settings.ServerEntry {
	out := slices.Clone(list)
	if from < 0 || from >= len(out) || to < 0 || to >= len(out) || from == to {
		return out
	}
	e := out[from]
	out = slices.Delete(out, from, from+1)
	return slices.Insert(out, to, e)
}

// serverIndex returns the index of the account in list, or -1.
func serverIndex(list []settings.ServerEntry, host string, port int, username string) int {
	return slices.IndexFunc(list, func(e settings.ServerEntry) bool { return sameAccount(e, host, port, username) })
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
