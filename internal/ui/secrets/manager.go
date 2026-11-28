package secrets

import (
	"errors"
	"log/slog"
	"sync"
)

// Passwords is the Store Murmel uses, which also tells where it saves.
type Passwords interface {
	Store
	// State is where passwords go (Unknown until detected).
	State() State
	// File describes the unencrypted password file.
	File() FileInfo
	// UseFile starts keeping passwords in the file (only offered without
	// a keyring); StopUsingFile deletes it with all passwords in it.
	UseFile() error
	StopUsingFile() error
	// MoveToKeyring moves every password from the file into the keyring
	// and then deletes the file; on any failure the file stays whole.
	MoveToKeyring() error
}

// FileInfo describes the unencrypted password file.
type FileInfo struct {
	Path string
	// Exists: the file is there (it holds Count passwords).
	Exists bool
	Count  int
	// Damaged lists copies of unreadable files that were set aside.
	Damaged []string
	// Offered: there is no keyring, so the file can be turned on.
	Offered bool
}

// ErrNoKeyring is returned by a Manager without a keyring: nothing was
// saved, read or removed. The user interface already says so.
var ErrNoKeyring = &Error{Kind: Gone, msg: "no keyring"}

// errNotHere is returned for file operations that do not fit the state.
var errNotHere = errors.New("secrets: not possible in this state")

// Manager keeps passwords in the keyring while there is one, or, without
// one, in the unencrypted file when the user has chosen that. It detects
// the keyring once; when the keyring turns out to be gone, it saves
// nothing until Murmel is started again (not in the file either).
//
// With a keyring and a file left from before, passwords are read from the
// keyring first and from the file second; whatever is saved goes to the
// keyring and leaves the file.
type Manager struct {
	keyring  Store
	file     *File // nil: no file
	onChange func()
	detected chan struct{}

	mu    sync.Mutex
	state State // as detected; NoKeyring once the keyring is gone
	lost  bool  // the keyring went away while Murmel ran
}

// NewManager starts detect on a goroutine and returns at once; calls wait
// for the result. onChange runs (on some goroutine) whenever State or the
// file changes. file may be nil.
func NewManager(keyring Store, file *File, detect func() State, onChange func()) *Manager {
	m := &Manager{keyring: keyring, file: file, onChange: onChange, detected: make(chan struct{})}
	go func() {
		s := detect()
		m.mu.Lock()
		m.state = s
		m.mu.Unlock()
		slog.Info("passwords", "storage", m.State())
		m.onChange()
		close(m.detected)
	}()
	return m
}

func (m *Manager) fileExists() bool { return m.file != nil && m.file.Exists() }

// State implements Passwords.
func (m *Manager) State() State {
	m.mu.Lock()
	s, lost := m.state, m.lost
	m.mu.Unlock()
	if s == NoKeyring && !lost && m.fileExists() {
		return InFile
	}
	return s
}

// File implements Passwords.
func (m *Manager) File() FileInfo {
	if m.file == nil {
		return FileInfo{}
	}
	m.mu.Lock()
	offered := m.state == NoKeyring && !m.lost
	m.mu.Unlock()
	info := FileInfo{Path: m.file.Path(), Exists: m.file.Exists(), Damaged: m.file.Damaged(), Offered: offered}
	if info.Exists {
		if keys, err := m.file.Entries(); err == nil {
			info.Count = len(keys)
		}
	}
	return info
}

// keyringCall runs call on the keyring and notes a keyring that is gone.
func (m *Manager) keyringCall(call func() error) error {
	err := call()
	if KindOf(err) == Gone {
		m.mu.Lock()
		changed := m.state != NoKeyring || !m.lost
		m.state, m.lost = NoKeyring, true
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
	<-m.detected
	switch m.State() {
	case InFile:
		return m.file.Save(k, password)
	case InKeyring:
		if err := m.keyringCall(func() error { return m.keyring.Save(k, password) }); err != nil {
			return err
		}
		m.leaveFile(k)
		return nil
	}
	return ErrNoKeyring
}

// leaveFile removes k from a file left from before, now that the keyring
// has it.
func (m *Manager) leaveFile(k Key) {
	if !m.fileExists() {
		return
	}
	if err := m.file.Delete(k); err != nil {
		slog.Warn("passwords: not removed from the password file", "err", err)
	}
	m.onChange()
}

// Lookup implements Store.
func (m *Manager) Lookup(k Key) (string, error) {
	<-m.detected
	switch m.State() {
	case InFile:
		return m.file.Lookup(k)
	case InKeyring:
		var pw string
		err := m.keyringCall(func() error {
			var err error
			pw, err = m.keyring.Lookup(k)
			return err
		})
		if err != nil || pw != "" || !m.fileExists() {
			return pw, err
		}
		pw, err = m.file.Lookup(k)
		if err != nil {
			slog.Warn("passwords: the password file is not readable", "err", err)
			return "", nil
		}
		return pw, nil
	}
	return "", ErrNoKeyring
}

// Delete implements Store.
func (m *Manager) Delete(k Key) error {
	<-m.detected
	switch m.State() {
	case InFile:
		return m.file.Delete(k)
	case InKeyring:
		err := m.keyringCall(func() error { return m.keyring.Delete(k) })
		if m.fileExists() {
			if ferr := m.file.Delete(k); ferr != nil && err == nil {
				err = ferr
			}
			m.onChange()
		}
		return err
	}
	return ErrNoKeyring
}

// UseFile implements Passwords.
func (m *Manager) UseFile() error {
	<-m.detected
	if !m.File().Offered {
		return errNotHere
	}
	if err := m.file.Create(); err != nil {
		return err
	}
	m.onChange()
	return nil
}

// StopUsingFile implements Passwords. It also deletes a file left from
// before next to a keyring.
func (m *Manager) StopUsingFile() error {
	<-m.detected
	if m.file == nil {
		return errNotHere
	}
	err := m.file.Remove()
	m.onChange()
	return err
}

// MoveToKeyring implements Passwords: each password is saved, read back
// and compared; only when all of them are in the keyring is the file
// deleted, with the damaged copies set aside next to it.
func (m *Manager) MoveToKeyring() error {
	<-m.detected
	if m.State() != InKeyring || !m.fileExists() {
		return errNotHere
	}
	keys, err := m.file.Entries()
	if err != nil {
		return err
	}
	for _, k := range keys {
		pw, err := m.file.Lookup(k)
		if err != nil {
			return err
		}
		if err := m.keyringCall(func() error { return m.keyring.Save(k, pw) }); err != nil {
			return err
		}
		var back string
		if err := m.keyringCall(func() error {
			var err error
			back, err = m.keyring.Lookup(k)
			return err
		}); err != nil {
			return err
		}
		if back != pw {
			return &Error{Kind: Other, msg: "the keyring returned a different password"}
		}
	}
	err = m.file.Remove()
	m.onChange()
	return err
}
