package settings

import (
	"maps"
	"testing"
)

func TestMemoryDefaults(t *testing.T) {
	m := NewMemory()
	for _, spec := range keySpecs {
		if got := spec.get(m); !valuesEqual(got, spec.def) {
			t.Errorf("%s: default = %v, want %v", spec.key, got, spec.def)
		}
	}
}

func TestMemorySetGetRoundtrip(t *testing.T) {
	m := NewMemory()
	for _, spec := range keySpecs {
		spec.set(m, spec.sample)
		if got := spec.get(m); !valuesEqual(got, spec.sample) {
			t.Errorf("%s: after set = %v, want %v", spec.key, got, spec.sample)
		}
		spec.set(m, spec.def)
		if got := spec.get(m); !valuesEqual(got, spec.def) {
			t.Errorf("%s: after set-default = %v, want %v", spec.key, got, spec.def)
		}
	}
}

func TestMemoryResetRestoresDefault(t *testing.T) {
	m := NewMemory()
	for _, spec := range keySpecs {
		spec.set(m, spec.sample)
		m.Reset(spec.key)
		if got := spec.get(m); !valuesEqual(got, spec.def) {
			t.Errorf("%s: after reset = %v, want default %v", spec.key, got, spec.def)
		}
	}
}

func TestMemoryChangedSignal(t *testing.T) {
	m := NewMemory()

	var got []string
	disconnect := m.ConnectChanged(func(key string) { got = append(got, key) })

	m.SetVADThreshold(0.7)
	m.SetAutoReconnect(false)
	m.Reset("vad-threshold")

	want := []string{"vad-threshold", "auto-reconnect", "vad-threshold"}
	if len(got) != len(want) {
		t.Fatalf("changed notifications = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("changed notifications = %v, want %v", got, want)
		}
	}

	disconnect()
	m.SetAutoReconnect(true)
	if len(got) != len(want) {
		t.Fatalf("handler invoked after disconnect: %v", got)
	}
}

func TestMemoryServersDefensiveCopy(t *testing.T) {
	m := NewMemory()
	in := []ServerEntry{sampleServer()}
	m.SetServers(in)
	in[0].Name = "mutated"

	got := m.Servers()
	if len(got) != 1 || got[0].Name != "Community" {
		t.Fatalf("Servers() = %+v, want the entry stored at SetServers time", got)
	}
	got[0].Host = "mutated"
	if again := m.Servers(); again[0].Host != "mumble.example.org" {
		t.Fatalf("Servers() result aliases stored state: %+v", again)
	}
}

func TestMemoryDefaultServerListEmpty(t *testing.T) {
	m := NewMemory()
	servers := m.Servers()
	if servers == nil || len(servers) != 0 {
		t.Fatalf("default Servers() = %#v, want empty non-nil slice", servers)
	}
}

func TestMemorySetServersNilNormalizes(t *testing.T) {
	m := NewMemory()
	m.SetServers(nil)
	if servers := m.Servers(); servers == nil || len(servers) != 0 {
		t.Fatalf("after SetServers(nil), Servers() = %#v, want empty non-nil slice", servers)
	}
}

// valuesEqual compares spec values, treating []ServerEntry elementwise so
// that empty-but-non-nil slices equal nil defaults.
func valuesEqual(a, b any) bool {
	if as, ok := a.([]ServerEntry); ok {
		bs, ok := b.([]ServerEntry)
		if !ok || len(as) != len(bs) {
			return false
		}
		for i := range as {
			if as[i] != bs[i] {
				return false
			}
		}
		return true
	}
	if am, ok := a.(map[string]string); ok {
		bm, ok := b.(map[string]string)
		return ok && maps.Equal(am, bm)
	}
	return a == b
}
