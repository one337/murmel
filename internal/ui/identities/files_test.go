package identities

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWritePrivateFileRestrictsExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.pem")
	if err := os.WriteFile(path, []byte("old content, longer than the new one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writePrivateFile(path, []byte("key")); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
	if got, _ := os.ReadFile(path); string(got) != "key" {
		t.Fatalf("content = %q, want %q", got, "key")
	}
}

// A file system without Unix permissions (FAT, exFAT) refuses the mode;
// the backup is written all the same.
func TestWritePrivateFileWithoutUnixPermissions(t *testing.T) {
	chmod = func(*os.File, os.FileMode) error { return syscall.EPERM }
	t.Cleanup(func() { chmod = (*os.File).Chmod })

	path := filepath.Join(t.TempDir(), "identity.pem")
	if err := writePrivateFile(path, []byte("key")); err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "key" {
		t.Fatalf("content = %q, want %q", got, "key")
	}
}

// The hint shows exactly when Murmel could not make sure the backup is
// private.
func TestExposedBackup(t *testing.T) {
	for _, tc := range []struct {
		local bool
		mode  os.FileMode
		want  bool
	}{
		{true, 0o600, false}, // local, private
		{true, 0o400, false}, // local, read-only for the user
		{true, 0o644, true},  // FAT/exFAT: the mode did not stick
		{true, 0o660, true},  // group may read
		{true, 0o755, true},  // others may read
		{false, 0, true},     // no local path: up to the location
		{false, 0o600, true}, // whatever a remote reports
	} {
		if got := exposed(tc.local, tc.mode); got != tc.want {
			t.Errorf("exposed(%v, %o) = %v, want %v", tc.local, tc.mode, got, tc.want)
		}
	}
}
