package certs

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"fmt"
	"strings"
)

// Bundle is an identity read from a file and not stored yet.
type Bundle struct {
	// Leaf is the certificate that belongs to the private key.
	Leaf  *x509.Certificate
	certs [][]byte // DER as in the file, the leaf first
	key   crypto.PrivateKey
}

// DuplicateError reports that the certificate is already stored.
type DuplicateError struct{ Meta IdentityMeta }

func (e *DuplicateError) Error() string {
	return "certs: certificate already stored as identity " + e.Meta.ID
}

// ReadIdentity reads an identity file: PEM (certificate chain and private
// key, any order) or PKCS#12 (.p12, .pfx), told apart by content.
// password opens a PKCS#12 file ("" also opens files without one); PEM
// ignores it. A PKCS#12 file that needs a password fails with
// ErrWrongPassword.
func ReadIdentity(data []byte, password string) (*Bundle, error) {
	var (
		ders [][]byte
		key  crypto.PrivateKey
		err  error
	)
	switch {
	case bytes.Contains(data, []byte("-----BEGIN ")):
		ders, key, err = parsePEMBundle(data)
	case len(data) > 0 && data[0] == 0x30: // DER SEQUENCE
		ders, key, err = decodeP12(data, password)
	default:
		return nil, ErrUnsupportedFile
	}
	if err != nil {
		return nil, err
	}
	return newBundle(ders, key)
}

// newBundle checks that a certificate in ders belongs to key and puts it
// first; the others keep their order.
func newBundle(ders [][]byte, key crypto.PrivateKey) (*Bundle, error) {
	if len(ders) == 0 {
		return nil, ErrNoCertificate
	}
	if key == nil {
		return nil, ErrNoKey
	}
	if _, err := publicKey(key); err != nil {
		return nil, err
	}
	leafAt := -1
	var leaf *x509.Certificate
	for i, der := range ders {
		c, err := parseOwnCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("%w (certificate: %v)", ErrDamagedFile, err)
		}
		if leafAt < 0 && ensureKeyMatches(c, key) == nil {
			leafAt, leaf = i, c
		}
	}
	if leafAt < 0 {
		return nil, ErrKeyMismatch
	}
	ordered := append([][]byte{ders[leafAt]}, ders[:leafAt]...)
	ordered = append(ordered, ders[leafAt+1:]...)
	return &Bundle{Leaf: leaf, certs: ordered, key: key}, nil
}

// CommonName is the certificate's CN: by Mumble convention the user name,
// the suggested name for the identity.
func (b *Bundle) CommonName() string { return strings.TrimSpace(b.Leaf.Subject.CommonName) }

// Fingerprint is the SHA-256 fingerprint of the certificate (see
// Fingerprint).
func (b *Bundle) Fingerprint() string { return Fingerprint(b.Leaf) }

// FindCertificate returns the stored identity whose certificate has
// fingerprint fp.
func (m *Manager) FindCertificate(fp string) (IdentityMeta, bool) {
	metas, err := m.List()
	if err != nil {
		return IdentityMeta{}, false
	}
	for _, meta := range metas {
		if ident, err := m.Load(meta.ID); err == nil && Fingerprint(ident.Certificate) == fp {
			return meta, true
		}
	}
	return IdentityMeta{}, false
}

// Store adds b to the identity store under name. A certificate that is
// already stored is not added again: the error is a *DuplicateError
// naming the stored identity.
func (m *Manager) Store(name string, b *Bundle) (*Identity, error) {
	if meta, ok := m.FindCertificate(b.Fingerprint()); ok {
		return nil, &DuplicateError{Meta: meta}
	}
	return m.importParsed(name, b.certs, b.key)
}

// Email is the certificate's e-mail address for display (see certEmail).
func (b *Bundle) Email() string { return certEmail(b.Leaf) }
