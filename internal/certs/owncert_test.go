package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

const mumbleEmail = "jürgen@müller.example"

// sanCert creates a self-signed certificate whose subjectAltName holds
// names exactly as given (the official Mumble client writes e-mail
// addresses with letters such as ä as raw UTF-8 into an rfc822Name).
func sanCert(t *testing.T, cn string, key *rsa.PrivateKey, names ...asn1.RawValue) []byte {
	t.Helper()
	san, err := asn1.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:    big.NewInt(1),
		Subject:         pkix.Name{CommonName: cn},
		NotBefore:       time.Now().Add(-time.Hour),
		NotAfter:        time.Now().Add(24 * time.Hour),
		KeyUsage:        x509.KeyUsageDigitalSignature,
		ExtKeyUsage:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		ExtraExtensions: []pkix.Extension{{Id: oidSubjectAltName, Value: san}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func rfc822(b string) asn1.RawValue {
	return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, Bytes: []byte(b)}
}

func dnsName(b string) asn1.RawValue {
	return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte(b)}
}

func pemIdentity(t *testing.T, der []byte, key *rsa.PrivateKey) []byte {
	t.Helper()
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...)
}

func fingerprintOf(der []byte) string { return Fingerprint(&x509.Certificate{Raw: der}) }

func TestParseOwnCertificateUTF8Email(t *testing.T) {
	key := rsaKey(t)
	der := sanCert(t, "Jürgen Ä", key, rfc822(mumbleEmail), rfc822("ascii@example.com"), dnsName("example.com"))
	if _, err := x509.ParseCertificate(der); err == nil {
		t.Fatal("Go's parser accepts the certificate; the test no longer covers the fallback")
	}
	c, err := parseOwnCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	if string(c.Raw) != string(der) {
		t.Error("Raw is not the original DER")
	}
	if len(c.EmailAddresses) != 2 || c.EmailAddresses[0] != mumbleEmail || c.EmailAddresses[1] != "ascii@example.com" {
		t.Errorf("EmailAddresses = %q", c.EmailAddresses)
	}
	if len(c.DNSNames) != 1 || c.DNSNames[0] != "example.com" {
		t.Errorf("other names lost: %q", c.DNSNames)
	}
	if c.Subject.CommonName != "Jürgen Ä" {
		t.Errorf("CN = %q", c.Subject.CommonName)
	}
	if err := ensureKeyMatches(c, key); err != nil {
		t.Error(err)
	}
}

