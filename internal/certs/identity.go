package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// emailAddressOID is the pkcs-9 emailAddress RDN used by the official Mumble
// client when generating identities.
var emailAddressOID = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}

const (
	rsaKeyBits = 2048
	certYears  = 10
)

// Generate creates a new identity: an RSA 2048 self-signed certificate with
// CN = name (the Mumble username convention) and, if email is non-empty, the
// pkcs-9 emailAddress RDN. The PEM pair and identity.json are persisted under
// identities/<id>/.
func (m *Manager) Generate(name, email string) (*Identity, error) {
	if name == "" {
		return nil, errors.New("certs: identity name (username) must not be empty")
	}
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return nil, fmt.Errorf("certs: generating RSA key: %w", err)
	}

	serial, err := randSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	subject := pkix.Name{CommonName: name}
	if email != "" {
		subject.ExtraNames = []pkix.AttributeTypeAndValue{{Type: emailAddressOID, Value: ia5String(email)}}
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               subject,
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(certYears, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true, // matches the official Mumble client's self-signed identity format
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("certs: creating certificate: %w", err)
	}

	id, err := newIdentityID()
	if err != nil {
		return nil, err
	}
	meta := IdentityMeta{ID: id, Name: name, Email: email, Created: now.UTC()}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	if err := m.persistIdentity(meta, certPEM, keyPEM); err != nil {
		return nil, err
	}
	return m.loadIdentity(meta, certPEM, keyPEM)
}

// Load reads identity <id> from disk.
func (m *Manager) Load(id string) (*Identity, error) {
	dir := m.identityDir(id)
	certPEM, err := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		return nil, fmt.Errorf("certs: loading identity %s: %w", id, err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "key.pem"))
	if err != nil {
		return nil, fmt.Errorf("certs: loading identity %s: %w", id, err)
	}
	meta, err := m.loadMeta(id)
	if err != nil {
		return nil, err
	}
	return m.loadIdentity(*meta, certPEM, keyPEM)
}

// List returns the metadata of all stored identities, oldest first.
func (m *Manager) List() ([]IdentityMeta, error) {
	entries, err := os.ReadDir(m.identitiesDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var metas []IdentityMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := m.loadMeta(e.Name())
		if err != nil {
			continue // half-written/foreign dir: skip rather than fail listing
		}
		metas = append(metas, *meta)
	}
	sort.Slice(metas, func(i, j int) bool {
		if !metas[i].Created.Equal(metas[j].Created) {
			return metas[i].Created.Before(metas[j].Created)
		}
		return metas[i].ID < metas[j].ID
	})
	return metas, nil
}

// Delete removes identity <id> from disk.
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.RemoveAll(m.identityDir(id)); err != nil {
		return fmt.Errorf("certs: deleting identity %s: %w", id, err)
	}
	return nil
}

// Associate records that identity <id> was used with the server at addr.
func (m *Manager) Associate(id, addr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	meta, err := m.loadMeta(id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for i := range meta.Servers {
		if meta.Servers[i].Addr == addr {
			meta.Servers[i].LastUsed = now
			return m.saveMeta(*meta)
		}
	}
	meta.Servers = append(meta.Servers, ServerAssoc{Addr: addr, LastUsed: now})
	return m.saveMeta(*meta)
}

func (m *Manager) identitiesDir() string        { return filepath.Join(m.dataDir, "identities") }
func (m *Manager) identityDir(id string) string { return filepath.Join(m.identitiesDir(), id) }
func (m *Manager) metaPath(id string) string {
	return filepath.Join(m.identityDir(id), "identity.json")
}

func (m *Manager) persistIdentity(meta IdentityMeta, certPEM, keyPEM []byte) error {
	dir := m.identityDir(meta.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("certs: creating identity dir: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, "cert.pem"), certPEM, 0o644); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "key.pem"), keyPEM, 0o600); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveMeta(meta)
}

func (m *Manager) loadMeta(id string) (*IdentityMeta, error) {
	raw, err := os.ReadFile(m.metaPath(id))
	if err != nil {
		return nil, fmt.Errorf("certs: reading identity.json for %s: %w", id, err)
	}
	var meta IdentityMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("certs: parsing identity.json for %s: %w", id, err)
	}
	return &meta, nil
}

// saveMeta must be called with m.mu held.
func (m *Manager) saveMeta(meta IdentityMeta) error {
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(m.metaPath(meta.ID), raw, 0o644)
}

func (m *Manager) loadIdentity(meta IdentityMeta, certPEM, keyPEM []byte) (*Identity, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("certs: identity %s: %w", meta.ID, err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("certs: identity %s: parsing leaf: %w", meta.ID, err)
	}
	return &Identity{Meta: meta, Certificate: leaf, PrivateKey: pair.PrivateKey, TLSCertificate: pair}, nil
}

func randSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 127)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("certs: generating serial: %w", err)
	}
	return serial, nil
}

func hostOf(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
