package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// fileVersion is the format version of the password file. A file with a
// higher version comes from a newer Murmel: it is read, never written.
const fileVersion = 1

// FileError is a failure of the password file.
type FileError struct {
	msg string
	err error
}

func (e *FileError) Error() string {
	if e.err == nil {
		return "password file: " + e.msg
	}
	return "password file: " + e.msg + ": " + e.err.Error()
}

func (e *FileError) Unwrap() error { return e.err }

// File keeps passwords unencrypted in a JSON file that only the user can
// read (mode 0600, in a directory with mode 0700). Without a keyring there
// is nowhere to keep a key that other programs of the same user could not
// read just as well, so the file is plain text. The user turns it on by
// creating it (Create) and off by removing it (Remove).
type File struct {
	path string
	mu   sync.Mutex
}

// NewFile returns the password file at path; it is not touched yet.
func NewFile(path string) *File { return &File{path: path} }

// Path is where the file is.
func (f *File) Path() string { return f.path }

type fileEntry struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type fileData struct {
	Version   int         `json:"version"`
	Passwords []fileEntry `json:"passwords"`
}

func (e fileEntry) key() Key { return Key{Host: e.Host, Port: e.Port, Username: e.Username} }

// Exists says whether the file is there, i.e. whether the user keeps
// passwords in it.
func (f *File) Exists() bool {
	_, err := os.Lstat(f.path)
	return err == nil
}

// Damaged lists the copies of unreadable files that were set aside.
func (f *File) Damaged() []string {
	m, _ := filepath.Glob(globEscape(f.path) + ".corrupt-*")
	sort.Strings(m)
	return m
}

// globEscape quotes the glob characters in a path.
func globEscape(p string) string {
	out := make([]rune, 0, len(p))
	for _, r := range p {
		switch r {
		case '*', '?', '[', '\\':
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}

// load reads the file. A damaged file is moved aside and the file starts
// empty again. readOnly is true for a file from a newer version. Caller
// holds f.mu.
func (f *File) load() (d fileData, readOnly bool, err error) {
	st, err := os.Lstat(f.path)
	if err != nil {
		return d, false, &FileError{msg: "not readable", err: err}
	}
	if !st.Mode().IsRegular() {
		return d, false, &FileError{msg: "not a regular file, not used"}
	}
	file, err := os.OpenFile(f.path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return d, false, &FileError{msg: "not readable", err: err}
	}
	defer func() { _ = file.Close() }()
	if st.Mode().Perm()&0o077 != 0 {
		if err := file.Chmod(0o600); err != nil {
			return d, false, &FileError{msg: "readable by others and cannot be restricted", err: err}
		}
		slog.Warn("passwords: the password file was readable by others; it is now readable by you only", "file", f.path)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return d, false, &FileError{msg: "not readable", err: err}
	}
	if err := json.Unmarshal(data, &d); err != nil || d.Version < 1 {
		if err == nil {
			err = fmt.Errorf("version %d", d.Version)
		}
		return f.setAside(err)
	}
	if d.Version > fileVersion {
		slog.Warn("passwords: the password file is from a newer version, it is not changed", "file", f.path, "version", d.Version)
		return d, true, nil
	}
	return d, false, nil
}

// setAside renames a damaged file to <name>.corrupt-<time> and starts a
// new, empty one. Caller holds f.mu.
func (f *File) setAside(cause error) (fileData, bool, error) {
	stamp := time.Now().Format("20060102-150405")
	aside := f.path + ".corrupt-" + stamp
	for i := 2; ; i++ {
		if _, err := os.Lstat(aside); err != nil {
			break
		}
		aside = fmt.Sprintf("%s.corrupt-%s-%d", f.path, stamp, i)
	}
	if err := os.Rename(f.path, aside); err != nil {
		return fileData{}, false, &FileError{msg: "damaged and cannot be moved aside", err: err}
	}
	slog.Error("passwords: the password file is damaged, moved aside; starting empty", "err", cause, "moved-to", aside)
	empty := fileData{Version: fileVersion}
	if err := f.write(empty); err != nil {
		return empty, false, err
	}
	return empty, false, nil
}

// write replaces the file atomically: a temporary file in the same
// directory, synced, renamed over it, then the directory synced. Caller
// holds f.mu.
func (f *File) write(d fileData) error {
	d.Version = fileVersion
	if d.Passwords == nil {
		d.Passwords = []fileEntry{}
	}
	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return &FileError{msg: "not written", err: err}
	}
	if err := writeAtomic(f.path, append(out, '\n')); err != nil {
		return &FileError{msg: "not written", err: err}
	}
	return nil
}

// writeAtomic is a variable so tests can make writes fail.
var writeAtomic = func(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*") // mode 0600
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Clean(dir)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Create starts keeping passwords in the file: an empty file, unless
// there is one already.
func (f *File) Create() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := os.Lstat(f.path); err == nil {
		return nil
	}
	return f.write(fileData{})
}

// Remove deletes the file, the damaged copies set aside and any leftover
// temporary file. Deleting does not overwrite the data on the disk first.
func (f *File) Remove() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	tmps, _ := filepath.Glob(filepath.Join(globEscape(filepath.Dir(f.path)), "."+globEscape(filepath.Base(f.path))+".tmp-*"))
	var first error
	for _, p := range append(append([]string{f.path}, f.Damaged()...), tmps...) {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) && first == nil {
			first = &FileError{msg: "not removed", err: err}
		}
	}
	return first
}

// Entries returns the keys in the file.
func (f *File) Entries() ([]Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, _, err := f.load()
	if err != nil {
		return nil, err
	}
	keys := make([]Key, 0, len(d.Passwords))
	for _, e := range d.Passwords {
		keys = append(keys, e.key())
	}
	return keys, nil
}

// Save implements Store.
func (f *File) Save(k Key, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, readOnly, err := f.load()
	if err != nil {
		return err
	}
	if readOnly {
		return &FileError{msg: "from a newer version of Murmel, not changed"}
	}
	for i, e := range d.Passwords {
		if e.key() == k {
			d.Passwords[i].Password = password
			return f.write(d)
		}
	}
	d.Passwords = append(d.Passwords, fileEntry{Host: k.Host, Port: k.Port, Username: k.Username, Password: password})
	return f.write(d)
}

// Lookup implements Store.
func (f *File) Lookup(k Key) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, _, err := f.load()
	if err != nil {
		return "", err
	}
	for _, e := range d.Passwords {
		if e.key() == k {
			return e.Password, nil
		}
	}
	return "", nil
}

// Delete implements Store.
func (f *File) Delete(k Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, readOnly, err := f.load()
	if err != nil {
		return err
	}
	kept := d.Passwords[:0]
	for _, e := range d.Passwords {
		if e.key() != k {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(d.Passwords) {
		return nil
	}
	if readOnly {
		return &FileError{msg: "from a newer version of Murmel, not changed"}
	}
	d.Passwords = kept
	return f.write(d)
}
