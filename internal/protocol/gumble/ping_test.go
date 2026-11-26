package gumble

import (
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/one337/murmel/internal/protocol/gumble/MumbleProto"
)

// Ping replies arriving while a ping is being sent are counted without a
// data race (run with -race), and the sent ping carries a count that was
// valid at some point.
func TestPingCountsRepliesWhileSending(t *testing.T) {
	client, packets := framingConn(t)
	client.end = make(chan struct{})
	const replies = 200

	var wg sync.WaitGroup
	wg.Go(client.pingRoutine)
	wg.Go(func() {
		for range replies {
			if err := client.handlePing(nil); err != nil {
				t.Errorf("handlePing: %v", err)
			}
		}
	})

	p := <-packets
	if p.pType != 3 {
		t.Fatalf("first packet type %d, want 3 (Ping)", p.pType)
	}
	var ping MumbleProto.Ping
	if err := proto.Unmarshal(p.body, &ping); err != nil {
		t.Fatal(err)
	}
	if n := ping.GetTcpPackets(); n > replies {
		t.Errorf("ping reports %d received packets, more than the %d replies", n, replies)
	}
	close(client.end)
	wg.Wait()
}
