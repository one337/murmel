package internal

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLayerBoundary enforces the architectural rule that the protocol and
// audio layers must never depend on GTK (gotk4). This keeps the network and
// audio code testable headlessly and prevents layer violations.
func TestLayerBoundary(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	repoRoot := filepath.Dir(filepath.Dir(thisFile))

	cmd := exec.Command("go", "list", "-deps",
		"./internal/protocol/...", "./internal/audio/...")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		// A guard that cannot run must fail, not pass silently.
		t.Fatalf("go list -deps protocol/audio: %v", err)
	}

	var deps []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "io.github/murmel/internal/protocol") ||
			strings.HasPrefix(line, "io.github/murmel/internal/audio") {
			continue
		}
		deps = append(deps, line)
	}

	if len(deps) == 0 {
		t.Fatal("go list -deps returned no dependencies: the check would pass vacuously")
	}

	for _, dep := range deps {
		if strings.Contains(dep, "diamondburned/gotk4") {
			t.Errorf("layer violation: %q must not depend on GTK (diamondburned/gotk4)", dep)
		}
	}
}
