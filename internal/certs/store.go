package certs

import (
	"fmt"
	"os"
	"path/filepath"
)

// defaultDataDir resolves $XDG_DATA_HOME/murmel, falling back to
// ~/.local/share/murmel per the XDG base directory spec.
func defaultDataDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "murmel"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("certs: resolving data dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "murmel"), nil
}

func mkdirAll(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("certs: creating %s: %w", dir, err)
	}
	return nil
}

// writeFileAtomic writes via a temp file + rename so readers never observe a
// partially written cert, key, or store file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("certs: writing %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("certs: writing %s: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("certs: writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("certs: writing %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("certs: writing %s: %w", path, err)
	}
	return nil
}