// Only a UTF-8 rfc822Name is tolerated; every other parse error stays.
func TestParseOwnCertificateStaysStrict(t *testing.T) {
	key := rsaKey(t)
	good := sanCert(t, "Good", key, rfc822(mumbleEmail))
	for name, der := range map[string][]byte{
		"invalid UTF-8 in rfc822Name":  sanCert(t, "Bad", key, rfc822("j\xfcrgen@m\xfcller.example")),
		"control character":            sanCert(t, "Bad", key, rfc822("jürgen\x01@müller.example")),
		"C1 control character":         sanCert(t, "Bad", key, rfc822("jürgen\u0085@müller.example")),
		"UTF-8 dNSName":                sanCert(t, "Bad", key, dnsName("müller.example")),
		"UTF-8 rfc822Name and dNSName": sanCert(t, "Bad", key, rfc822(mumbleEmail), dnsName("müller.example")),
		"truncated":                    good[:len(good)-10],
		"trailing data":                append(append([]byte(nil), good...), 0),
		"not a certificate":            {0x30, 0x03, 0x02, 0x01, 0x01},
	} {
		if _, err := parseOwnCertificate(der); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A server's certificate goes through Go's strict parser (crypto/tls parses
// it before Murmel's check): the same e-mail address that an identity may
// carry fails the handshake.
func TestServerCertificateWithUTF8EmailIsRefused(t *testing.T) {
	key := rsaKey(t)
	der := sanCert(t, "Server", key, rfc822(mumbleEmail))
	srv := &Identity{TLSCertificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}}
	addr, stop := startTLSServer(t, srv, false)
	defer stop()

	m := newTestManager(t)
	accepted := false
	err := dialTLS(t, m.TLSConfig(nil, addr, func(VerifyResult, ServerEntry, ServerEntry) error {
		accepted = true
		return nil
	}), addr)
	if err == nil || !strings.Contains(err.Error(), "x509: SAN rfc822Name is malformed") {
		t.Fatalf("handshake: %v, want a parse error", err)
	}
	if accepted {
		t.Error("the trust prompt was asked for an unparsable server certificate")
	}
	if known, _ := m.KnownServers(); len(known) != 0 {
		t.Errorf("pinned: %v", known)
	}
}

// An identity of the official Mumble client with a UTF-8 e-mail address
// is imported from PEM and PKCS#12, sent to servers, kept over a restart
// and backed up as the original certificate, byte for byte.
func TestUTF8EmailIdentityRoundtrip(t *testing.T) {
	key := rsaKey(t)
	der := sanCert(t, "Jürgen Ä", key, rfc822(mumbleEmail))
	fp := fingerprintOf(der)

	inputs := map[string]func() (*Bundle, error){
		"PEM": func() (*Bundle, error) { return ReadIdentity(pemIdentity(t, der, key), "") },
		// No MAC, no encryption.
		"PKCS#12 without password": func() (*Bundle, error) {
			return ReadIdentity(encode(t, pkcs12.Passwordless, key, &x509.Certificate{Raw: der}, nil, ""), "")
		},
		// AES-256 with PBKDF2, as Murmel backs up.
		"PKCS#12 with password": func() (*Bundle, error) {
			return ReadIdentity(encode(t, pkcs12.Modern2023, key, &x509.Certificate{Raw: der}, nil, testPassword), testPassword)
		},
	}
	for name, read := range inputs {
		t.Run(name, func(t *testing.T) {
			b, err := read()
			if err != nil {
				t.Fatalf("ReadIdentity: %v", err)
			}
			if b.CommonName() != "Jürgen Ä" || b.Fingerprint() != fp {
				t.Fatalf("read %q %s", b.CommonName(), b.Fingerprint())
			}
			dir := t.TempDir()
			m, _ := NewManager(WithDataDir(dir))
			ident, err := m.Store("Jürgen", b)
			if err != nil {
				t.Fatalf("Store: %v", err)
			}
			checkUTF8Identity(t, ident, der, fp)

			// After a restart.
			m2, _ := NewManager(WithDataDir(dir))
			again, err := m2.Load(ident.Meta.ID)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			checkUTF8Identity(t, again, der, fp)

			// Backup and restore.
			backup, err := m2.ExportP12(ident.Meta.ID, testPassword)
			if err != nil {
				t.Fatalf("ExportP12: %v", err)
			}
			restored, err := ReadIdentity(backup, testPassword)
			if err != nil {
				t.Fatalf("reading the backup: %v", err)
			}
			if string(restored.certs[0]) != string(der) {
				t.Fatal("backup holds a different certificate")
			}
			m3 := newTestManager(t)
			ident3, err := m3.Store("Restored", restored)
			if err != nil {
				t.Fatal(err)
			}
			checkUTF8Identity(t, ident3, der, fp)
		})
	}
}

func checkUTF8Identity(t *testing.T, ident *Identity, der []byte, fp string) {
	t.Helper()
	if ident.Meta.Email != mumbleEmail {
		t.Errorf("e-mail = %q, want %q", ident.Meta.Email, mumbleEmail)
	}
	if Fingerprint(ident.Certificate) != fp {
		t.Error("fingerprint is not that of the original certificate")
	}
	if string(ident.TLSCertificate.Certificate[0]) != string(der) {
		t.Error("stored certificate differs from the original")
	}
	cfg := (&Manager{}).TLSConfig(ident, "127.0.0.1:1", nil)
	sent, err := cfg.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil || string(sent.Certificate[0]) != string(der) {
		t.Errorf("certificate sent to servers is not the original (%v)", err)
	}
}

// The file in testdata/p12 is OpenSSL's version of a Mumble export with
// such an address (see gen.sh).
func TestReadIdentityMumbleUTF8File(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "p12", "mumble-format-umlaut.p12"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReadIdentity(data, "")
	if err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t)
	ident, err := m.Store("Jürgen", b)
	if err != nil {
		t.Fatal(err)
	}
	if ident.Meta.Email != mumbleEmail || ident.Certificate.Subject.CommonName != "Jürgen Ä" {
		t.Errorf("e-mail %q, CN %q", ident.Meta.Email, ident.Certificate.Subject.CommonName)
	}
	if _, err := ReadIdentity(data, "wrong"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password: %v", err)
	}
}

// Legacy encryption around such a certificate cannot be opened by Murmel's
// own reader: a clear error, not a crash or a half import.
func TestReadIdentityUTF8EmailLegacyEncryption(t *testing.T) {
	key := rsaKey(t)
	der := sanCert(t, "Legacy", key, rfc822(mumbleEmail))
	data := encode(t, pkcs12.LegacyDES, key, &x509.Certificate{Raw: der}, nil, testPassword)
	if _, err := ReadIdentity(data, testPassword); !errors.Is(err, ErrUnreadableCertificate) {
		t.Fatalf("%v, want ErrUnreadableCertificate", err)
	}
	if _, err := ReadIdentity(data, "wrong"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
}

// Murmel's own addresses with a non-ASCII local part (SmtpUTF8Mailbox) and
// a CN with umlauts survive a backup unchanged.
func TestSmtpUTF8MailboxRoundtrip(t *testing.T) {
	m := newTestManager(t)
	orig, err := m.Generate("Jürgen Ä", mumbleEmail)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := m.ExportP12(orig.Meta.ID, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReadIdentity(backup, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	ident, err := newTestManager(t).Store("Back", b)
	if err != nil {
		t.Fatal(err)
	}
	if ident.Meta.Email != orig.Meta.Email || ident.Meta.Email != mumbleEmail {
		t.Errorf("e-mail %q, original %q", ident.Meta.Email, orig.Meta.Email)
	}
	if b.CommonName() != "Jürgen Ä" || string(ident.Certificate.Raw) != string(orig.Certificate.Raw) {
		t.Error("certificate changed")
	}
	if smtpUTF8Mailbox(ident.Certificate) == "" || len(ident.Certificate.EmailAddresses) != 0 {
		t.Error("the address is no longer a SmtpUTF8Mailbox")
	}
}
