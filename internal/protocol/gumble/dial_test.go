package gumble

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// A dial given up during the TLS handshake (here: a server that never
// answers it) returns at once and sends nothing beyond the handshake.
func TestDialContextGivesUpDuringHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	received := make(chan int, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		n, _ := io.Copy(io.Discard, c) // the ClientHello, then EOF
		received <- int(n)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err = DialContext(ctx, new(net.Dialer), ln.Addr().String(), NewConfig(), &tls.Config{InsecureSkipVerify: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("gave up after %v", d)
	}
	select {
	case n := <-received:
		// A ClientHello is a single TLS record of a few hundred bytes;
		// an authentication would follow only after the handshake.
		if n == 0 || n > 2048 {
			t.Fatalf("server received %d bytes", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the connection stayed open")
	}
}

func TestDialContextCancelledBeforehand(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DialContext(ctx, new(net.Dialer), ln.Addr().String(), NewConfig(), &tls.Config{InsecureSkipVerify: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
