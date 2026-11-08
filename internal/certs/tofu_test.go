package certs

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTOFUStateMachine(t *testing.T) {
	m := newTestManager(t)

	first, err := m.Generate("ServerCert1", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Generate("ServerCert2", "")
	if err != nil {
		t.Fatal(err)
	}
	addr := "harness.local:64738"

	chainA := []*x509.Certificate{first.Certificate}
	chainB := []*x509.Certificate{second.Certificate}

	if r, err := m.VerifyServer(addr, chainA); err != nil || r != VerifyNew {
		t.Fatalf("first verify = %v, %v; want VerifyNew", r, err)
	}
	if r, err := m.VerifyServer(addr, chainA); err != nil || r != VerifyMatch {
		t.Fatalf("second verify = %v, %v; want VerifyMatch", r, err)
	}
	if r, err := m.VerifyServer(addr, chainB); err != nil || r != VerifyChanged {
		t.Fatalf("swapped-cert verify = %v, %v; want VerifyChanged", r, err)
	}

	known, err := m.KnownServers()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := known[addr]
	if !ok {
		t.Fatalf("no TOFU entry persisted for %s", addr)
	}
	if entry.Fingerprint != Fingerprint(first.Certificate) {
		t.Error("CHANGED must not overwrite the stored fingerprint")
	}
	if entry.DisplayName != "harness.local" {
		t.Errorf("DisplayName = %q, want host part of addr", entry.DisplayName)
	}
	if entry.FirstSeen.IsZero() {
		t.Error("FirstSeen not recorded")
	}
}

func TestTOFUPersistsAcrossManagerInstances(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Generate("Persist", "")

	if _, err := m.VerifyServer("h:1", []*x509.Certificate{id.Certificate}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(m.DataDir(), "known-servers.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("known-servers.json not created: %v", err)
	}

	m2, err := NewManager(WithDataDir(m.DataDir()))
	if err != nil {
		t.Fatal(err)
	}
	if r, err := m2.VerifyServer("h:1", []*x509.Certificate{id.Certificate}); err != nil || r != VerifyMatch {
		t.Fatalf("verify in fresh manager = %v, %v; want VerifyMatch (persisted)", r, err)
	}
}

func TestTOFULastSeenUpdatedOnMatch(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Generate("LastSeen", "")
	addr := "h:2"

	m.VerifyServer(addr, []*x509.Certificate{id.Certificate})
	known, _ := m.KnownServers()
	first := known[addr].FirstSeen

	time.Sleep(20 * time.Millisecond)
	m.VerifyServer(addr, []*x509.Certificate{id.Certificate})
	known, _ = m.KnownServers()
	if !known[addr].LastSeen.After(first) {
		t.Error("LastSeen not advanced on Match")
	}
	if !known[addr].FirstSeen.Equal(first) {
		t.Error("FirstSeen must never change after the initial New")
	}
}

func TestVerifyResultString(t *testing.T) {
	cases := map[VerifyResult]string{
		VerifyNew:     "new",
		VerifyMatch:   "match",
		VerifyChanged: "changed",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Errorf("VerifyResult(%d).String() = %q, want %q", r, got, want)
		}
	}
}
