package settings

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// ensureGSettings makes the Murmel schema resolvable for this test. It
// honors a pre-set GSETTINGS_SCHEMA_DIR; otherwise it compiles the schema
// from data/ into a temp dir. gio caches the default schema source at the
// first lookup, so the env var must be finalized BEFORE any gio call —
// hence the plain os.Getenv check first, and the skip when a user-provided
// dir does not actually contain the compiled schema.
func ensureGSettings(t *testing.T) *GSettings {
	t.Helper()

	// Backend choice is read when the first GSettings object is created;
	// the memory backend keeps the test hermetic (no dconf/D-Bus needed).
	t.Setenv("GSETTINGS_BACKEND", "memory")

	if os.Getenv("GSETTINGS_SCHEMA_DIR") == "" {
		bin, err := exec.LookPath("glib-compile-schemas")
		if err != nil {
			t.Skipf("schema not installed and glib-compile-schemas unavailable: %v", err)
		}
		target := t.TempDir()
		cmd := exec.Command(bin, "--strict", "--targetdir="+target, filepath.Dir(schemaFilePath(t)))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("glib-compile-schemas --strict failed: %v\n%s", err, out)
		}
		t.Setenv("GSETTINGS_SCHEMA_DIR", target)
	}

	if !SchemaAvailable() {
		t.Skipf("schema %s not resolvable; run glib-compile-schemas data/ first", SchemaID)
	}

	g, err := NewGSettings()
	if err != nil {
		t.Fatalf("NewGSettings: %v", err)
	}
	return g
}

// TestGSettingsRoundtrip drives every schema key through default → set →
// get → reset against real GSettings (memory backend), per the QA scenario:
// set vad-threshold=0.7, read back, reset, read default.
func TestGSettingsRoundtrip(t *testing.T) {
	g := ensureGSettings(t)

	for _, spec := range keySpecs {
		if got := spec.get(g); !valuesEqual(got, spec.def) {
			t.Errorf("%s: initial value %v, want schema default %v", spec.key, got, spec.def)
			continue
		}
		spec.set(g, spec.sample)
		if got := spec.get(g); !valuesEqual(got, spec.sample) {
			t.Errorf("%s: after set = %v, want %v", spec.key, got, spec.sample)
		}
		g.Reset(spec.key)
		if got := spec.get(g); !valuesEqual(got, spec.def) {
			t.Errorf("%s: after reset = %v, want default %v", spec.key, got, spec.def)
		}
	}

	// QA scenario steps, verbatim values.
	if g.VADThreshold() != 0.35 {
		t.Errorf("vad-threshold default = %v, want 0.35", g.VADThreshold())
	}
	g.SetVADThreshold(0.7)
	if g.VADThreshold() != 0.7 {
		t.Errorf("vad-threshold after set = %v, want 0.7", g.VADThreshold())
	}
	g.Reset("vad-threshold")
	if g.VADThreshold() != 0.35 {
		t.Errorf("vad-threshold after reset = %v, want 0.35", g.VADThreshold())
	}
}

// TestGSettingsChangedSignal verifies the changed notification: GSettings
// only emits changed for keys that were read while a handler was already
// connected, so the read happens after ConnectChanged.
func TestGSettingsChangedSignal(t *testing.T) {
	g := ensureGSettings(t)

	ch := make(chan string, 1)
	disconnect := g.ConnectChanged(func(key string) { ch <- key })
	defer disconnect()

	_ = g.VADThreshold() // prime the changed emission for this key

	g.SetVADThreshold(0.7)
	select {
	case key := <-ch:
		if key != "vad-threshold" {
			t.Fatalf("changed key = %q, want %q", key, "vad-threshold")
		}
	case <-time.After(5 * time.Second):
		// Asynchronous backends dispatch via the main context; pump it
		// briefly before giving up.
		ctx := glib.MainContextDefault()
		for ctx.Pending() {
			ctx.Iteration(false)
		}
		select {
		case key := <-ch:
			if key != "vad-threshold" {
				t.Fatalf("changed key = %q, want %q", key, "vad-threshold")
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("changed signal for vad-threshold not received within timeout")
		}
	}

	g.Reset("vad-threshold")
}

// TestGSettingsServerListSerialization pins the documented wire format:
// compact JSON per array element, malformed entries skipped on load.
func TestGSettingsServerListSerialization(t *testing.T) {
	g := ensureGSettings(t)

	want := []ServerEntry{sampleServer()}
	g.SetServers(want)
	if got := g.Servers(); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("server-list roundtrip = %+v, want %+v", got, want)
	}

	raw := g.gs.Strv("server-list")
	if len(raw) != 1 {
		t.Fatalf("server-list raw = %q, want 1 element", raw)
	}
	if s, err := unmarshalEntry(raw[0]); err != nil || s != want[0] {
		t.Fatalf("raw element %q does not round-trip: %+v, %v", raw[0], s, err)
	}

	g.gs.SetStrv("server-list", []string{"{not json", raw[0]})
	if got := g.Servers(); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("malformed entry not skipped: %+v", got)
	}
	g.Reset("server-list")
}

func TestEntryMarshalFormat(t *testing.T) {
	s, err := marshalEntry(sampleServer())
	if err != nil {
		t.Fatalf("marshalEntry: %v", err)
	}
	for _, want := range []string{
		`"name":"Community"`,
		`"host":"mumble.example.org"`,
		`"port":64738`,
		`"username":"alice"`,
		`"identity-id":"sha256:abcd1234"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("marshaled entry %q missing %s", s, want)
		}
	}
}

// TestNewFactory documents New()'s runtime selection: GSettings when the
// schema is resolvable in this environment, Memory otherwise.
func TestNewFactory(t *testing.T) {
	s := New()
	if SchemaAvailable() {
		if _, ok := s.(*GSettings); !ok {
			t.Fatalf("schema available but New() returned %T, want *GSettings", s)
		}
	} else if _, ok := s.(*Memory); !ok {
		t.Fatalf("schema unavailable but New() returned %T, want *Memory", s)
	}
}
