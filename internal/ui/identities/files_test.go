package identities

import (
	"os"
	"path/filepath"
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
