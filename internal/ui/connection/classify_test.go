package connection

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"

	"crypto/tls"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/certs"
	"io.github/murmel/internal/protocol/gumble"
)

func TestClassifyRejectTypes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want errorClass
	}{
		{"server password", &gumble.RejectError{Type: gumble.RejectServerPassword}, classWrongPassword},
		{"user credentials", &gumble.RejectError{Type: gumble.RejectUserCredentials}, classWrongPassword},
		{"server full", &gumble.RejectError{Type: gumble.RejectServerFull}, classServerFull},
		{"banned string", &gumble.RejectError{Reason: "You are banned."}, classBanned},
		{"username in use", &gumble.RejectError{Type: gumble.RejectUsernameInUse}, classOther},
		{"wrapped reject", fmt.Errorf("gumble: dial: %w", gumble.RejectError{Type: gumble.RejectServerPassword}), classWrongPassword},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyDialError(tc.err); got != tc.want {
				t.Fatalf("classifyDialError = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyUnreachable(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"op error refused", &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}},
		{"dns failure", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("no such host")}},
		{"sync timeout", errors.New("gumble: synchronization timeout")},
		{"wrapped op error", fmt.Errorf("dial %s: %w", "localhost:1", &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyDialError(tc.err); got != classUnreachable {
				t.Fatalf("classifyDialError(%v) = %v, want classUnreachable", tc.err, got)
			}
		})
	}
}

func TestClassifyTLSFailure(t *testing.T) {
	certErr := &tls.CertificateVerificationError{Err: x509.CertificateInvalidError{}}
	cases := []struct {
		name string
		err  error
	}{
		{"verification error", certErr},
		{"wrapped verification", fmt.Errorf("handshake: %w", certErr)},
		{"handshake failure", errors.New("tls: handshake failure")},
		{"cert parse", errors.New("certs: parsing server certificate: x509: malformed certificate")},
		{"changed pin", fmt.Errorf("certs: %w: aa (pinned: bb)", certs.ErrServerCertChanged)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyDialError(tc.err); got != classTLSFailure {
				t.Fatalf("classifyDialError(%v) = %v, want classTLSFailure", tc.err, got)
			}
		})
	}
}

func TestClassifyDeclinedProducesNoDialog(t *testing.T) {
	if got := classifyDialError(errDeclinedTOFU); got != classNone {
		t.Fatalf("declined TOFU classified %v, want classNone", got)
	}
}

func TestClassifyBusConnectError(t *testing.T) {
	cases := []struct {
		ev   bus.ConnectError
		want errorClass
	}{
		{bus.ConnectError{Reason: bus.RejectServerPassword}, classWrongPassword},
		{bus.ConnectError{Reason: bus.RejectServerFull}, classServerFull},
		{bus.ConnectError{Text: "banned: try again later"}, classBanned},
		{bus.ConnectError{Reason: bus.RejectUserName, Text: "bad name"}, classOther},
	}
	for _, tc := range cases {
		if got := classifyReject(tc.ev); got != tc.want {
			t.Fatalf("classifyReject(%+v) = %v, want %v", tc.ev, got, tc.want)
		}
	}
}

func TestUnreachableDetailStripsBoilerplate(t *testing.T) {
	inner := os.NewSyscallError("connect", syscall.ECONNREFUSED)
	err := fmt.Errorf("dial tcp 127.0.0.1:64738: %w", &net.OpError{Op: "dial", Net: "tcp", Err: inner})
	got := unreachableDetail(err)
	if got != inner.Error() {
		t.Fatalf("unreachableDetail = %q, want %q", got, inner.Error())
	}
}
