package ui

import (
	"net/url"
	"strconv"
	"strings"
)

// DefaultPort is the standard Mumble TLS port, applied when a connect URL
// carries none.
const DefaultPort = 64738

// ConnectRequest carries everything the connect dialog (T19) needs to open
// pre-filled. It is produced by ParseConnectURL for mumble:// URLs handed to
// the binary; an empty request opens the dialog blank.
type ConnectRequest struct {
	// URL is the original mumble:// string ("" when the user clicked
	// Connect without a URL).
	URL string
	// Host and Port name the server (Port defaults to DefaultPort).
	Host string
	Port int
	// Username and Password come from the URL userinfo, if present.
	Username string
	Password string
	// ChannelPath is the channel/sub-channel chain from the URL path,
	// e.g. mumble://host/Lobby/Sub → ["Lobby", "Sub"].
	ChannelPath []string
}

// ParseConnectURL parses a mumble:// or mumbles:// connect URL. It returns
// ok=false for every other scheme, for unparsable input, and for URLs
// without a host — callers then simply ignore the argument.
func ParseConnectURL(raw string) (req ConnectRequest, ok bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return ConnectRequest{}, false
	}
	switch u.Scheme {
	case "mumble", "mumbles":
	default:
		return ConnectRequest{}, false
	}
	if u.Hostname() == "" {
		return ConnectRequest{}, false
	}

	req = ConnectRequest{
		URL:      raw,
		Host:     u.Hostname(),
		Port:     DefaultPort,
		Username: u.User.Username(),
	}
	if password, hasPassword := u.User.Password(); hasPassword {
		req.Password = password
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err == nil && n > 0 && n < 65536 {
			req.Port = n
		}
	}
	if trimmed := strings.Trim(u.Path, "/"); trimmed != "" {
		req.ChannelPath = strings.Split(trimmed, "/")
	}
	return req, true
}
