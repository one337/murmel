package secrets

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

// memKeyring keeps passwords in memory. failSave, when set, makes Save
// fail for the key it returns an error for; mangle changes what Lookup
// returns.
type memKeyring struct {
	mu       sync.Mutex
	pw       map[Key]string
	failSave func(Key) error
	mangle   bool
}

func newMemKeyring() *memKeyring { return &memKeyring{pw: map[Key]string{}} }

func (m *memKeyring) Save(k Key, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSave != nil {
		if err := m.failSave(k); err != nil {
			return err
		}
	}
	m.pw[k] = password
	return nil
}

func (m *memKeyring) Lookup(k Key) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mangle && m.pw[k] != "" {
		return m.pw[k] + "x", nil
	}
	return m.pw[k], nil
}

func (m *memKeyring) Delete(k Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pw, k)
	return nil
}

func (m *memKeyring) has(k Key) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pw[k]
}

func settled(t *testing.T, m *Manager) *Manager {
	t.Helper()
	<-m.detected
	return m
}

var k2 = Key{Host: "two.invalid", Port: 2, Username: "b"}

// Without a keyring the file is offered; turned on, passwords go there,
// turned off, it is gone with them.
func TestManagerFileOptIn(t *testing.T) {
	kr, f := newMemKeyring(), newTestFile(t)
	var changes atomic.Int32
	m := settled(t, NewManager(kr, f, detected(NoKeyring), func() { changes.Add(1) }))
	if m.State() != NoKeyring || !m.File().Offered || m.File().Exists {
		t.Fatalf("before: %v %+v", m.State(), m.File())
	}
	if err := m.Save(k, "pw"); !errors.Is(err, ErrNoKeyring) {
		t.Errorf("Save before opting in: %v", err)
	}
	if err := m.UseFile(); err != nil {
		t.Fatal(err)
	}
	if m.State() != InFile || changes.Load() != 2 {
		t.Errorf("after UseFile: %v, %d changes", m.State(), changes.Load())
	}
	if err := m.Save(k, "pw"); err != nil {
		t.Fatal(err)
	}
	if pw, err := m.Lookup(k); pw != "pw" || err != nil {
		t.Errorf("Lookup: %q, %v", pw, err)
	}
	if kr.has(k) != "" {
		t.Error("the keyring got the password")
	}
	if info := m.File(); info.Count != 1 || info.Path != f.Path() {
		t.Errorf("File: %+v", info)
	}
	if err := m.StopUsingFile(); err != nil {
		t.Fatal(err)
	}
	if m.State() != NoKeyring || f.Exists() {
		t.Errorf("after StopUsingFile: %v, exists %v", m.State(), f.Exists())
	}
}

// With a keyring the file is neither offered nor created.
func TestManagerFileNotWithKeyring(t *testing.T) {
	f := newTestFile(t)
	m := settled(t, NewManager(newMemKeyring(), f, detected(InKeyring), func() {}))
	if m.File().Offered {
		t.Error("offered with a keyring")
	}
	if err := m.UseFile(); err == nil || f.Exists() {
		t.Errorf("UseFile with a keyring: %v, exists %v", err, f.Exists())
	}
}

// A keyring that goes away while Murmel runs does not send passwords to
// the file, even with the file there, until the next start.
func TestManagerKeyringGoneNoFile(t *testing.T) {
	s, f := &scripted{}, newTestFile(t)
	if err := f.Create(); err != nil {
		t.Fatal(err)
	}
	m := settled(t, NewManager(s, f, detected(InKeyring), func() {}))
	s.fail(gone())
	if err := m.Save(k, "pw"); KindOf(err) != Gone {
		t.Fatalf("Save: %v", err)
	}
	if err := m.Save(k, "pw"); !errors.Is(err, ErrNoKeyring) {
		t.Errorf("Save after Gone: %v", err)
	}
	if m.State() != NoKeyring || m.File().Offered {
		t.Errorf("State %v, offered %v", m.State(), m.File().Offered)
	}
	if keys, _ := f.Entries(); len(keys) != 0 {
		t.Errorf("the file got %v", keys)
	}
	if err := m.UseFile(); err == nil {
		t.Error("UseFile after the keyring went away")
	}
}

