package certs

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
)

// VerifyCallback observes TOFU results during the TLS handshake and decides
// whether the connection may proceed: returning nil accepts, an error aborts
// the handshake. It is the hook the T19 trust dialog will use to present a
// changed fingerprint and honor an "accept anyway" choice.
type VerifyCallback func(result VerifyResult, stored, presented ServerEntry) error

// TLSConfig builds the *tls.Config for dialing a Mumble server with the given
// identity: it presents the identity as the TLS client certificate, and —
// since Mumble servers use self-signed certificates that must not be validated
// against WebPKI — it skips standard verification and instead applies TOFU
// verification to the presented server chain via VerifyPeerCertificate.
//
// By default a changed server certificate (VerifyChanged) fails the handshake
// with an error wrapping ErrServerCertChanged; supply onVerify to override,
// e.g. after user confirmation. When identity is nil the connection is
// anonymous (no client certificate) but still TOFU-verified.
func (m *Manager) TLSConfig(identity *Identity, addr string, onVerify VerifyCallback) *tls.Config {
	cfg := &tls.Config{
		InsecureSkipVerify: true, // TOFU below replaces WebPKI; murmur certs are self-signed
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
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
			if onVerify != nil {
				if cbErr := onVerify(result, stored, presented); cbErr != nil {
					return cbErr
				}
				if result == VerifyChanged {
					return m.rePin(addr, presented)
				}
				return nil
			}
			if result == VerifyChanged {
				return fmt.Errorf("certs: %w: %s (pinned: %s)", ErrServerCertChanged, presented.Fingerprint, stored.Fingerprint)
			}
			return nil
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
