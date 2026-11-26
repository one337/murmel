package prefs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/one337/murmel/internal/audio/effects"
)

func TestCopySoundKeepsNamesApart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sounds")
	first, err := copySound(dir, "bell.ogg", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := copySound(dir, "bell.ogg", []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first) != "bell.ogg" || filepath.Base(second) != "bell-2.ogg" {
		t.Fatalf("copies = %s, %s", first, second)
	}
	if data, _ := os.ReadFile(first); string(data) != "one" {
		t.Fatalf("first copy = %q", data)
	}
	if fi, _ := os.Stat(second); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", fi.Mode().Perm())
	}
}

func TestRemoveUnusedCopy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sounds")
	copied, _ := copySound(dir, "bell.ogg", []byte("x"))
	choice := effects.FileChoice(copied)

	// Still used by another event: kept.
	removeUnusedCopy(dir, choice, map[string]string{"user-joined": choice})
	if _, err := os.Stat(copied); err != nil {
		t.Fatal("a copy still in use was removed")
	}
	// No longer used: removed.
	removeUnusedCopy(dir, choice, map[string]string{})
	if _, err := os.Stat(copied); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("an unused copy was kept")
	}
	// A file of the user's outside the copies folder: never touched.
	own := filepath.Join(t.TempDir(), "mine.ogg")
	if err := os.WriteFile(own, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	removeUnusedCopy(dir, effects.FileChoice(own), map[string]string{})
	if _, err := os.Stat(own); err != nil {
		t.Fatal("a file outside the copies folder was removed")
	}
}
