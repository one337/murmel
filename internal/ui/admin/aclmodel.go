package admin

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/protocol/gumble"
	"io.github/murmel/internal/ui/permname"
)

// aclPerm is one permission the editor offers.
type aclPerm struct {
	bit  int
	root bool // only meaningful on the root channel
}

// name is the permission's translated name.
func (p aclPerm) name() string { return permname.Name(gumble.Permission(p.bit)) }

// aclPerms lists the permissions in Mumble's order.
var aclPerms = []aclPerm{
	{int(gumble.PermissionWrite), false},
	{int(gumble.PermissionTraverse), false},
	{int(gumble.PermissionEnter), false},
	{int(gumble.PermissionSpeak), false},
	{int(gumble.PermissionWhisper), false},
	{int(gumble.PermissionMuteDeafen), false},
	{int(gumble.PermissionMove), false},
	{int(gumble.PermissionMakeChannel), false},
	{int(gumble.PermissionMakeTemporaryChannel), false},
	{int(gumble.PermissionLinkChannel), false},
	{int(gumble.PermissionTextMessage), false},
	{int(gumble.PermissionKick), true},
	{int(gumble.PermissionBan), true},
	{int(gumble.PermissionRegister), true},
	{int(gumble.PermissionRegisterSelf), true},
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
