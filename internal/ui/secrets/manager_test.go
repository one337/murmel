package secrets

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// scripted is a Store whose calls fail with err (nil: they work) and are
// counted.
type scripted struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (s *scripted) call() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}

func (s *scripted) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *scripted) Save(Key, string) error     { return s.call() }
func (s *scripted) Lookup(Key) (string, error) { return "pw", s.call() }
func (s *scripted) Delete(Key) error           { return s.call() }

func (s *scripted) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func detected(s State) func() State { return func() State { return s } }

func gone() error    { return &Error{Kind: Gone, msg: "gone"} }
func refused() error { return &Error{Kind: Refused, msg: "refused"} }

var k = Key{Host: "example.invalid", Port: 1, Username: "a"}

// Without a keyring nothing reaches it, and every call says so.
func TestManagerWithoutKeyring(t *testing.T) {
	s := &scripted{}
	m := NewManager(s, nil, detected(NoKeyring), func() {})
	if err := m.Save(k, "pw"); !errors.Is(err, ErrNoKeyring) {
		t.Errorf("Save: %v", err)
	}
	if pw, err := m.Lookup(k); pw != "" || !errors.Is(err, ErrNoKeyring) {
		t.Errorf("Lookup: %q, %v", pw, err)
	}
	if err := m.Delete(k); !errors.Is(err, ErrNoKeyring) {
		t.Errorf("Delete: %v", err)
	}
	if s.count() != 0 {
		t.Errorf("%d calls reached the keyring", s.count())
	}
	if m.State() != NoKeyring {
		t.Errorf("State %v", m.State())
	}
}

// A keyring that turns out to be gone is not used again; State changes
// once and onChange runs once for it.
func TestManagerKeyringGone(t *testing.T) {
	s := &scripted{}
	var changes atomic.Int32
	m := NewManager(s, nil, detected(InKeyring), func() { changes.Add(1) })
	if err := m.Save(k, "pw"); err != nil {
		t.Fatal(err)
	}
	if m.State() != InKeyring || changes.Load() != 1 {
		t.Fatalf("after detection: %v, %d changes", m.State(), changes.Load())
	}
	s.fail(gone())
	if err := m.Save(k, "pw"); KindOf(err) != Gone || errors.Is(err, ErrNoKeyring) {
		t.Errorf("Save: %v, want the keyring's own Gone error", err)
	}
	if m.State() != NoKeyring || changes.Load() != 2 {
		t.Errorf("after Gone: %v, %d changes", m.State(), changes.Load())
	}
	calls := s.count()
	if err := m.Delete(k); !errors.Is(err, ErrNoKeyring) {
		t.Errorf("Delete: %v", err)
	}
	if s.count() != calls || changes.Load() != 2 {
		t.Errorf("the keyring was used again (%d calls) or changed again (%d)", s.count()-calls, changes.Load())
	}
}

// A locked keyring stays the place for passwords.
func TestManagerKeyringRefused(t *testing.T) {
	s := &scripted{}
	s.fail(refused())
	var changes atomic.Int32
	m := NewManager(s, nil, detected(InKeyring), func() { changes.Add(1) })
	if err := m.Save(k, "pw"); KindOf(err) != Refused {
		t.Errorf("Save: %v", err)
	}
	s.fail(nil)
	if err := m.Save(k, "pw"); err != nil {
		t.Errorf("Save after unlocking: %v", err)
	}
	if m.State() != InKeyring || changes.Load() != 1 {
		t.Errorf("%v, %d changes", m.State(), changes.Load())
	}
}

// Calls wait for the detection; until then State is Unknown.
func TestManagerWaitsForDetection(t *testing.T) {
	s := &scripted{}
	release := make(chan struct{})
	m := NewManager(s, nil, func() State { <-release; return InKeyring }, func() {})
	if m.State() != Unknown {
		t.Errorf("State before detection: %v", m.State())
	}
	done := make(chan error)
	go func() { done <- m.Save(k, "pw") }()
	select {
	case err := <-done:
		t.Fatalf("Save did not wait for the detection (%v)", err)
	case <-time.After(100 * time.Millisecond):
	}
	if s.count() != 0 {
		t.Fatal("the keyring was used before the detection")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.count() != 1 || m.State() != InKeyring {
		t.Errorf("%d calls, State %v", s.count(), m.State())
	}
}
