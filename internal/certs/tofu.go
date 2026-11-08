package certs

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrServerCertChanged is returned (wrapped) when a server presents a
// certificate that does not match the fingerprint pinned for it.
var ErrServerCertChanged = errors.New("server certificate changed")

// VerifyResult is the outcome of a TOFU verification.
type VerifyResult int

const (
	// VerifyNew means no fingerprint was pinned for the address; the
	// presented fingerprint has been recorded as the new pin.
	VerifyNew VerifyResult = iota
	// VerifyMatch means the presented fingerprint equals the pin.
	VerifyMatch
	// VerifyChanged means the presented fingerprint differs from the pin;
	// the pin is NOT modified.
	VerifyChanged
)

func (r VerifyResult) String() string {
	switch r {
	case VerifyNew:
		return "new"
	case VerifyMatch:
		return "match"
	case VerifyChanged:
		return "changed"
	default:
		return fmt.Sprintf("VerifyResult(%d)", int(r))
	}
}

// ServerEntry is one pinned server in known-servers.json.
type ServerEntry struct {
	Fingerprint string    `json:"fingerprint"` // grouped-hex SHA-256 of the leaf cert
	DisplayName string    `json:"display_name"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
}

// Fingerprint returns the SHA-256 fingerprint of cert as lowercase hex bytes
// joined by colons ("aa:bb:cc:…") — the format shown in the T19 trust dialog.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = hex.EncodeToString([]byte{b})
	}
	return strings.Join(parts, ":")
}

// VerifyServer applies TOFU to the server certificate chain (leaf first) for
// host:port and persists the outcome.
func (m *Manager) VerifyServer(addr string, chain []*x509.Certificate) (VerifyResult, error) {
	result, _, _, err := m.verifyServerDetail(addr, chain)
	return result, err
}

// KnownServers returns the persisted pin table keyed by "host:port".
func (m *Manager) KnownServers() (map[string]ServerEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadStore()
}

func (m *Manager) verifyServerDetail(addr string, chain []*x509.Certificate) (VerifyResult, ServerEntry, ServerEntry, error) {
	if len(chain) == 0 || chain[0] == nil {
		return 0, ServerEntry{}, ServerEntry{}, errors.New("certs: empty server certificate chain")
	}
	now := time.Now().UTC()
	presented := ServerEntry{
		Fingerprint: Fingerprint(chain[0]),
		DisplayName: hostOf(addr),
		FirstSeen:   now,
		LastSeen:    now,
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	store, err := m.loadStore()
	if err != nil {
		return 0, ServerEntry{}, ServerEntry{}, err
	}
	stored, ok := store[addr]
	switch {
	case !ok:
		store[addr] = presented
		if err := m.saveStore(store); err != nil {
			return 0, ServerEntry{}, ServerEntry{}, err
		}
		return VerifyNew, presented, presented, nil
	case stored.Fingerprint == presented.Fingerprint:
		stored.LastSeen = now
		store[addr] = stored
		if err := m.saveStore(store); err != nil {
			return 0, ServerEntry{}, ServerEntry{}, err
		}
		return VerifyMatch, stored, presented, nil
	default:
		return VerifyChanged, stored, presented, nil
	}
}

// rePin replaces the pinned fingerprint for addr after the user explicitly
// accepted a changed certificate, preserving the original FirstSeen.
func (m *Manager) rePin(addr string, presented ServerEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	store, err := m.loadStore()
	if err != nil {
		return err
	}
	stored := store[addr]
	presented.FirstSeen = stored.FirstSeen
	store[addr] = presented
	return m.saveStore(store)
}

func (m *Manager) storePath() string { return filepath.Join(m.dataDir, "known-servers.json") }

// loadStore must be called with m.mu held.
func (m *Manager) loadStore() (map[string]ServerEntry, error) {
	store := map[string]ServerEntry{}
	raw, err := os.ReadFile(m.storePath())
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, fmt.Errorf("certs: reading known-servers.json: %w", err)
	}
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, fmt.Errorf("certs: parsing known-servers.json: %w", err)
	}
	return store, nil
}

// saveStore must be called with m.mu held.
func (m *Manager) saveStore(store map[string]ServerEntry) error {
	raw, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(m.storePath(), raw, 0o600)
}
