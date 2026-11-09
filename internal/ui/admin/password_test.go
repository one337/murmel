package admin

import (
	"testing"

	"io.github/murmel/internal/bus"
)

func TestChannelPasswordRoundTrip(t *testing.T) {
	inherited := bus.ACLRuleInfo{Inherited: true, AppliesCurrent: true, GroupName: "admin", Granted: 1}
	own := bus.ACLRuleInfo{AppliesCurrent: true, AppliesChildren: true, GroupName: "auth", Granted: 8}
	rules := []bus.ACLRuleInfo{inherited, own}

	if pw := channelPassword(rules); pw != "" {
		t.Fatalf("no password expected, got %q", pw)
	}

	set := withPassword(rules, "geheim")
	if len(set) != 3 {
		t.Fatalf("set: want own rule + pair (inherited dropped), got %+v", set)
	}
	if set[0] != own {
		t.Errorf("own rule changed or reordered: %+v", set[0])
	}
	if !isPasswordDeny(set[1]) || !isPasswordRule(set[2]) {
		t.Errorf("password pair wrong: %+v", set[1:])
	}
	if pw := channelPassword(set); pw != "geheim" {
		t.Fatalf("channelPassword = %q", pw)
	}

	changed := withPassword(set, "neu")
	if len(changed) != 3 || channelPassword(changed) != "neu" {
		t.Fatalf("change: %+v", changed)
	}

	cleared := withPassword(changed, "")
	if len(cleared) != 1 || cleared[0] != own {
		t.Fatalf("clear must leave only the own rule: %+v", cleared)
	}
}

// A deny-all rule the admin wrote for other reasons (applies to
// sub-channels too) is not part of a password and survives clearing.
func TestClearPasswordKeepsUnrelatedDeny(t *testing.T) {
	deny := bus.ACLRuleInfo{AppliesCurrent: true, AppliesChildren: true, GroupName: "all", Denied: permEnter}
	rules := withPassword([]bus.ACLRuleInfo{deny}, "x")
	got := withPassword(rules, "")
	if len(got) != 1 || got[0] != deny {
		t.Fatalf("got %+v", got)
	}
}
