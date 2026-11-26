package certs

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
)

// VerifyCallback observes TOFU results during the TLS handshake and decides
// whether the connection may proceed: returning nil accepts, an error aborts
// the handshake. It is the hook the trust dialog uses to present a
// changed fingerprint and honor an "accept anyway" choice.
type VerifyCallback func(result VerifyResult, stored, presented ServerEntry) error

// TLSConfig builds the *tls.Config for dialing a Mumble server with the given
// identity: it presents the identity as the TLS client certificate and
// verifies the server chain itself in VerifyPeerCertificate.
//
// A server certificate that a trusted authority (the system's CA store)
// issued for the host is accepted without asking, as browsers and the
// official Mumble client do, and pinned silently, so regular renewals (e.g.
// Let's Encrypt) do not raise the changed-certificate warning. Everything
// else, typically the self-signed certificates of most Mumble servers, goes
// through TOFU: by default an unknown server is pinned on first sight and a
// changed certificate (VerifyChanged) fails the handshake with an error
// wrapping ErrServerCertChanged. With onVerify, the callback decides both
// cases (e.g. the user's trust dialog), and the certificate is pinned only
// once it accepts. When identity is nil the connection is anonymous (no
// client certificate) but verified the same way.
func (m *Manager) TLSConfig(identity *Identity, addr string, onVerify VerifyCallback) *tls.Config {
	cfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // verified below: authority, then TOFU (most murmur certs are self-signed)
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error { //nolint:gosec // no ClientSessionCache, so no handshake is resumed unverified
			chain := make([]*x509.Certificate, 0, len(rawCerts))
			for _, raw := range rawCerts {
				cert, err := x509.ParseCertificate(raw)
				if err != nil {
					return fmt.Errorf("certs: parsing server certificate: %w", err)
				}
				chain = append(chain, cert)
			}
			result, stored, presented, err := m.verifyServerDetail(addr, chain)
			if err != nil {
				return err
			}
			if m.authorityTrusts(addr, chain) {
				// Valid for this host by a trusted authority: no prompt, and a
				// renewed certificate replaces the pin silently.
				if result == VerifyMatch {
					return nil
				}
				return m.pin(addr, presented)
			}
			if onVerify != nil {
				if cbErr := onVerify(result, stored, presented); cbErr != nil {
					return cbErr // declined: nothing is pinned
				}
			} else if result == VerifyChanged {
				return fmt.Errorf("certs: %w: %s (pinned: %s)", ErrServerCertChanged, presented.Fingerprint, stored.Fingerprint)
			}
			if result == VerifyMatch {
				return nil
			}
			return m.pin(addr, presented)
		},
	}
	if identity != nil {
		pair := identity.TLSCertificate
		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &pair, nil
		}
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		cfg.ServerName = host
	}
	return cfg
}

// authorityTrusts reports whether the server chain (leaf first) is valid for
// the host in addr under the trusted authorities.
func (m *Manager) authorityTrusts(addr string, chain []*x509.Certificate) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:] {
		intermediates.AddCert(c)
	}
	_, err = chain[0].Verify(x509.VerifyOptions{
		DNSName:       host,
		Intermediates: intermediates,
		Roots:         m.roots,
	})
	return err == nil
}
