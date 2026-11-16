package settings

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// countWrites counts the data file writes for the test; fail makes them
// fail.
func countWrites(t *testing.T, fail bool) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	orig := writeData
	writeData = func(path string, data []byte) error {
		n.Add(1)
		if fail {
			return errors.New("disk full")
		}
		return orig(path, data)
	}
	t.Cleanup(func() { writeData = orig })
	return &n
}

// oldSettings is a base with values in the former GSettings keys.
func oldSettings() *Memory {
	m := NewMemory()
	m.SetServers([]ServerEntry{sampleServer()})
	m.SetUserVolumes(map[string]float64{"h:1|cert:aa": 0.5})
	m.SetUserLocalMutes(map[string]bool{"h:1|name:bob": true})
	return m
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func checkValues(t *testing.T, s Settings) {
	t.Helper()
	if got := s.Servers(); !reflect.DeepEqual(got, []ServerEntry{sampleServer()}) {
		t.Errorf("Servers = %+v", got)
	}
	if got := s.UserVolumes(); !maps.Equal(got, map[string]float64{"h:1|cert:aa": 0.5}) {
		t.Errorf("UserVolumes = %v", got)
	}
	if got := s.UserLocalMutes(); !maps.Equal(got, map[string]bool{"h:1|name:bob": true}) {
		t.Errorf("UserLocalMutes = %v", got)
	}
}

func checkCleared(t *testing.T, base Settings) {
	t.Helper()
	if len(base.Servers()) != 0 || len(base.UserVolumes()) != 0 || len(base.UserLocalMutes()) != 0 {
		t.Errorf("old values not cleared: %v %v %v", base.Servers(), base.UserVolumes(), base.UserLocalMutes())
	}
}

func TestMigrationMovesOldValues(t *testing.T) {
	dir := t.TempDir()
	base := oldSettings()
	s := NewStore(base, dir)

	checkValues(t, s)
	checkCleared(t, base)
	for _, name := range []string{serversFile, localAudioFile} {
		path := filepath.Join(dir, name)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := fi.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", name, mode)
		}
		if v := readJSON(t, path)["version"]; v != float64(1) {
			t.Errorf("%s version = %v, want 1", name, v)
		}
	}

	// Next start: the values come from the files, GSettings stays empty.
	again := NewMemory()
	checkValues(t, NewStore(again, dir))
	checkCleared(t, again)
}

// The move works against real GSettings too, which are cleared after it.
func TestMigrationFromGSettings(t *testing.T) {
	g := ensureGSettings(t)
	g.SetServers([]ServerEntry{sampleServer()})
	g.SetUserVolumes(map[string]float64{"h:1|cert:aa": 0.5})
	g.SetUserLocalMutes(map[string]bool{"h:1|name:bob": true})
	t.Cleanup(func() {
		for _, k := range []string{"server-list", "user-volumes", "user-local-mutes"} {
			g.Reset(k)
		}
	})

	checkValues(t, NewStore(g, t.TempDir()))
	checkCleared(t, g)
}

func TestNoOldValuesWritesNothing(t *testing.T) {
	writes := countWrites(t, false)
	dir := t.TempDir()
	NewStore(NewMemory(), dir)
	if n := writes.Load(); n != 0 {
		t.Fatalf("%d writes without anything to move", n)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files written: %v", entries)
	}
}

func TestMigrationWriteFailsKeepsGSettings(t *testing.T) {
	countWrites(t, true)
	dir := t.TempDir()
	base := oldSettings()
	s := NewStore(base, dir)

	checkValues(t, s)    // usable in this session
	checkValues(t, base) // and still in GSettings for the next try
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files left: %v", entries)
	}
}

