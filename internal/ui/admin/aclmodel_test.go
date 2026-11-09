package admin

import (
	"reflect"
	"testing"

	"io.github/murmel/internal/bus"
)

func TestRuleStates(t *testing.T) {
	var r bus.ACLRuleInfo
	setRuleState(&r, permEnter, permAllow)
	if ruleState(r, permEnter) != permAllow || r.Denied != 0 {
		t.Fatalf("allow: %+v", r)
	}
	setRuleState(&r, permEnter, permDeny)
	if ruleState(r, permEnter) != permDeny || r.Granted != 0 {
		t.Fatalf("deny: %+v", r)
	}
	setRuleState(&r, permEnter, permInherit)
	if r.Granted != 0 || r.Denied != 0 {
		t.Fatalf("inherit: %+v", r)
	}
}

func TestGroupNamesAndCatalog(t *testing.T) {
	groups := []bus.ACLGroupInfo{
		{Name: "zeta"}, {Name: "admin", Inherited: true}, {Name: "all"},
	}
	if got, want := groupNames(groups), []string{"all", "auth", "in", "out", "sub", "admin", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("groupNames = %v, want %v", got, want)
	}

	acl := &bus.ACLFetched{
		Groups: []bus.ACLGroupInfo{{Name: "admin", UsersAdd: []bus.ACLUserInfo{{UserID: 3, Name: "carl"}}}},
		Rules:  []bus.ACLRuleInfo{{UserID: 4, UserName: "dora"}},
	}
	online := map[uint32]bus.UserInfo{1: {UserID: 0, Name: "guest"}, 2: {UserID: 5, Name: "eve"}}
	reg := []bus.RegisteredUserInfo{{UserID: 6, Name: "fritz"}}
	got := userCatalog(acl, online, reg)
	want := map[uint32]string{3: "carl", 4: "dora", 5: "eve", 6: "fritz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("userCatalog = %v, want %v", got, want)
	}
	if id, _, ok := findUser(got, " EVE "); !ok || id != 5 {
		t.Fatalf("findUser = %d %v", id, ok)
	}
}

// Only groups the channel defines or changes are written back.
func TestOwnGroups(t *testing.T) {
	groups := []bus.ACLGroupInfo{
		{Name: "own", InheritUsers: true, Inheritable: true},
		{Name: "plain", Inherited: true, InheritUsers: true, Inheritable: true},
		{Name: "extended", Inherited: true, InheritUsers: true, Inheritable: true, UsersAdd: []bus.ACLUserInfo{{UserID: 1}}},
		{Name: "closed", Inherited: true, InheritUsers: false, Inheritable: true},
	}
	var names []string
	for _, g := range ownGroups(groups) {
		names = append(names, g.Name)
	}
	if want := []string{"own", "extended", "closed"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("ownGroups = %v, want %v", names, want)
	}
}
