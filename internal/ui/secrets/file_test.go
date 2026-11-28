package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestFile(t *testing.T) *File {
	t.Helper()
	return NewFile(filepath.Join(t.TempDir(), "murmel", "passwords.json"))
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

// Passwords are saved, replaced, found and removed; the file and its
// directory are the user's alone.
func TestFileRoundTrip(t *testing.T) {
	f := newTestFile(t)
	if f.Exists() {
		t.Fatal("exists before Create")
	}
	if err := f.Create(); err != nil {
		t.Fatal(err)
	}
	if mode(t, f.Path()) != 0o600 || mode(t, filepath.Dir(f.Path())) != 0o700 {
		t.Errorf("modes %v %v", mode(t, f.Path()), mode(t, filepath.Dir(f.Path())))
	}
	other := Key{Host: "two.invalid", Port: 2, Username: "b"}
	for _, step := range []struct {
		k  Key
		pw string
	}{{k, "one"}, {other, "two"}, {k, "three"}} {
		if err := f.Save(step.k, step.pw); err != nil {
			t.Fatal(err)
		}
	}
	if pw, err := f.Lookup(k); pw != "three" || err != nil {
		t.Errorf("Lookup: %q, %v", pw, err)
	}
	if keys, err := f.Entries(); len(keys) != 2 || err != nil {
		t.Errorf("Entries: %v, %v", keys, err)
	}
	if err := f.Delete(k); err != nil {
		t.Fatal(err)
	}
	if pw, err := f.Lookup(k); pw != "" || err != nil {
		t.Errorf("Lookup after Delete: %q, %v", pw, err)
	}
	if pw, _ := f.Lookup(other); pw != "two" {
		t.Errorf("other entry: %q", pw)
	}
	if err := f.Delete(k); err != nil {
		t.Errorf("Delete of a missing entry: %v", err)
	}
	if mode(t, f.Path()) != 0o600 {
		t.Errorf("mode after writing %v", mode(t, f.Path()))
	}
	if err := f.Create(); err != nil {
		t.Fatal(err)
	}
	if pw, _ := f.Lookup(other); pw != "two" {
		t.Error("Create emptied an existing file")
	}
}

// A symlink in the file's place is not followed.
func TestFileSymlinkNotUsed(t *testing.T) {
	f := newTestFile(t)
	if err := os.MkdirAll(filepath.Dir(f.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte(`{"version":1,"passwords":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, f.Path()); err != nil {
		t.Fatal(err)
	}
	var fe *FileError
	if err := f.Save(k, "pw"); !errors.As(err, &fe) {
		t.Errorf("Save through a symlink: %v", err)
	}
	if _, err := f.Lookup(k); !errors.As(err, &fe) {
		t.Errorf("Lookup through a symlink: %v", err)
	}
	if data, _ := os.ReadFile(target); strings.Contains(string(data), "pw") {
		t.Error("the symlink's target was written")
	}
}

// A file others could read is restricted to the user before it is used.
func TestFileWideModeRestricted(t *testing.T) {
	f := newTestFile(t)
	if err := f.Create(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.Path(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Lookup(k); err != nil {
		t.Fatal(err)
	}
	if mode(t, f.Path()) != 0o600 {
		t.Errorf("mode %v", mode(t, f.Path()))
	}
}

// A damaged file is kept aside, unchanged, and the file starts empty.
func TestFileDamagedSetAside(t *testing.T) {
	for _, content := range []string{"{not json", `{"version":0}`, `[]`} {
		f := newTestFile(t)
		if err := f.Create(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.Path(), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if pw, err := f.Lookup(k); pw != "" || err != nil {
			t.Errorf("%q: Lookup %q, %v", content, pw, err)
		}
		damaged := f.Damaged()
		if len(damaged) != 1 {
			t.Fatalf("%q: damaged copies %v", content, damaged)
		}
		if data, _ := os.ReadFile(damaged[0]); string(data) != content {
			t.Errorf("%q: copy holds %q", content, data)
		}
		if mode(t, damaged[0]) != 0o600 {
			t.Errorf("%q: copy mode %v", content, mode(t, damaged[0]))
		}
		if err := f.Save(k, "pw"); err != nil {
			t.Errorf("%q: Save after setting aside: %v", content, err)
		}
	}
}

// A file from a newer version is read but never changed.
func TestFileNewerVersionReadOnly(t *testing.T) {
	f := newTestFile(t)
	if err := f.Create(); err != nil {
		t.Fatal(err)
	}
	newer := `{"version":2,"passwords":[{"host":"example.invalid","port":1,"username":"a","password":"pw"}],"more":true}`
	if err := os.WriteFile(f.Path(), []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	if pw, err := f.Lookup(k); pw != "pw" || err != nil {
		t.Errorf("Lookup: %q, %v", pw, err)
	}
	var fe *FileError
	if err := f.Save(k, "new"); !errors.As(err, &fe) {
		t.Errorf("Save: %v", err)
	}
	if err := f.Delete(k); !errors.As(err, &fe) {
		t.Errorf("Delete: %v", err)
	}
	if data, _ := os.ReadFile(f.Path()); string(data) != newer {
		t.Errorf("file changed: %s", data)
	}
}

// A failed write leaves the file as it was and no temporary file behind.
func TestFileFailedWriteKeepsFile(t *testing.T) {
	f := newTestFile(t)
	if err := f.Create(); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(k, "old"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(f.Path())
	real := writeAtomic
	t.Cleanup(func() { writeAtomic = real })
	writeAtomic = func(string, []byte) error { return errors.New("disk full") }
	var fe *FileError
	if err := f.Save(k, "new"); !errors.As(err, &fe) {
		t.Errorf("Save: %v", err)
	}
	if after, _ := os.ReadFile(f.Path()); string(after) != string(before) {
		t.Errorf("file changed by a failed write")
	}
}

// The temporary file of an interrupted write is never left next to the
// file after a successful one, and Remove takes everything away.
func TestFileRemoveTakesEverything(t *testing.T) {
	f := newTestFile(t)
	if err := f.Create(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(f.Path())
	if err := os.WriteFile(f.Path(), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Lookup(k); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(k, "pw"); err != nil {
		t.Fatal(err)
	}
	if tmps, _ := filepath.Glob(filepath.Join(dir, ".passwords.json.tmp-*")); len(tmps) != 0 {
		t.Errorf("temporary files left: %v", tmps)
	}
	leftover := filepath.Join(dir, ".passwords.json.tmp-123")
	if err := os.WriteFile(leftover, []byte("pw"), 0o600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(dir, "servers.json")
	if err := os.WriteFile(unrelated, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "servers.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("left in the directory: %v", names)
	}
	if f.Exists() {
		t.Error("still exists")
	}
}