func TestMigrationReadBackMismatchKeepsGSettings(t *testing.T) {
	orig := writeData
	writeData = func(path string, data []byte) error {
		// A file that reads back as something else.
		return orig(path, []byte(`{"version": 1, "servers": [], "volumes": {"x": 1.5}}`))
	}
	t.Cleanup(func() { writeData = orig })
	dir := t.TempDir()
	base := oldSettings()
	NewStore(base, dir)

	checkValues(t, base)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files left after a failed check: %v", entries)
	}
}

// With a file present, it is used and the old keys are left alone.
func TestExistingFileWinsOverGSettings(t *testing.T) {
	dir := t.TempDir()
	NewStore(oldSettings(), dir) // writes the files

	base := NewMemory()
	other := []ServerEntry{{Name: "Other", Host: "other.example.org", Port: 1}}
	base.SetServers(other)
	base.SetUserVolumes(map[string]float64{"z": 2})
	s := NewStore(base, dir)

	checkValues(t, s)
	if got := base.Servers(); !reflect.DeepEqual(got, other) {
		t.Errorf("old server-list changed: %+v", got)
	}
	if got := base.UserVolumes(); !maps.Equal(got, map[string]float64{"z": 2}) {
		t.Errorf("old user-volumes changed: %v", got)
	}
}

func TestCorruptFileIsMovedAside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serversFile)
	broken := []byte(`{"version": 1, "servers": [`)
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(oldSettings(), dir)
	s.servers.delay = time.Millisecond

	if got := s.Servers(); len(got) != 0 {
		t.Fatalf("Servers = %+v, want empty", got)
	}
	aside, _ := filepath.Glob(path + ".corrupt-*")
	if len(aside) != 1 {
		t.Fatalf("moved-aside files: %v", aside)
	}
	if data, _ := os.ReadFile(aside[0]); string(data) != string(broken) {
		t.Fatalf("moved-aside content = %q", data)
	}

	s.SetServers([]ServerEntry{sampleServer()})
	s.Flush()
	if got := NewStore(NewMemory(), dir).Servers(); !reflect.DeepEqual(got, []ServerEntry{sampleServer()}) {
		t.Fatalf("after a change: %+v", got)
	}
	if data, _ := os.ReadFile(aside[0]); string(data) != string(broken) {
		t.Fatal("moved-aside file was changed")
	}
}

// A second unreadable file never replaces one moved aside before.
func TestCorruptFilesAreKeptApart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serversFile)
	for _, content := range []string{"first", "second"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		NewStore(NewMemory(), dir)
	}
	aside, _ := filepath.Glob(path + ".corrupt-*")
	if len(aside) != 2 {
		t.Fatalf("moved-aside files: %v", aside)
	}
}

func TestFileWithoutVersionIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, localAudioFile)
	if err := os.WriteFile(path, []byte(`{"volumes": {"a": 0.5}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(NewMemory(), dir)
	if got := s.UserVolumes(); len(got) != 0 {
		t.Fatalf("UserVolumes = %v, want empty", got)
	}
	if aside, _ := filepath.Glob(path + ".corrupt-*"); len(aside) != 1 {
		t.Fatalf("moved-aside files: %v", aside)
	}
}

// A file from a newer version is read but never overwritten.
func TestNewerVersionIsNotOverwritten(t *testing.T) {
	writes := countWrites(t, false)
	dir := t.TempDir()
	path := filepath.Join(dir, localAudioFile)
	newer := []byte(`{"version": 2, "volumes": {"a": 0.5}, "mutes": {}, "later": [1]}`)
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(NewMemory(), dir)
	s.audio.delay = time.Millisecond
	if got := s.UserVolumes(); !maps.Equal(got, map[string]float64{"a": 0.5}) {
		t.Fatalf("UserVolumes = %v", got)
	}

	s.SetUserVolumes(map[string]float64{"a": 1.5})
	time.Sleep(20 * time.Millisecond)
	s.Flush()
	if n := writes.Load(); n != 0 {
		t.Fatalf("%d writes to a newer file", n)
	}
	if data, _ := os.ReadFile(path); string(data) != string(newer) {
		t.Fatalf("newer file changed: %s", data)
	}
}

// Quick changes in a row are written once, with the last value.
func TestQuickChangesAreWrittenOnce(t *testing.T) {
	writes := countWrites(t, false)
	dir := t.TempDir()
	s := NewStore(NewMemory(), dir)
	s.audio.delay = 100 * time.Millisecond

	for i := 1; i <= 20; i++ {
		s.SetUserVolumes(map[string]float64{"a": float64(i) / 10})
	}
	deadline := time.Now().Add(2 * time.Second)
	for writes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if n := writes.Load(); n != 1 {
		t.Fatalf("%d writes, want 1", n)
	}
	if got := NewStore(NewMemory(), dir).UserVolumes(); got["a"] != 2 {
		t.Fatalf("saved volume = %v, want 2", got["a"])
	}
}

// Changes that keep coming for longer than the delay (a slow drag) are
// still written once, after the last one.
func TestSteadyChangesAreWrittenOnceAfterTheLast(t *testing.T) {
	writes := countWrites(t, false)
	dir := t.TempDir()
	s := NewStore(NewMemory(), dir)
	s.audio.delay = 100 * time.Millisecond

	for i := 1; i <= 10; i++ { // 10 × 40 ms: four times the delay
		s.SetUserVolumes(map[string]float64{"a": float64(i) / 10})
		time.Sleep(40 * time.Millisecond)
	}
	if n := writes.Load(); n != 0 {
		t.Fatalf("%d writes while changes kept coming", n)
	}
	time.Sleep(250 * time.Millisecond)
	if n := writes.Load(); n != 1 {
		t.Fatalf("%d writes, want 1", n)
	}
	if got := NewStore(NewMemory(), dir).UserVolumes(); got["a"] != 1 {
		t.Fatalf("saved volume = %v, want 1", got["a"])
	}
}

// Quitting right after a change loses nothing: Flush writes it.
func TestFlushWritesPendingChange(t *testing.T) {
	writes := countWrites(t, false)
	dir := t.TempDir()
	s := NewStore(NewMemory(), dir)
	s.SetServers([]ServerEntry{sampleServer()})
	s.SetUserLocalMutes(map[string]bool{"b": true})
	s.Flush()

	if n := writes.Load(); n != 2 {
		t.Fatalf("%d writes on flush, want 2", n)
	}
	again := NewStore(NewMemory(), dir)
	if got := again.Servers(); !reflect.DeepEqual(got, []ServerEntry{sampleServer()}) {
		t.Fatalf("Servers after restart = %+v", got)
	}
	if got := again.UserLocalMutes(); !got["b"] {
		t.Fatalf("UserLocalMutes after restart = %v", got)
	}
	time.Sleep(saveDelay + 100*time.Millisecond)
	if n := writes.Load(); n != 2 {
		t.Fatalf("written again after flush: %d writes", n)
	}
}

func TestResetEmptiesDataFileValues(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(oldSettings(), dir)
	s.Reset("server-list")
	s.Reset("user-volumes")
	s.Reset("user-local-mutes")
	s.Flush()
	checkCleared(t, NewStore(NewMemory(), dir))
}

// Without a data directory the values live in memory only.
func TestStoreWithoutDataDir(t *testing.T) {
	writes := countWrites(t, false)
	s := NewStore(oldSettings(), "")
	checkValues(t, s)
	s.SetServers(nil)
	s.Flush()
	if n := writes.Load(); n != 0 {
		t.Fatalf("%d writes without a data directory", n)
	}
}

func TestDataFileFormat(t *testing.T) {
	dir := t.TempDir()
	NewStore(oldSettings(), dir)
	data, err := os.ReadFile(filepath.Join(dir, serversFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"version": 1`, `"identity-id": "sha256:abcd1234"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("servers.json lacks %s:\n%s", want, data)
		}
	}
}
