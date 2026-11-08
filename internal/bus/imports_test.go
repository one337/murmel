package bus

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGtkLightImports enforces the plan's gtk-light rule: package bus may
// import exactly glib/v2 from the gotk4 module tree — never gtk, adw, or gio.
func TestGtkLightImports(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	const allowed = "github.com/diamondburned/gotk4/pkg/glib/v2"
	sawAllowed := false
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(path, "github.com/diamondburned/gotk4") {
				continue
			}
			if path != allowed {
				t.Errorf("%s imports %q; the only permitted gotk4 import is %q", name, path, allowed)
			} else {
				sawAllowed = true
			}
		}
	}
	if !sawAllowed {
		t.Errorf("package no longer imports %q; if dispatch moved off glib.IdleAdd, update this test and the plan", allowed)
	}
}
