package certs

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ImportPEM imports an identity from PEM data: the certificate chain and
// the private key in one blob, in any order (use ImportFiles when the key
// lives in a separate file). One of the certificates must belong to the
// key; it becomes the identity's certificate.
func (m *Manager) ImportPEM(name string, pemData []byte) (*Identity, error) {
	if name == "" {
		return nil, errors.New("certs: imported identity must have a name")
	}
	certDERs, key, err := parsePEMBundle(pemData)
	if err != nil {
		return nil, err
	}
	b, err := newBundle(certDERs, key)
	if err != nil {
		return nil, err
	}
	return m.importParsed(name, b.certs, b.key)
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
	leaf, err := parseOwnCertificate(certDERs[0])
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
	meta := IdentityMeta{ID: id, Name: name, Email: certEmail(leaf), Created: time.Now().UTC()}

	var certPEM []byte
	for _, der := range certDERs {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	keyDER, err := x509MarshalPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	// Check the result before anything is written, and leave no partial
	// identity directory (with a private key in it) behind.
	ident, err := m.loadIdentity(meta, certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	if err := m.persistIdentity(meta, certPEM, keyPEM); err != nil {
		_ = os.RemoveAll(m.identityDir(id))
		return nil, err
	}
	return ident, nil
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
				return nil, nil, fmt.Errorf("%w (PKCS#8 private key: %v)", ErrDamagedFile, perr)
			}
			key = k
		case "RSA PRIVATE KEY":
			k, perr := x509.ParsePKCS1PrivateKey(block.Bytes)
			if perr != nil {
				return nil, nil, fmt.Errorf("%w (RSA private key: %v)", ErrDamagedFile, perr)
			}
			key = k
		case "EC PRIVATE KEY":
			k, perr := x509.ParseECPrivateKey(block.Bytes)
			if perr != nil {
				return nil, nil, fmt.Errorf("%w (EC private key: %v)", ErrDamagedFile, perr)
			}
			key = k
		}
	}
	if key == nil && len(certs) == 0 {
		return nil, nil, ErrUnsupportedFile
	}
	return certs, key, nil
}

// ensureKeyMatches verifies the private key really belongs to the certificate,
// so a mismatched cert/key pair can never be persisted as an identity.
func ensureKeyMatches(cert *x509.Certificate, key crypto.PrivateKey) error {
	pub, err := publicKey(key)
	if err != nil {
		return err
	}
	certPub, ok := cert.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !certPub.Equal(pub) {
		return ErrKeyMismatch
	}
	return nil
}

// publicKey returns the public half of a key type Murmel can use.
func publicKey(key crypto.PrivateKey) (crypto.PublicKey, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey, nil
	case *ecdsa.PrivateKey:
		return &k.PublicKey, nil
	case ed25519.PrivateKey:
		return k.Public(), nil
	}
	return nil, fmt.Errorf("certs: unsupported private key type %T", key)
}

func x509MarshalPrivateKey(key crypto.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("certs: encoding private key: %w", err)
	}
	return der, nil
}
