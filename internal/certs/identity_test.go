package certs

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(WithDataDir(t.TempDir()))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func TestGenerateCreatesValidSelfSignedCert(t *testing.T) {
	m := newTestManager(t)

	id, err := m.Generate("TestUser", "test@example.com")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	cert := id.Certificate
	if cert == nil {
		t.Fatal("Identity.Certificate is nil")
	}
	if got := cert.Subject.CommonName; got != "TestUser" {
		t.Errorf("CN = %q, want %q (Mumble convention: CN = username)", got, "TestUser")
	}
	if got := subjectEmail(cert); got != "test@example.com" {
		t.Errorf("emailAddress RDN = %q, want test@example.com (round-tripped through DER)", got)
	}

	key, ok := id.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("private key type = %T, want *rsa.PrivateKey", id.PrivateKey)
	}
	if got := key.N.BitLen(); got != 2048 {
		t.Errorf("RSA key size = %d bits, want 2048", got)
	}
	if got := cert.PublicKeyAlgorithm; got != x509.RSA {
		t.Errorf("public key algorithm = %v, want RSA", got)
	}

	if err := cert.CheckSignatureFrom(cert); err != nil {
		t.Errorf("self-signature invalid: %v", err)
	}
	if cert.NotAfter.Before(time.Now()) {
		t.Errorf("cert already expired: NotAfter=%s", cert.NotAfter)
	}

	if len(id.TLSCertificate.Certificate) == 0 {
		t.Error("Identity.TLSCertificate not populated")
	}
}

func TestGeneratePersistsFilesAndMetadata(t *testing.T) {
	m := newTestManager(t)

	id, err := m.Generate("FileCheck", "files@example.com")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	dir := filepath.Join(m.DataDir(), "identities", id.Meta.ID)

	for _, name := range []string{"cert.pem", "key.pem", "identity.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected file %s: %v", name, err)
		}
	}

	info, err := os.Stat(filepath.Join(dir, "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("key.pem perms = %v, want 0600", info.Mode().Perm())
	}

	raw, err := os.ReadFile(filepath.Join(dir, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta IdentityMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("identity.json is not valid JSON: %v", err)
	}
	if meta.Name != "FileCheck" {
		t.Errorf("meta.Name = %q, want FileCheck", meta.Name)
	}
	if meta.Email != "files@example.com" {
		t.Errorf("meta.Email = %q", meta.Email)
	}
	if meta.ID != id.Meta.ID {
		t.Errorf("meta.ID = %q, want %q", meta.ID, id.Meta.ID)
	}
	if meta.Created.IsZero() {
		t.Error("meta.Created is zero")
	}
}

func TestLoadIdentity(t *testing.T) {
	m := newTestManager(t)

	id, err := m.Generate("LoadMe", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// m2 must prove the data comes from disk, not in-memory state.
	m2, err := NewManager(WithDataDir(m.DataDir()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := m2.Load(id.Meta.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Certificate.Subject.CommonName != "LoadMe" {
		t.Errorf("loaded CN = %q", got.Certificate.Subject.CommonName)
	}
	sumWant := sha256.Sum256(id.Certificate.Raw)
	sumGot := sha256.Sum256(got.Certificate.Raw)
	if sumWant != sumGot {
		t.Error("loaded cert differs from generated cert")
	}
	if got.TLSCertificate.PrivateKey == nil {
		t.Error("loaded TLSCertificate has no private key")
	}

	if _, err := m2.Load("does-not-exist"); err == nil {
		t.Error("Load of unknown id must fail")
	}
}

func TestListIdentities(t *testing.T) {
	m := newTestManager(t)

	a, _ := m.Generate("Alice", "")
	time.Sleep(10 * time.Millisecond) // force distinct Created timestamps for the sort assertion
	b, _ := m.Generate("Bob", "")

	list, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(list))
	}
	if list[0].Name != "Alice" || list[1].Name != "Bob" {
		t.Errorf("List not sorted by creation: [%s %s]", list[0].Name, list[1].Name)
	}
	if a.Meta.ID == b.Meta.ID {
		t.Error("identity IDs are not unique")
	}
}

func TestDeleteIdentity(t *testing.T) {
	m := newTestManager(t)

	id, _ := m.Generate("Temp", "")
	if err := m.Delete(id.Meta.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, _ := m.List()
	if len(list) != 0 {
		t.Errorf("identity still listed after delete: %d", len(list))
	}
	if _, err := os.Stat(filepath.Join(m.DataDir(), "identities", id.Meta.ID)); !os.IsNotExist(err) {
		t.Error("identity dir still on disk")
	}
}

func TestAssociateServer(t *testing.T) {
	m := newTestManager(t)

	id, _ := m.Generate("Assoc", "")
	if err := m.Associate(id.Meta.ID, "harness.local:64738"); err != nil {
		t.Fatalf("Associate: %v", err)
	}
	if err := m.Associate(id.Meta.ID, "harness.local:64738"); err != nil {
		t.Fatalf("Associate (2nd): %v", err)
	}

	got, err := m.Load(id.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Meta.Servers) != 1 || got.Meta.Servers[0].Addr != "harness.local:64738" {
		t.Errorf("Servers = %+v, want single harness.local:64738 entry", got.Meta.Servers)
	}
}

func TestFingerprintFormat(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Generate("Fp", "")

	fp := Fingerprint(id.Certificate)
	// SHA-256 over DER, rendered as 32 colon-separated lowercase hex bytes (T19 dialog format).
	want := regexp.MustCompile(`^([0-9a-f]{2}:){31}[0-9a-f]{2}$`)
	if !want.MatchString(fp) {
		t.Errorf("Fingerprint %q does not match grouped-hex SHA-256 format", fp)
	}
}
