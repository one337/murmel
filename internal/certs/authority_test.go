package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// testCA is a certificate authority for tests, trusted through Manager.roots.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testCA{cert: cert, key: key}
}

// leaf issues a server certificate for dnsName, valid until notAfter.
func (ca *testCA) leaf(t *testing.T, dnsName string, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    time.Now().Add(-2 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// startSwitchableServer serves the certificate in cur; tests swap it to
// simulate a renewal. It returns the address as localhost:port.
func startSwitchableServer(t *testing.T, cur *atomic.Pointer[tls.Certificate]) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return cur.Load(), nil
	}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			tconn := tls.Server(conn, cfg)
			if err := tconn.Handshake(); err == nil {
				tconn.Write([]byte("ok"))
			}
			tconn.Close()
		}
	}()
	t.Cleanup(func() { ln.Close(); <-done })
	return "localhost:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

func TestTLSConfigAuthorityIssuedCertificateSkipsPrompt(t *testing.T) {
	m := newTestManager(t)
	ca := newTestCA(t)
	m.roots = x509.NewCertPool()
	m.roots.AddCert(ca.cert)
	client, _ := m.Generate("Client", "")

	var cur atomic.Pointer[tls.Certificate]
	first := ca.leaf(t, "localhost", time.Now().Add(time.Hour))
	cur.Store(&first)
	addr := startSwitchableServer(t, &cur)

	prompted := 0
	cfg := m.TLSConfig(client, addr, func(VerifyResult, ServerEntry, ServerEntry) error {
		prompted++
		return nil
	})

	if err := dialTLS(t, cfg, addr); err != nil {
		t.Fatalf("first dial: %v", err)
	}
	firstFP := Fingerprint(mustParse(t, first))
	if known, _ := m.KnownServers(); known[addr].Fingerprint != firstFP {
		t.Fatal("authority-issued certificate was not pinned")
	}

	// A renewal (new key, same authority) connects silently and replaces the pin.
	renewed := ca.leaf(t, "localhost", time.Now().Add(time.Hour))
	cur.Store(&renewed)
	if err := dialTLS(t, cfg, addr); err != nil {
		t.Fatalf("dial after renewal: %v", err)
	}
	known, _ := m.KnownServers()
	if known[addr].Fingerprint != Fingerprint(mustParse(t, renewed)) {
		t.Fatal("renewed certificate did not replace the pin")
	}
	if prompted != 0 {
		t.Fatalf("trust callback ran %d times, want 0", prompted)
	}
}

func TestTLSConfigInvalidAuthorityCertificateUsesTOFU(t *testing.T) {
	ca := newTestCA(t)
	cases := map[string]func(t *testing.T) tls.Certificate{
		"wrong host": func(t *testing.T) tls.Certificate {
			return ca.leaf(t, "other.example", time.Now().Add(time.Hour))
		},
		"expired": func(t *testing.T) tls.Certificate {
			return ca.leaf(t, "localhost", time.Now().Add(-time.Hour))
		},
		"unknown authority": func(t *testing.T) tls.Certificate {
			return newTestCA(t).leaf(t, "localhost", time.Now().Add(time.Hour))
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			m := newTestManager(t)
			m.roots = x509.NewCertPool()
			m.roots.AddCert(ca.cert)
			client, _ := m.Generate("Client", "")

			var cur atomic.Pointer[tls.Certificate]
			c := mk(t)
			cur.Store(&c)
			addr := startSwitchableServer(t, &cur)

			var results []VerifyResult
			cfg := m.TLSConfig(client, addr, func(r VerifyResult, _, _ ServerEntry) error {
				results = append(results, r)
				return nil
			})
			if err := dialTLS(t, cfg, addr); err != nil {
				t.Fatalf("dial: %v", err)
			}
			if len(results) != 1 || results[0] != VerifyNew {
				t.Fatalf("trust callback results = %v, want [new]", results)
			}
		})
	}
}

func mustParse(t *testing.T, c tls.Certificate) *x509.Certificate {
	t.Helper()
	cert, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
