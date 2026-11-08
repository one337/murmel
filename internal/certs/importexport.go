package certs

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ImportPEM imports an identity from PEM data, accepting either a single
// concatenated blob (certificate chain followed by the private key — the
// official Mumble client export format) or certificate-only data when the key
// lives in a separate file (use ImportFiles for that layout). The private key
// must match the leaf certificate; the import fails otherwise.
func (m *Manager) ImportPEM(name string, pemData []byte) (*Identity, error) {
	if name == "" {
		return nil, errors.New("certs: imported identity must have a name")
	}
	certDERs, key, err := parsePEMBundle(pemData)
	if err != nil {
		return nil, err
	}
	if len(certDERs) == 0 {
		return nil, errors.New("certs: import data contains no CERTIFICATE block")
	}
	if key == nil {
		return nil, errors.New("certs: import data contains no private key")
	}
	return m.importParsed(name, certDERs, key)
}

// ImportFiles imports an identity from PEM files. certPath may hold the
// certificate chain alone or a concatenated cert+key blob; keyPath, when
// non-empty, holds the private key separately.
func (m *Manager) ImportFiles(name, certPath, keyPath string) (*Identity, error) {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("certs: reading %s: %w", certPath, err)
	}
	pemData := certData
	if keyPath != "" {
		keyData, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("certs: reading %s: %w", keyPath, err)
		}
		pemData = append(append([]byte{}, certData...), keyData...)
	}
	return m.ImportPEM(name, pemData)
}

// ExportPEM returns the identity as a single concatenated PEM blob
// (certificate chain, then private key) suitable for backup or migration.
func (m *Manager) ExportPEM(id string) ([]byte, error) {
	ident, err := m.Load(id)
	if err != nil {
		return nil, err
	}
	certPEM, err := os.ReadFile(filepath.Join(m.identityDir(id), "cert.pem"))
	if err != nil {
		return nil, err
	}
	keyDER, err := x509MarshalPrivateKey(ident.PrivateKey)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return append(append([]byte{}, certPEM...), keyPEM...), nil
}

func (m *Manager) importParsed(name string, certDERs [][]byte, key crypto.PrivateKey) (*Identity, error) {
	leaf, err := x509.ParseCertificate(certDERs[0])
	if err != nil {
		return nil, fmt.Errorf("certs: parsing imported certificate: %w", err)
	}
	if err := ensureKeyMatches(leaf, key); err != nil {
		return nil, err
	}

	id, err := newIdentityID()
	if err != nil {
		return nil, err
	}
	meta := IdentityMeta{ID: id, Name: name, Email: subjectEmail(leaf), Created: time.Now().UTC()}

	var certPEM []byte
	for _, der := range certDERs {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	keyDER, err := x509MarshalPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	if err := m.persistIdentity(meta, certPEM, keyPEM); err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &Identity{Meta: meta, Certificate: leaf, PrivateKey: key, TLSCertificate: pair}, nil
}

func parsePEMBundle(data []byte) (certs [][]byte, key crypto.PrivateKey, err error) {
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch block.Type {
		case "CERTIFICATE":
			certs = append(certs, block.Bytes)
		case "PRIVATE KEY":
			k, perr := x509.ParsePKCS8PrivateKey(block.Bytes)
			if perr != nil {
				return nil, nil, fmt.Errorf("certs: parsing PKCS#8 private key: %w", perr)
			}
			key = k
		case "RSA PRIVATE KEY":
			k, perr := x509.ParsePKCS1PrivateKey(block.Bytes)
			if perr != nil {
				return nil, nil, fmt.Errorf("certs: parsing RSA private key: %w", perr)
			}
			key = k
		case "EC PRIVATE KEY":
			k, perr := x509.ParseECPrivateKey(block.Bytes)
			if perr != nil {
				return nil, nil, fmt.Errorf("certs: parsing EC private key: %w", perr)
			}
			key = k
		}
	}
	if key == nil && len(certs) == 0 {
		return nil, nil, errors.New("certs: no PEM blocks found")
	}
	return certs, key, nil
}

// ensureKeyMatches verifies the private key really belongs to the certificate,
// so a mismatched cert/key pair can never be persisted as an identity.
func ensureKeyMatches(cert *x509.Certificate, key crypto.PrivateKey) error {
	var pub crypto.PublicKey
	switch k := key.(type) {
	case *rsa.PrivateKey:
		pub = &k.PublicKey
	case *ecdsa.PrivateKey:
		pub = &k.PublicKey
	case ed25519.PrivateKey:
		pub = k.Public()
	default:
		return fmt.Errorf("certs: unsupported private key type %T", key)
	}
	certPub, ok := cert.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !certPub.Equal(pub) {
		return errors.New("certs: private key does not match certificate")
	}
	return nil
}

func x509MarshalPrivateKey(key crypto.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("certs: encoding private key: %w", err)
	}
	return der, nil
}

func subjectEmail(cert *x509.Certificate) string {
	for _, atv := range cert.Subject.Names {
		if atv.Type.Equal(emailAddressOID) {
			if s, ok := atv.Value.(string); ok {
				return s
			}
		}
	}
	return ""
}

// ia5String wraps s as an ASN.1 IA5String so the pkcs-9 emailAddress RDN is
// encoded per RFC 2985 (Go's default string encoding would use UTF8String,
// which openssl renders as a raw-OID hex blob instead of emailAddress=...).
func ia5String(s string) asn1.RawValue {
	return asn1.RawValue{
		Class:     asn1.ClassUniversal,
		Tag:       asn1.TagIA5String,
		Bytes:     []byte(s),
		FullBytes: append([]byte{byte(asn1.TagIA5String), byte(len(s))}, s...),
	}
}
