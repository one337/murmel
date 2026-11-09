package admin

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/protocol/gumble"
)

// aclPerm is one permission the editor offers.
type aclPerm struct {
	bit  int
	name func() string
	root bool // only meaningful on the root channel
}

// aclPerms lists the permissions in Mumble's order. Names are functions so
// they are translated when shown.
//
// TRANSLATORS (for the permission names below): these are Mumble channel
// permissions, shown as rows with Inherit/Allow/Deny. "Enter" means
// entering the channel, "Traverse" passing through it to sub-channels,
// "Move" moving other users, "Write ACL" editing the channel's permissions.
var aclPerms = []aclPerm{
	// TRANSLATORS: Permission: edit the channel's groups and permissions.
	{int(gumble.PermissionWrite), func() string { return tr("Write ACL") }, false},
	// TRANSLATORS: Permission: pass through the channel to its sub-channels.
	{int(gumble.PermissionTraverse), func() string { return tr("Traverse") }, false},
	// TRANSLATORS: Permission: enter (join) the channel.
	{int(gumble.PermissionEnter), func() string { return tr("Enter") }, false},
	// TRANSLATORS: Permission: talk in the channel.
	{int(gumble.PermissionSpeak), func() string { return tr("Speak") }, false},
	{int(gumble.PermissionWhisper), func() string { return tr("Whisper") }, false},
	{int(gumble.PermissionMuteDeafen), func() string { return tr("Mute/Deafen") }, false},
	// TRANSLATORS: Permission: move other users between channels.
	{int(gumble.PermissionMove), func() string { return tr("Move") }, false},
	{int(gumble.PermissionMakeChannel), func() string { return tr("Make Channel") }, false},
	{int(gumble.PermissionMakeTemporaryChannel), func() string { return tr("Make Temporary Channel") }, false},
	{int(gumble.PermissionLinkChannel), func() string { return tr("Link Channel") }, false},
	{int(gumble.PermissionTextMessage), func() string { return tr("Text Message") }, false},
	{int(gumble.PermissionKick), func() string { return tr("Kick") }, true},
	{int(gumble.PermissionBan), func() string { return tr("Ban") }, true},
	{int(gumble.PermissionRegister), func() string { return tr("Register User") }, true},
	{int(gumble.PermissionRegisterSelf), func() string { return tr("Register Self") }, true},
}

// builtinGroups are the groups Mumble computes itself.
var builtinGroups = []string{"all", "auth", "in", "out", "sub"}

// permState is a rule's setting for one permission.
type permState uint8

const (
	permInherit permState = iota
	permAllow
	permDeny
)

func ruleState(r bus.ACLRuleInfo, bit int) permState {
	switch {
	case r.Denied&bit != 0:
		return permDeny
	case r.Granted&bit != 0:
		return permAllow
	}
	return permInherit
}

func setRuleState(r *bus.ACLRuleInfo, bit int, st permState) {
	r.Granted &^= bit
	r.Denied &^= bit
	switch st {
	case permAllow:
		r.Granted |= bit
	case permDeny:
		r.Denied |= bit
	}
}

// ruleTarget names whom a rule applies to: "@group" or the user's name.
func ruleTarget(r bus.ACLRuleInfo, users map[uint32]string) string {
	if r.GroupName != "" {
		return "@" + r.GroupName
	}
	if r.UserName != "" {
		return r.UserName
	}
	if n := users[r.UserID]; n != "" {
		return n
	}
	return "#" + itoa(r.UserID)
}

// ruleSummary lists what a rule allows and denies, e.g. "Allows Enter,
// Speak · Denies Move".
func ruleSummary(r bus.ACLRuleInfo) string {
	var allow, deny []string
	for _, p := range aclPerms {
		switch ruleState(r, p.bit) {
		case permAllow:
			allow = append(allow, p.name())
		case permDeny:
			deny = append(deny, p.name())
		}
	}
	var parts []string
	if len(allow) > 0 {
		// TRANSLATORS: %s is a comma-separated list of permission names.
		parts = append(parts, fmt.Sprintf(tr("Allows %s"), strings.Join(allow, ", ")))
	}
	if len(deny) > 0 {
		// TRANSLATORS: %s is a comma-separated list of permission names.
		parts = append(parts, fmt.Sprintf(tr("Denies %s"), strings.Join(deny, ", ")))
	}
	if len(parts) == 0 {
		return tr("Changes nothing")
	}
	return strings.Join(parts, " · ")
}

// groupNames are the groups a rule can target: built-ins first, then the
// channel's groups (own and inherited), sorted.
func groupNames(groups []bus.ACLGroupInfo) []string {
	names := slices.Clone(builtinGroups)
	var own []string
	for _, g := range groups {
		if !slices.Contains(names, g.Name) && !slices.Contains(own, g.Name) {
			own = append(own, g.Name)
		}
	}
	sort.Strings(own)
	return append(names, own...)
}

// userCatalog collects registered users known from the ACL, online users
// and (if available) the server's user list.
func userCatalog(acl *bus.ACLFetched, online map[uint32]bus.UserInfo, registered []bus.RegisteredUserInfo) map[uint32]string {
	out := map[uint32]string{}
	add := func(id uint32, name string) {
		if id != 0 && name != "" {
			out[id] = name
		}
	}
	for _, u := range registered {
		add(u.UserID, u.Name)
	}
	for _, u := range online {
		add(u.UserID, u.Name)
	}
	if acl != nil {
		for _, g := range acl.Groups {
			for _, list := range [][]bus.ACLUserInfo{g.UsersAdd, g.UsersRemove, g.UsersInherited} {
				for _, u := range list {
					add(u.UserID, u.Name)
				}
			}
		}
		for _, r := range acl.Rules {
			add(r.UserID, r.UserName)
		}
	}
	return out
}

// findUser resolves a name (case-insensitive) in the catalog.
func findUser(users map[uint32]string, name string) (uint32, string, bool) {
	name = strings.TrimSpace(name)
	for id, n := range users {
		if strings.EqualFold(n, name) {
			return id, n, true
		}
	}
	return 0, "", false
}

func hasUser(list []bus.ACLUserInfo, id uint32) bool {
	return slices.ContainsFunc(list, func(u bus.ACLUserInfo) bool { return u.UserID == id })
}

func withoutUser(list []bus.ACLUserInfo, id uint32) []bus.ACLUserInfo {
	return slices.DeleteFunc(slices.Clone(list), func(u bus.ACLUserInfo) bool { return u.UserID == id })
}

func itoa(n uint32) string { return strconv.FormatUint(uint64(n), 10) }
