package secrets

import (
	"log/slog"
	"sync"
)

// Passwords is the Store Murmel uses, which also tells where it saves.
type Passwords interface {
	Store
	// State is where passwords go (Unknown until detected).
	State() State
}

// ErrNoKeyring is returned by a Manager without a keyring: nothing was
// saved, read or removed. The user interface already says so.
var ErrNoKeyring = &Error{Kind: Gone, msg: "no keyring"}

// Manager keeps passwords in the keyring while there is one. It detects
// the keyring once; when the keyring turns out to be gone, it stays without
// one until Murmel is started again.
type Manager struct {
	keyring  Store
	onChange func()
	detected chan struct{}

	mu    sync.Mutex
	state State
}

// NewManager starts detect on a goroutine and returns at once; calls wait
// for the result. onChange runs (on some goroutine) whenever State changes.
func NewManager(keyring Store, detect func() State, onChange func()) *Manager {
	m := &Manager{keyring: keyring, onChange: onChange, detected: make(chan struct{})}
	go func() {
		s := detect()
		slog.Info("passwords", "storage", s)
		m.mu.Lock()
		m.state = s
		m.mu.Unlock()
		m.onChange()
		close(m.detected)
	}()
	return m
}

// State implements Passwords.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// use waits for the detection and runs call on the keyring, unless there
// is none.
func (m *Manager) use(call func() error) error {
	<-m.detected
	if m.State() == NoKeyring {
		return ErrNoKeyring
	}
	err := call()
	if KindOf(err) == Gone {
		m.mu.Lock()
		changed := m.state != NoKeyring
		m.state = NoKeyring
		m.mu.Unlock()
		if changed {
			slog.Info("passwords: the keyring is gone", "err", err)
			m.onChange()
		}
	}
	return err
}

// Save implements Store.
func (m *Manager) Save(k Key, password string) error {
	return m.use(func() error { return m.keyring.Save(k, password) })
}

// Lookup implements Store.
func (m *Manager) Lookup(k Key) (string, error) {
	var pw string
	err := m.use(func() error {
		var err error
		pw, err = m.keyring.Lookup(k)
		return err
	})
	return pw, err
}

// Delete implements Store.
func (m *Manager) Delete(k Key) error {
	return m.use(func() error { return m.keyring.Delete(k) })
}
