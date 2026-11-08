// Package certs implements Mumble client identity management: self-signed
// RSA certificate generation (CN = username), PEM import/export, a TOFU
// (trust-on-first-use) known-servers store, and the tls.Config seam used by
// the connection layer.
package certs

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"
	"time"
)

// Manager owns the on-disk layout under the Murmel data directory
// ($XDG_DATA_HOME/murmel, override with WithDataDir):
//
//	identities/<id>/{cert.pem,key.pem,identity.json}
//	known-servers.json
type Manager struct {
	dataDir string

	mu sync.Mutex // serializes TOFU store and metadata read-modify-write cycles
}

// Option customizes a Manager.
type Option func(*Manager)

// WithDataDir points the Manager at an explicit data directory. Tests use it
// with t.TempDir() so the real user XDG dir is never touched.
func WithDataDir(dir string) Option {
	return func(m *Manager) { m.dataDir = dir }
}

// NewManager creates a Manager rooted at the Murmel data directory
// (XDG_DATA_HOME, falling back to ~/.local/share).
func NewManager(opts ...Option) (*Manager, error) {
	dir, err := defaultDataDir()
	if err != nil {
		return nil, err
	}
	m := &Manager{dataDir: dir}
	for _, o := range opts {
		o(m)
	}
	if err := mkdirAll(m.dataDir); err != nil {
		return nil, err
	}
	return m, nil
}

// DataDir returns the root data directory in use.
func (m *Manager) DataDir() string { return m.dataDir }

// Identity is a loaded identity: metadata plus the parsed certificate pair.
type Identity struct {
	Meta           IdentityMeta
	Certificate    *x509.Certificate
	PrivateKey     any
	TLSCertificate tls.Certificate
}

// IdentityMeta is the per-identity metadata persisted as identity.json.
type IdentityMeta struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Email   string        `json:"email,omitempty"`
	Created time.Time     `json:"created"`
	Servers []ServerAssoc `json:"servers,omitempty"`
}

// ServerAssoc records that an identity has been used with a server.
type ServerAssoc struct {
	Addr     string    `json:"addr"`
	LastUsed time.Time `json:"last_used"`
}

func newIdentityID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("certs: generating identity id: %w", err)
	}
	return fmt.Sprintf("%x", b[:]), nil
}
