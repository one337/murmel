package certs

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func hasLegacyEmail(cert *x509.Certificate) bool {
	for _, atv := range cert.Subject.Names {
		if atv.Type.Equal(oidLegacyEmail) {
			return true
		}
	}
	return false
}

func TestGenerateEmailASCII(t *testing.T) {
	id, err := newTestManager(t).Generate("User", "user@example.org")
	if err != nil {
		t.Fatal(err)
	}
	c := id.Certificate
	if len(c.EmailAddresses) != 1 || c.EmailAddresses[0] != "user@example.org" {
		t.Fatalf("rfc822Name = %v, want [user@example.org]", c.EmailAddresses)
	}
	if hasLegacyEmail(c) {
		t.Fatal("email also written to the subject DN")
	}
	if smtpUTF8Mailbox(c) != "" {
		t.Fatal("ASCII address also written as SmtpUTF8Mailbox")
	}
}

func TestGenerateEmailUnicodeDomain(t *testing.T) {
	id, err := newTestManager(t).Generate("User", "user@müller.example")
	if err != nil {
		t.Fatal(err)
	}
	c := id.Certificate
	if len(c.EmailAddresses) != 1 || c.EmailAddresses[0] != "user@xn--mller-kva.example" {
		t.Fatalf("rfc822Name = %v, want the domain as A-label", c.EmailAddresses)
	}
	if got := certEmail(c); got != "user@müller.example" {
		t.Fatalf("displayed email = %q, want user@müller.example", got)
	}
}

func TestGenerateEmailUnicodeLocalPart(t *testing.T) {
	id, err := newTestManager(t).Generate("User", "jürgen@müller.example")
	if err != nil {
		t.Fatal(err)
	}
	c := id.Certificate
	if len(c.EmailAddresses) != 0 {
		t.Fatalf("rfc822Name = %v, want none", c.EmailAddresses)
	}
	// RFC 9598: the domain is an A-label in SmtpUTF8Mailbox too.
	if got := smtpUTF8Mailbox(c); got != "jürgen@xn--mller-kva.example" {
		t.Fatalf("SmtpUTF8Mailbox = %q", got)
	}
	if got := certEmail(c); got != "jürgen@müller.example" {
		t.Fatalf("displayed email = %q", got)
	}
	if hasLegacyEmail(c) {
		t.Fatal("email also written to the subject DN")
	}
	if unreadable, err := CheckEmail("jürgen@müller.example"); err != nil || !unreadable {
		t.Fatalf("CheckEmail = %v, %v; want unreadable", unreadable, err)
	}

	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl not installed")
	}
	cmd := exec.Command(openssl, "x509", "-noout", "-text")
	cmd.Stdin = bytes.NewReader(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("openssl cannot read the certificate: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Subject Alternative Name") {
		t.Fatalf("openssl output lacks the subjectAltName:\n%s", out)
	}
}

func TestGenerateInvalidEmailLeavesNothing(t *testing.T) {
	cases := []string{
		"noat.example.org",
		"a@b@example.org",
		"@example.org",
		"user@",
		"a b@example.org",
		"a\n@example.org",
		"user@exa mple.org",
		"user@-bad-.example",
		strings.Repeat("a", 65) + "@example.org",
		"user@" + strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 63) + ".org", // 264 octets
	}
	for _, addr := range cases {
		m := newTestManager(t)
		if _, err := m.Generate("User", addr); err == nil {
			t.Errorf("Generate(%q) succeeded", addr)
			continue
		}
		entries, _ := os.ReadDir(m.identitiesDir())
		if len(entries) != 0 {
			t.Errorf("Generate(%q) left %d entries in identities/", addr, len(entries))
		}
		if _, err := CheckEmail(addr); err == nil {
			t.Errorf("CheckEmail(%q) accepted it", addr)
		}
	}
}

func TestGenerateEmailLimits(t *testing.T) {
	local := strings.Repeat("a", 64)
	if _, err := CheckEmail(local + "@example.org"); err != nil {
		t.Fatalf("64-octet local part refused: %v", err)
	}
	if _, err := newTestManager(t).Generate("User", ""); err != nil {
		t.Fatalf("Generate without email: %v", err)
	}
}

func TestCertEmailLegacySubjectDN(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "Old",
			ExtraNames: []pkix.AttributeTypeAndValue{{Type: oidLegacyEmail, Value: "old@example.org"}},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	if got := certEmail(c); got != "old@example.org" {
		t.Fatalf("certEmail = %q, want old@example.org", got)
	}
}
