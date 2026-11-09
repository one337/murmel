package bus

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoGotk4Imports: package bus imports nothing from gotk4. The audio
// layer depends on the bus, and TestLayerBoundary keeps that layer GTK-free;
// the UI hands the bus its main loop with SetMainLoop instead.
func TestNoGotk4Imports(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
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
			if path := strings.Trim(imp.Path.Value, `"`); strings.HasPrefix(path, "github.com/diamondburned/gotk4") {
				t.Errorf("%s imports %q; package bus must not import gotk4", name, path)
			}
		}
	}
}
