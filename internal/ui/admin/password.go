package admin

import (
	"strings"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/protocol/gumble"
)

// Mumble has no channel password of its own; clients encode it in the ACL
// (as the official client's channel editor does):
//
//	@all       deny  Enter   (this channel, not sub-channels)
//	#password  allow Enter   (this channel, not sub-channels)
//
// "#password" is an access-token group: users who give the password as an
// access token are members. These helpers read and write that pair.

const permEnter = int(gumble.PermissionEnter)

// isPasswordRule reports whether r is the token rule that grants Enter.
func isPasswordRule(r bus.ACLRuleInfo) bool {
	return !r.Inherited && r.AppliesCurrent && strings.HasPrefix(r.GroupName, "#") &&
		len(r.GroupName) > 1 && r.Granted&permEnter != 0
}

// isPasswordDeny reports whether r is the "@all deny Enter" half.
func isPasswordDeny(r bus.ACLRuleInfo) bool {
	return !r.Inherited && r.AppliesCurrent && !r.AppliesChildren && r.GroupName == "all" &&
		r.UserID == 0 && r.Granted == 0 && r.Denied == permEnter
}

// channelPassword returns the password the channel's own rules encode ("" =
// none).
func channelPassword(rules []bus.ACLRuleInfo) string {
	for _, r := range rules {
		if isPasswordRule(r) {
			return r.GroupName[1:]
		}
	}
	return ""
}

// withPassword returns the channel's own (non-inherited) rules with the
// password pair set to password: replaced, added, or removed ("").
func withPassword(rules []bus.ACLRuleInfo, password string) []bus.ACLRuleInfo {
	out := make([]bus.ACLRuleInfo, 0, len(rules)+2)
	replaced := false
	for _, r := range rules {
		switch {
		case r.Inherited:
			continue
		case isPasswordRule(r):
			if password != "" && !replaced {
				r.GroupName = "#" + password
				out = append(out, r)
				replaced = true
			}
			continue
		case isPasswordDeny(r) && password == "":
			continue
		}
		out = append(out, r)
	}
	if password != "" && !replaced {
		out = append(out,
			bus.ACLRuleInfo{AppliesCurrent: true, GroupName: "all", Denied: permEnter},
			bus.ACLRuleInfo{AppliesCurrent: true, GroupName: "#" + password, Granted: permEnter})
	}
	return out
}
