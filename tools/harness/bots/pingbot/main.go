// Package main implements pingbot: a UDP ping probe for Mumble servers that
// prints a JSON summary and never hangs.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"layeh.com/gumble/gumble"
)

func main() {
	addr := flag.String("addr", "localhost:64738", "server address (host:port)")
	timeout := flag.Duration("timeout", 5*time.Second, "give up after this duration")
	flag.Parse()

	resp, err := gumble.Ping(*addr, 0, *timeout)
	if err != nil {
		fail(err)
	}

	major, minor, patch := resp.Version.SemanticVersion()
	printJSON(map[string]any{
		"ok":          true,
		"address":     resp.Address.String(),
		"version":     fmt.Sprintf("%d.%d.%d", major, minor, patch),
		"version_raw": resp.Version.Version,
		"users":       resp.ConnectedUsers,
		"max_users":   resp.MaximumUsers,
		"max_bitrate": resp.MaximumBitrate,
		"latency_ms":  float64(resp.Ping.Microseconds()) / 1000.0,
	})
}

func fail(err error) {
	printJSON(map[string]any{
		"ok":    false,
		"error": err.Error(),
	})
	fmt.Fprintf(os.Stderr, "pingbot: %v\n", err)
	os.Exit(2)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "pingbot: json encode: %v\n", err)
		os.Exit(2)
	}
}
