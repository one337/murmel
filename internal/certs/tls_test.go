package certs

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"testing"
	"time"
)

// startTLSServer runs a one-connection-at-a-time TLS echo server with the
// given server identity; its address is dialed by the client-side tests.
func startTLSServer(t *testing.T, srv *Identity, requireClientCert bool) (addr string, stop func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srvCfg := &tls.Config{
		Certificates: []tls.Certificate{srv.TLSCertificate},
	}
	if requireClientCert {
		srvCfg.ClientAuth = tls.RequireAnyClientCert
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			tconn := tls.Server(conn, srvCfg)
			if err := tconn.Handshake(); err == nil {
				tconn.Write([]byte("ok"))
			}
			tconn.Close()
		}
	}()
	return ln.Addr().String(), func() { ln.Close(); <-done }
}

func dialTLS(t *testing.T, cfg *tls.Config, addr string) error {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, cfg)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var buf [2]byte
	_, err = conn.Read(buf[:])
	return err
}

func TestTLSConfigTOFUOverRealHandshake(t *testing.T) {
	m := newTestManager(t)
	client, _ := m.Generate("Client", "")
	serverA, _ := m.Generate("ServerA", "")
	serverB, _ := m.Generate("ServerB", "")

	addr, stop := startTLSServer(t, serverA, false)

	cfg := m.TLSConfig(client, addr, nil)

	// First dial: unknown server -> VerifyNew recorded, handshake succeeds.
	if err := dialTLS(t, cfg, addr); err != nil {
		t.Fatalf("first dial (New): %v", err)
	}
	known, _ := m.KnownServers()
	if _, ok := known[addr]; !ok {
		t.Fatal("TOFU entry not recorded after first handshake")
	} else if known[addr].Fingerprint != Fingerprint(serverA.Certificate) {
		t.Error("recorded fingerprint is not the server's leaf fingerprint")
	}

	// Second dial: Match, still succeeds.
	if err := dialTLS(t, cfg, addr); err != nil {
		t.Fatalf("second dial (Match): %v", err)
	}

	// Server presents a different certificate: fail closed by default.
	stop()
	addr2, stop2 := listenSameAddr(t, serverB, addr)
	defer stop2()
	err := dialTLS(t, cfg, addr2)
	if err == nil {
		t.Fatal("dial with swapped server cert must fail (fail-closed default)")
	}
	if !errors.Is(err, ErrServerCertChanged) {
		t.Errorf("error should wrap ErrServerCertChanged, got: %v", err)
	}

	// Stored fingerprint must still be the ORIGINAL server's.
	known, _ = m.KnownServers()
	if known[addr].Fingerprint != Fingerprint(serverA.Certificate) {
		t.Error("CHANGED must not overwrite the pinned fingerprint")
	}
}

func listenSameAddr(t *testing.T, srv *Identity, addr string) (string, func()) {
	t.Helper()
	var ln net.Listener
	var err error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("rebind %s: %v", addr, err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			tconn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{srv.TLSCertificate}})
			if err := tconn.Handshake(); err == nil {
				tconn.Write([]byte("ok"))
			}
			tconn.Close()
		}
	}()
	return addr, func() { ln.Close(); <-done }
}

func TestTLSConfigCallbackCanAcceptChanged(t *testing.T) {
	m := newTestManager(t)
	client, _ := m.Generate("Client2", "")
	serverA, _ := m.Generate("SA", "")
	serverB, _ := m.Generate("SB", "")

	addr, stop := startTLSServer(t, serverA, false)
	defer stop()

	cfg := m.TLSConfig(client, addr, nil)
	if err := dialTLS(t, cfg, addr); err != nil {
		t.Fatalf("pin server A: %v", err)
	}
	stop()

	addr2, stop2 := listenSameAddr(t, serverB, addr)
	defer stop2()

	var seenResults []VerifyResult
	accepting := func(result VerifyResult, stored, presented ServerEntry) error {
		seenResults = append(seenResults, result)
		return nil // T19 dialog: user clicked "accept anyway"
	}
	cfg2 := m.TLSConfig(client, addr2, accepting)
	if err := dialTLS(t, cfg2, addr2); err != nil {
		t.Fatalf("dial with accepting callback must succeed: %v", err)
	}
	if len(seenResults) != 1 || seenResults[0] != VerifyChanged {
		t.Errorf("callback results = %v, want [changed]", seenResults)
	}
	if stored, _ := m.KnownServers(); stored[addr].Fingerprint != Fingerprint(serverB.Certificate) {
		t.Error("accepted override must re-pin to the new fingerprint")
	}
}

func TestTLSConfigSendsClientCertificate(t *testing.T) {
	m := newTestManager(t)
	client, _ := m.Generate("Presented", "")
	server, _ := m.Generate("Srv", "")

	gotCert := make(chan *x509.Certificate, 1)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		srvConn := tls.Server(conn, &tls.Config{
			Certificates: []tls.Certificate{server.TLSCertificate},
			ClientAuth:   tls.RequireAnyClientCert,
		})
		if err := srvConn.Handshake(); err == nil {
			if pc := srvConn.ConnectionState().PeerCertificates; len(pc) > 0 {
				gotCert <- pc[0]
			}
		}
	}()

	cfg := m.TLSConfig(client, ln.Addr().String(), nil)
	cconn, err := tls.Dial("tcp", ln.Addr().String(), cfg)
	if err != nil {
		t.Fatalf("dial with client cert required: %v", err)
	}
	cconn.Close()

	select {
	case c := <-gotCert:
		if !c.Equal(client.Certificate) {
			t.Error("server did not receive the identity's client certificate")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server saw no client certificate")
	}
}

func TestTLSConfigWithoutIdentity(t *testing.T) {
	m := newTestManager(t)
	server, _ := m.Generate("AnonSrv", "")
	addr, stop := startTLSServer(t, server, false)
	defer stop()

	cfg := m.TLSConfig(nil, addr, nil)
	if cfg.GetClientCertificate != nil {
		t.Error("nil identity must not advertise a client certificate")
	}
	if err := dialTLS(t, cfg, addr); err != nil {
		t.Fatalf("anonymous dial: %v", err)
	}
}
