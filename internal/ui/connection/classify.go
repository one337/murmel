package connection

import (
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"strings"
	"syscall"

	"crypto/tls"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/certs"
)

// errorClass selects which distinct error dialog a connection failure
// produces (five actionable classes plus fallthroughs).
type errorClass int

const (
	classNone errorClass = iota
	classWrongPassword
	classBanned
	classServerFull
	classUnreachable
	classTLSFailure
	classOther
)

// errDeclinedTOFU marks a handshake aborted because the user declined a
// TOFU prompt; it must not produce an error dialog.
var errDeclinedTOFU = errors.New("connection: server certificate declined by user")

// classifyDialError maps a gumble.DialWithDialer failure to its dialog
// class. Rejections are inspected first (they carry the server's verdict),
// then certificate failures, then network reachability.
func classifyDialError(err error) errorClass {
	switch {
	case err == nil:
		return classNone
	case errors.Is(err, errDeclinedTOFU):
		return classNone
	case errors.Is(err, certs.ErrServerCertChanged):
		return classTLSFailure
	}

	if ev, ok := bus.TranslateReject(err); ok {
		return classifyReject(ev)
	}

	var certErr *tls.CertificateVerificationError
	var x509Err x509.ConstraintViolationError
	if errors.As(err, &certErr) ||
		errors.As(err, &x509Err) ||
		mentionsCert(err.Error()) {
		return classTLSFailure
	}

	var netErr net.Error
	var opErr *net.OpError
	if errors.As(err, &netErr) || errors.As(err, &opErr) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return classUnreachable
	}

	// The fork reports synchronization timeouts and truncated handshakes
	// ("server closed connection") as plain errors — both mean the server
	// could not be reached usefully.
	msg := err.Error()
	if strings.Contains(msg, "synchronization timeout") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") {
		return classUnreachable
	}
	return classOther
}

// classifyReject maps a bus ConnectError (already translated from a
// gumble.RejectError) to its dialog class. murmur reports a dial-time ban
// as a reject with no typed reason but a "banned" string.
func classifyReject(ev bus.ConnectError) errorClass {
	switch ev.Reason {
	case bus.RejectServerPassword, bus.RejectUserCredentials:
		return classWrongPassword
	case bus.RejectServerFull:
		return classServerFull
	}
	if strings.Contains(strings.ToLower(ev.Text), "ban") {
		return classBanned
	}
	return classOther
}

func mentionsCert(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "tls:") ||
		strings.Contains(m, "x509:") ||
		strings.Contains(m, "certificate") ||
		strings.Contains(m, "handshake")
}

// unreachableDetail strips Go error boilerplate ("dial tcp [::1]:64738:
// connect: connection refused") down to the actionable part for the
// dialog body.
func unreachableDetail(err error) string {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Err != nil {
			if urlErr := new(url.Error); errors.As(opErr.Err, &urlErr) && urlErr.Err != nil {
				return urlErr.Err.Error()
			}
			return opErr.Err.Error()
		}
		return opErr.Error()
	}
	return err.Error()
}
