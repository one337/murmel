// Command regbot registers and verifies Mumble client identities against a
// live murmur server, dialing through the Murmel certs package's TLSConfig
// seam (client cert + TOFU).
//
// TEST-ONLY throwaway harness client for the T7 registration QA scenario.
// Not part of any build; lives under testdata so the go tool ignores it.
//
// -cert selects an identity slot inside -datadir: the first run generates and
// persists it, later runs reuse the same certificate, so "same cert" and
// "different cert" reconnects can be simulated across invocations.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"layeh.com/gumble/gumble"

	"io.github/murmel/internal/certs"
)

func main() {
	mode := flag.String("mode", "check", "register | check")
	addr := flag.String("addr", "localhost:64738", "server address")
	name := flag.String("name", "", "Mumble username (required)")
	datadir := flag.String("datadir", "", "throwaway certs data dir (required)")
	certSlot := flag.String("cert", "main", "identity slot label inside datadir")
	timeout := flag.Duration("timeout", 10*time.Second, "overall timeout")
	flag.Parse()

	if *name == "" || *datadir == "" {
		fmt.Fprintln(os.Stderr, "regbot: --name and --datadir are required")
		os.Exit(2)
	}

	m, err := certs.NewManager(certs.WithDataDir(*datadir))
	if err != nil {
		fail(err)
	}
	ident, fresh, err := identityForSlot(m, *certSlot)
	if err != nil {
		fail(err)
	}

	cfg := gumble.Config{Username: *name}
	tlsCfg := m.TLSConfig(ident, *addr, nil)

	client, err := gumble.DialWithDialer(&net.Dialer{Timeout: *timeout}, *addr, &cfg, tlsCfg)
	if err != nil {
		printJSON(map[string]any{
			"ok":             false,
			"stage":          "dial",
			"mode":           *mode,
			"username":       *name,
			"cert_slot":      *certSlot,
			"fresh_identity": fresh,
			"cert":           certs.Fingerprint(ident.Certificate),
			"error":          err.Error(),
		})
		os.Exit(1)
	}
	defer client.Conn.Close()

	if *mode == "register" {
		client.Self.Register()
		deadline := time.Now().Add(*timeout)
		for client.Self.UserID == 0 && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
	}

	printJSON(map[string]any{
		"ok":             client.Self.IsRegistered(),
		"stage":          *mode,
		"username":       *name,
		"user_id":        client.Self.UserID,
		"is_registered":  client.Self.IsRegistered(),
		"server_name":    client.Self.Name,
		"cert_slot":      *certSlot,
		"fresh_identity": fresh,
		"cert":           certs.Fingerprint(ident.Certificate),
	})
	if *mode == "register" && client.Self.UserID == 0 {
		os.Exit(1)
	}
}

// identityForSlot returns the persisted identity whose stored name equals the
// slot label, generating a fresh one on first use.
func identityForSlot(m *certs.Manager, slot string) (*certs.Identity, bool, error) {
	metas, err := m.List()
	if err != nil {
		return nil, false, err
	}
	for _, meta := range metas {
		if meta.Name == slot {
			id, err := m.Load(meta.ID)
			if err != nil {
				return nil, false, err
			}
			return id, false, nil
		}
	}
	id, err := m.Generate(slot, "")
	if err != nil {
		return nil, false, err
	}
	return id, true, nil
}

func printJSON(v map[string]any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "regbot: %v\n", err)
	os.Exit(1)
}
