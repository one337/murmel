package certs

import (
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

// Errors reading or writing an identity file. Their texts name no file
// content, password or key.
var (
	// ErrWrongPassword: the password does not open the file (a damaged
	// file fails the same check).
	ErrWrongPassword = errors.New("certs: wrong password")
	// ErrUnsupportedFile: neither PEM nor a PKCS#12 file Murmel can read
	// (BER encoding among others).
	ErrUnsupportedFile = errors.New("certs: unsupported file format")
	// ErrDamagedFile: the file is cut off or not well-formed.
	ErrDamagedFile = errors.New("certs: damaged file")
	// ErrTooManyIterations: the file asks for more key derivation rounds
	// than any real backup uses.
	ErrTooManyIterations = errors.New("certs: too many key derivation iterations")
	// ErrNoCertificate and ErrNoKey: the file lacks the certificate or the
	// private key.
	ErrNoCertificate = errors.New("certs: no certificate in the file")
	ErrNoKey         = errors.New("certs: no private key in the file")
	// ErrKeyMismatch: no certificate in the file belongs to the key.
	ErrKeyMismatch = errors.New("certs: private key does not match certificate")
	// ErrEmptyPassword: backups are never written without a password.
	ErrEmptyPassword = errors.New("certs: backup password is empty")
	// ErrPasswordCharacters: the password has characters a PKCS#12 file
	// cannot hold (outside the Basic Multilingual Plane, such as emoji).
	ErrPasswordCharacters = errors.New("certs: password has unsupported characters")
)

// exportIterations is the PBKDF2 round count for backups: 50 times
// OpenSSL's default, still quick to open in the official Mumble client,
// which tries the password on every key press.
const exportIterations = 100_000

// maxIterations bounds the key derivation rounds a file may ask for
// (a few seconds of work); the library itself has no limit.
const maxIterations = 10_000_000

// ExportP12 returns identity id as a PKCS#12 file protected by password:
// certificate chain and private key encrypted with AES-256-CBC, keys
// derived with PBKDF2-HMAC-SHA-256, SHA-256 MAC. OpenSSL 1.1.1 and later
// read it, so does the official Mumble client.
func (m *Manager) ExportP12(id, password string) ([]byte, error) {
	if password == "" {
		return nil, ErrEmptyPassword
	}
	ident, err := m.Load(id)
	if err != nil {
		return nil, err
	}
	var chain []*x509.Certificate
	for _, der := range ident.TLSCertificate.Certificate[1:] {
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("certs: identity %s: parsing chain: %w", id, err)
		}
		chain = append(chain, c)
	}
	data, err := pkcs12.Modern2023.WithIterations(exportIterations).Encode(ident.PrivateKey, ident.Certificate, chain, password)
	if err != nil {
		if isPasswordEncodingError(err) {
			return nil, ErrPasswordCharacters
		}
		return nil, fmt.Errorf("certs: encoding backup: %w", err)
	}
	return data, nil
}

// decodeP12 reads a PKCS#12 file: the certificates (DER, in file order)
// and the private key.
func decodeP12(data []byte, password string) (certDERs [][]byte, key crypto.PrivateKey, err error) {
	if err := checkP12Iterations(data); err != nil {
		return nil, nil, err
	}
	key, leaf, chain, err := pkcs12.DecodeChain(data, password)
	if err != nil {
		return nil, nil, p12Error(err)
	}
	certDERs = append(certDERs, leaf.Raw)
	for _, c := range chain {
		certDERs = append(certDERs, c.Raw)
	}
	return certDERs, key, nil
}

// p12Error maps the library's errors to the ones above.
func p12Error(err error) error {
	var notImpl pkcs12.NotImplementedError
	switch {
	case errors.Is(err, pkcs12.ErrIncorrectPassword), errors.Is(err, pkcs12.ErrDecryption):
		return ErrWrongPassword
	case isPasswordEncodingError(err):
		return ErrWrongPassword
	case errors.As(err, &notImpl):
		return ErrUnsupportedFile
	case strings.Contains(err.Error(), "certificate missing"):
		return ErrNoCertificate
	case strings.Contains(err.Error(), "private key missing"):
		return ErrNoKey
	case isBER(err):
		return ErrUnsupportedFile
	}
	return fmt.Errorf("%w (%v)", ErrDamagedFile, err)
}

func isPasswordEncodingError(err error) bool {
	return strings.Contains(err.Error(), "cannot be encoded in UCS-2")
}

func isBER(err error) bool {
	return strings.Contains(err.Error(), "indefinite length")
}
