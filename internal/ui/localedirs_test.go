package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLocaleDirsOrder(t *testing.T) {
	got := localeDirsFor("/dev/locale", "/opt/murmel/bin/murmel")
	want := []string{"/dev/locale", "/opt/murmel/share/locale", "/app/share/locale", "/usr/share/locale"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("localeDirsFor = %v, want %v", got, want)
	}
}

func TestLocaleDirsNoDuplicates(t *testing.T) {
	got := localeDirsFor("", "/usr/bin/murmel")
	want := []string{"/usr/share/locale", "/app/share/locale"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("localeDirsFor = %v, want %v", got, want)
	}
}

func TestLocaleDirsResolvesSymlink(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "prefix")
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(prefix, "bin", "murmel")
	if err := os.WriteFile(program, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "home", ".local", "bin")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "murmel")
	if err := os.Symlink(program, link); err != nil {
		t.Fatal(err)
	}
	realPrefix, _ := filepath.EvalSymlinks(prefix)

	got := localeDirsFor("", link)
	if want := filepath.Join(realPrefix, "share", "locale"); got[0] != want {
		t.Fatalf("first dir = %q, want %q (symlink resolved)", got[0], want)
	}
}

func TestLocaleDirsUnresolvablePathIsKept(t *testing.T) {
	// EvalSymlinks fails for a path that does not exist; the path is used
	// as it is instead of dropping the program-relative directory.
	got := localeDirsFor("", "/nonexistent/prefix/bin/murmel")
	if want := "/nonexistent/prefix/share/locale"; got[0] != want {
		t.Fatalf("first dir = %q, want %q", got[0], want)
	}
}
