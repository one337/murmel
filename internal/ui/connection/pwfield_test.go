package connection

import "testing"

func TestPasswordFieldConnectBeforeLookupLeavesKeyring(t *testing.T) {
	var f passwordField
	f.startLookup()
	if f.known() {
		t.Fatal("field counts as known while the lookup is pending")
	}
}

func TestPasswordFieldClearedAfterLookupMayDelete(t *testing.T) {
	var f passwordField
	gen := f.startLookup()
	if !f.lookupDone(gen, true) {
		t.Fatal("lookup result not applied")
	}
	f.edited() // the user clears the field
	if !f.known() {
		t.Fatal("cleared field after lookup is not known")
	}
}

func TestPasswordFieldTypingWinsOverLateLookup(t *testing.T) {
	var f passwordField
	gen := f.startLookup()
	f.edited()
	if f.lookupDone(gen, true) {
		t.Fatal("late lookup overwrote the user's input")
	}
	if !f.known() {
		t.Fatal("typed password is not known")
	}
}

func TestPasswordFieldFailedLookupStaysPending(t *testing.T) {
	var f passwordField
	gen := f.startLookup()
	if f.lookupDone(gen, false) {
		t.Fatal("failed lookup applied")
	}
	if f.known() {
		t.Fatal("field counts as known after a failed lookup")
	}
}

func TestPasswordFieldNewerLookupWins(t *testing.T) {
	var f passwordField
	old := f.startLookup()
	cur := f.startLookup()
	if f.lookupDone(old, true) {
		t.Fatal("outdated lookup applied")
	}
	if !f.lookupDone(cur, true) || !f.known() {
		t.Fatal("current lookup not applied")
	}
}
