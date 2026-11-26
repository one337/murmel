package gumble

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/one337/murmel/internal/protocol/gumble/MumbleProto"
	"google.golang.org/protobuf/proto"
)

// newHandlerTestClient builds a client as DialContext does, waiting for
// ServerSync, on a pipe whose far end discards everything the client
// writes.
func newHandlerTestClient(t testing.TB) *Client {
	near, far := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, far) }()
	t.Cleanup(func() {
		_ = near.Close()
		_ = far.Close()
	})
	return newClient(near, NewConfig())
}

func marshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// runHandler calls a message handler and fails the test if it panics or
// does not return.
func runHandler(t *testing.T, name string, handle func([]byte) error, data []byte) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		_ = handle(data)
	}()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("%s panicked: %v", name, p)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not return", name)
	}
}

func TestRepeatedServerSync(t *testing.T) {
	c := newHandlerTestClient(t)
	msg := marshal(t, &MumbleProto.ServerSync{Session: proto.Uint32(1)})
	runHandler(t, "first ServerSync", c.handleServerSync, msg)
	runHandler(t, "second ServerSync", c.handleServerSync, msg)
	if err, ok := <-c.connect; err != nil || ok {
		t.Fatalf("Dial got %v (open %v), want a closed channel", err, ok)
	}
}

func TestRejectWithoutWaitingDial(t *testing.T) {
	c := newHandlerTestClient(t)
	reject := marshal(t, &MumbleProto.Reject{
		Type:   MumbleProto.Reject_ServerFull.Enum(),
		Reason: proto.String("full"),
	})
	runHandler(t, "first Reject", c.handleReject, reject)
	runHandler(t, "second Reject", c.handleReject, reject)
	err := <-c.connect
	if err == nil || err.Reason != "full" {
		t.Fatalf("Dial got %v, want the rejection", err)
	}
}

func TestRejectBeforeServerSyncWins(t *testing.T) {
	c := newHandlerTestClient(t)
	runHandler(t, "Reject", c.handleReject, marshal(t, &MumbleProto.Reject{Reason: proto.String("no")}))
	runHandler(t, "ServerSync", c.handleServerSync, marshal(t, &MumbleProto.ServerSync{}))
	if err := <-c.connect; err == nil || err.Reason != "no" {
		t.Fatalf("Dial got %v, want the rejection", err)
	}
}

// FuzzHandleMessages feeds a sequence of server messages to the message
// handlers; none may panic or block. Each message is a type byte, a length
// byte and that many bytes of body.
func FuzzHandleMessages(f *testing.F) {
	f.Add([]byte{5, 2, 8, 1})                   // ServerSync
	f.Add([]byte{9, 2, 8, 1, 5, 2, 8, 1, 4, 0}) // UserState, ServerSync, Reject
	f.Fuzz(func(t *testing.T, in []byte) {
		c := newHandlerTestClient(t)
		for len(in) >= 2 {
			typ, n := int(in[0])%len(handlers), min(int(in[1]), len(in)-2)
			body := in[2 : 2+n]
			in = in[2+n:]
			handle := func(b []byte) error { return handlers[typ](c, b) }
			runHandler(t, fmt.Sprintf("message type %d", typ), handle, body)
		}
	})
}
