// Command setupbot idempotently creates the harness channels (Lobby, TestChan)
// under the server root. It connects as SuperUser because permanent channel
// creation requires registered/admin privileges on a default murmur.
package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"layeh.com/gumble/gumble"
	"layeh.com/gumble/gumbleutil"
)

var channelNames = []string{"Lobby", "TestChan"}

func main() {
	addr := flag.String("addr", "localhost:64738", "server address (host:port)")
	name := flag.String("name", "SuperUser", "login name (SuperUser can create channels)")
	password := flag.String("password", "murmelqa", "SuperUser password (harness QA value)")
	timeout := flag.Duration("timeout", 15*time.Second, "connect timeout")
	flag.Parse()

	os.Exit(run(*addr, *name, *password, *timeout))
}

func run(addr, name, password string, timeout time.Duration) int {
	denied := make(chan string, len(channelNames))
	config := gumble.NewConfig()
	config.Username = name
	config.Password = password
	config.Attach(gumbleutil.Listener{
		PermissionDenied: func(e *gumble.PermissionDeniedEvent) {
			select {
			case denied <- e.String:
			default:
			}
		},
	})

	tlsConfig := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // QA server uses a self-signed certificate
	client, err := gumble.DialWithDialer(&net.Dialer{Timeout: timeout}, addr, config, tlsConfig)
	if err != nil {
		return fail(fmt.Errorf("connect: %w", err))
	}
	defer client.Disconnect() //nolint:errcheck // best-effort; may already be gone

	var created, existed []string
	for _, channelName := range channelNames {
		if findChannel(client, channelName) != nil {
			existed = append(existed, channelName)
			continue
		}
		if err := createChannel(client, channelName, denied); err != nil {
			return fail(err)
		}
		created = append(created, channelName)
	}

	printJSON(map[string]any{
		"ok":      true,
		"created": created,
		"existed": existed,
	})
	return 0
}

func createChannel(client *gumble.Client, channelName string, denied <-chan string) error {
	var root *gumble.Channel
	client.Do(func() {
		root = client.Channels.Find()
	})
	if root == nil {
		return fmt.Errorf("server sent no root channel")
	}

	// Channel.Add is fire-and-forget: it only writes a ChannelState message,
	// so poll until the server echoes the channel back.
	root.Add(channelName, false)
	deadline := time.Now().Add(5 * time.Second)
	for findChannel(client, channelName) == nil {
		select {
		case msg := <-denied:
			return fmt.Errorf("permission denied creating channel %q: %s", channelName, msg)
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for channel %q to appear", channelName)
		}
	}
	return nil
}

func findChannel(client *gumble.Client, name string) *gumble.Channel {
	var ch *gumble.Channel
	client.Do(func() {
		ch = client.Channels.Find(name)
	})
	return ch
}

func fail(err error) int {
	printJSON(map[string]any{
		"ok":    false,
		"error": err.Error(),
	})
	fmt.Fprintf(os.Stderr, "setupbot: %v\n", err)
	return 1
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "setupbot: json encode: %v\n", err)
	}
}
