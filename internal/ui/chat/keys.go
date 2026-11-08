package chat

import (
	"strconv"

	"io.github/murmel/internal/bus"
)

// convKey identifies a private conversation across reconnects: registered
// users by their account ID, others by name (sessions change on every
// connect).
type convKey string

func keyForUser(u bus.UserInfo) convKey {
	if u.UserID > 0 {
		return convKey("u:" + strconv.FormatUint(uint64(u.UserID), 10))
	}
	return convKey("n:" + u.Name)
}

// sessionFor finds the current session of the conversation partner (false
// while they are offline).
func sessionFor(st bus.State, key convKey) (uint32, bool) {
	for session, u := range st.Users {
		if session != st.SelfSession && keyForUser(u) == key {
			return session, true
		}
	}
	return 0, false
}
