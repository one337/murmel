package certs

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// ErrInvalidEmail is returned for an email address a certificate cannot hold.
var ErrInvalidEmail = errors.New("certs: invalid email address")

var (
	// oidSubjectAltName is the subjectAltName extension (RFC 5280).
	oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}
	// oidSmtpUTF8Mailbox is the otherName type for addresses whose local
	// part is not ASCII (RFC 9598).
	oidSmtpUTF8Mailbox = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 8, 9}
	// oidLegacyEmail is the pkcs-9 emailAddress attribute in the subject
	// DN, where Murmel stored the address before; only read, never written.
	oidLegacyEmail = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}
)

// Length limits in octets (RFC 5321).
const (
	maxEmailLen = 254
	maxLocalLen = 64
)

// mailbox is an address in the form a certificate stores it: the local
// part as typed, the domain as IDNA A-labels.
type mailbox struct {
	local, domain string
}

func (b mailbox) String() string { return b.local + "@" + b.domain }

// ascii reports whether the local part is ASCII, i.e. whether the address
// fits a rfc822Name (otherwise it needs a SmtpUTF8Mailbox).
func (b mailbox) ascii() bool {
	for i := 0; i < len(b.local); i++ {
		if b.local[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// parseMailbox validates addr: exactly one @, a non-empty local part of at
// most 64 octets without spaces or control characters, a valid domain
// (converted to A-labels) and at most 254 octets in all.
func parseMailbox(addr string) (mailbox, error) {
	if !utf8.ValidString(addr) || strings.Count(addr, "@") != 1 {
		return mailbox{}, ErrInvalidEmail
	}
	for _, r := range addr {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return mailbox{}, ErrInvalidEmail
		}
	}
	local, domain, _ := strings.Cut(addr, "@")
	if local == "" || len(local) > maxLocalLen || domain == "" {
		return mailbox{}, ErrInvalidEmail
	}
	ascii, err := idna.Lookup.ToASCII(domain)
	if err != nil || ascii == "" {
		return mailbox{}, ErrInvalidEmail
	}
	b := mailbox{local: local, domain: ascii}
	if len(b.String()) > maxEmailLen {
		return mailbox{}, ErrInvalidEmail
	}
	return b, nil
}

// CheckEmail validates an email address for a new certificate. unreadable
// reports that the local part has non-ASCII letters: the address is then
// stored in a form Mumble servers do not read.
func CheckEmail(addr string) (unreadable bool, err error) {
	b, err := parseMailbox(addr)
	if err != nil {
		return false, err
	}
	return !b.ascii(), nil
}

// otherName is a subjectAltName entry of a type named by OID (RFC 5280):
// [0] IMPLICIT SEQUENCE { type-id OID, value [0] EXPLICIT ANY }. Value holds
// the explicit [0] wrapper itself (asn1 writes a RawValue verbatim).
type otherName struct {
	Type  asn1.ObjectIdentifier
	Value asn1.RawValue
}

// setEmail puts the address into the certificate template's subjectAltName,
// where the official Mumble client stores it and Mumble servers read it: an
// ASCII local part as rfc822Name, any other as SmtpUTF8Mailbox (never both).
func setEmail(tmpl *x509.Certificate, b mailbox) error {
	if b.ascii() {
		tmpl.EmailAddresses = []string{b.String()}
		return nil
	}
	value, err := asn1.MarshalWithParams(b.String(), "utf8")
	if err != nil {
		return err
	}
	other, err := asn1.MarshalWithParams(otherName{
		Type:  oidSmtpUTF8Mailbox,
		Value: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: value},
	}, "tag:0")
	if err != nil {
		return err
	}
	san, err := asn1.Marshal([]asn1.RawValue{{FullBytes: other}})
	if err != nil {
		return err
	}
	tmpl.ExtraExtensions = append(tmpl.ExtraExtensions, pkix.Extension{Id: oidSubjectAltName, Value: san})
	return nil
}

// certEmail returns the certificate's email address for display: the
// rfc822Name, else a SmtpUTF8Mailbox, else the subject DN attribute older
// Murmel versions wrote. Domains are shown in Unicode.
func certEmail(cert *x509.Certificate) string {
	if len(cert.EmailAddresses) > 0 {
		return displayEmail(cert.EmailAddresses[0])
	}
	if addr := smtpUTF8Mailbox(cert); addr != "" {
		return displayEmail(addr)
	}
	for _, atv := range cert.Subject.Names {
		if atv.Type.Equal(oidLegacyEmail) {
			if s, ok := atv.Value.(string); ok {
				return s
			}
		}
	}
	return ""
}

// smtpUTF8Mailbox returns the first SmtpUTF8Mailbox in the certificate's
// subjectAltName (Go's parser skips otherName entries).
func smtpUTF8Mailbox(cert *x509.Certificate) string {
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(oidSubjectAltName) {
			continue
		}
		var names []asn1.RawValue
		if rest, err := asn1.Unmarshal(ext.Value, &names); err != nil || len(rest) > 0 {
			return ""
		}
		for _, n := range names {
			if n.Class != asn1.ClassContextSpecific || n.Tag != 0 || !n.IsCompound {
				continue
			}
			var other otherName
			if _, err := asn1.UnmarshalWithParams(n.FullBytes, &other, "tag:0"); err != nil {
				continue
			}
			v := other.Value
			if !other.Type.Equal(oidSmtpUTF8Mailbox) || v.Class != asn1.ClassContextSpecific || v.Tag != 0 {
				continue
			}
			var s string
			if _, err := asn1.UnmarshalWithParams(v.Bytes, &s, "utf8"); err == nil {
				return s
			}
		}
	}
	return ""
}

// displayEmail turns the domain's A-labels back into Unicode.
func displayEmail(addr string) string {
	local, domain, ok := strings.Cut(addr, "@")
	if !ok {
		return addr
	}
	if u, err := idna.Display.ToUnicode(domain); err == nil {
		domain = u
	}
	return local + "@" + domain
}
