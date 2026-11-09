package gumble

import "testing"

func TestACLReferencesUsers(t *testing.T) {
	plain := &ACL{
		Groups: []*ACLGroup{{Name: "admin"}},
		Rules:  []*ACLRule{{Group: &ACLGroup{Name: "all"}}},
	}
	if aclReferencesUsers(plain) {
		t.Error("ACL without users reported as referencing users")
	}
	member := &ACL{Groups: []*ACLGroup{{Name: "admin", UsersAdd: map[uint32]*ACLUser{3: {UserID: 3}}}}}
	if !aclReferencesUsers(member) {
		t.Error("group member not detected")
	}
	userRule := &ACL{Rules: []*ACLRule{{User: &ACLUser{UserID: 4}}}}
	if !aclReferencesUsers(userRule) {
		t.Error("user rule not detected")
	}
}