// With a keyring and a file left from before, the keyring is read first,
// the file second; saving or forgetting takes the entry out of the file.
func TestManagerFileLeftNextToKeyring(t *testing.T) {
	kr, f := newMemKeyring(), newTestFile(t)
	if err := f.Create(); err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct {
		k  Key
		pw string
	}{{k, "old"}, {k2, "file"}} {
		if err := f.Save(e.k, e.pw); err != nil {
			t.Fatal(err)
		}
	}
	m := settled(t, NewManager(kr, f, detected(InKeyring), func() {}))
	if m.State() != InKeyring || m.File().Count != 2 {
		t.Fatalf("%v %+v", m.State(), m.File())
	}
	if pw, err := m.Lookup(k2); pw != "file" || err != nil {
		t.Errorf("Lookup falls back to the file: %q, %v", pw, err)
	}
	if err := m.Save(k, "new"); err != nil {
		t.Fatal(err)
	}
	if kr.has(k) != "new" {
		t.Error("not saved in the keyring")
	}
	if pw, _ := f.Lookup(k); pw != "" {
		t.Errorf("still in the file: %q", pw)
	}
	if err := m.Delete(k2); err != nil {
		t.Fatal(err)
	}
	if pw, _ := f.Lookup(k2); pw != "" {
		t.Errorf("forgotten password still in the file: %q", pw)
	}
}

func fileWithTwo(t *testing.T) *File {
	t.Helper()
	f := newTestFile(t)
	if err := f.Create(); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(k, "one"); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(k2, "two"); err != nil {
		t.Fatal(err)
	}
	return f
}

// Moving puts every password in the keyring, then deletes the file and
// the damaged copies.
func TestManagerMoveToKeyring(t *testing.T) {
	kr, f := newMemKeyring(), fileWithTwo(t)
	damaged := f.Path() + ".corrupt-20260101-000000"
	if err := os.WriteFile(damaged, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var changes atomic.Int32
	m := settled(t, NewManager(kr, f, detected(InKeyring), func() { changes.Add(1) }))
	if err := m.MoveToKeyring(); err != nil {
		t.Fatal(err)
	}
	if kr.has(k) != "one" || kr.has(k2) != "two" {
		t.Errorf("keyring has %q, %q", kr.has(k), kr.has(k2))
	}
	if f.Exists() || len(f.Damaged()) != 0 {
		t.Errorf("left: exists %v, damaged %v", f.Exists(), f.Damaged())
	}
	if changes.Load() != 2 {
		t.Errorf("%d changes", changes.Load())
	}
}

// Any failure while moving keeps the whole file.
func TestManagerMoveFailureKeepsFile(t *testing.T) {
	for name, setup := range map[string]func(*memKeyring){
		"second fails": func(kr *memKeyring) {
			kr.failSave = func(key Key) error {
				if key == k2 {
					return &Error{Kind: Other, msg: "failed"}
				}
				return nil
			}
		},
		"locked":       func(kr *memKeyring) { kr.failSave = func(Key) error { return refused() } },
		"reads differ": func(kr *memKeyring) { kr.mangle = true },
	} {
		kr, f := newMemKeyring(), fileWithTwo(t)
		setup(kr)
		before, _ := os.ReadFile(f.Path())
		m := settled(t, NewManager(kr, f, detected(InKeyring), func() {}))
		if err := m.MoveToKeyring(); err == nil {
			t.Errorf("%s: no error", name)
		}
		if after, _ := os.ReadFile(f.Path()); string(after) != string(before) {
			t.Errorf("%s: the file changed", name)
		}
		if m.State() != InKeyring {
			t.Errorf("%s: State %v", name, m.State())
		}
	}
}

// Moving is only possible with a keyring and a file.
func TestManagerMoveNeedsKeyringAndFile(t *testing.T) {
	f := fileWithTwo(t)
	m := settled(t, NewManager(newMemKeyring(), f, detected(NoKeyring), func() {}))
	if err := m.MoveToKeyring(); err == nil {
		t.Error("moved without a keyring")
	}
	if !f.Exists() {
		t.Error("file gone")
	}
	m = settled(t, NewManager(newMemKeyring(), newTestFile(t), detected(InKeyring), func() {}))
	if err := m.MoveToKeyring(); err == nil {
		t.Error("moved without a file")
	}
}
