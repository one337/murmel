package settings

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type schemaXML struct {
	Schemas []struct {
		ID   string `xml:"id,attr"`
		Keys []struct {
			Name    string `xml:"name,attr"`
			Type    string `xml:"type,attr"`
			Default string `xml:"default"`
		} `xml:"key"`
	} `xml:"schema"`
}

func schemaFilePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))), "data", "io.github.murmel.Murmel.gschema.xml")
}

func parseSchema(t *testing.T) map[string]struct {
	Type    string
	Default string
} {
	t.Helper()
	raw, err := os.ReadFile(schemaFilePath(t))
	if err != nil {
		t.Fatalf("read gschema: %v", err)
	}
	var file schemaXML
	if err := xml.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse gschema: %v", err)
	}
	keys := map[string]struct {
		Type    string
		Default string
	}{}
	for _, s := range file.Schemas {
		if s.ID != SchemaID {
			continue
		}
		for _, k := range s.Keys {
			keys[k.Name] = struct {
				Type    string
				Default string
			}{k.Type, k.Default}
		}
	}
	if len(keys) == 0 {
		t.Fatalf("no keys found for schema id %s in %s", SchemaID, schemaFilePath(t))
	}
	return keys
}

// parseDefault converts the GVariant literal used as a schema <default>
// into the equivalent Go value. Only the literal forms used by this schema
// are supported; anything else fails loudly so new keys cannot silently
// drift between schema and Memory impl.
func parseDefault(gtype, lit string) (any, error) {
	switch gtype {
	case "b":
		switch lit {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("unsupported boolean default %q", lit)
	case "i":
		return strconv.Atoi(lit)
	case "d":
		return strconv.ParseFloat(lit, 64)
	case "s":
		if len(lit) < 2 || !strings.HasPrefix(lit, "'") || !strings.HasSuffix(lit, "'") {
			return nil, fmt.Errorf("unsupported string default %q", lit)
		}
		return strings.ReplaceAll(strings.Trim(lit, "'"), "''", "'"), nil
	case "as":
		if lit == "[]" {
			return []ServerEntry{}, nil
		}
		return nil, fmt.Errorf("unsupported string-array default %q (only [] is supported)", lit)
	case "a{sb}":
		if lit == "{}" {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("unsupported dictionary default %q (only {} is supported)", lit)
	case "a{ss}":
		if lit == "{}" {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("unsupported dictionary default %q (only {} is supported)", lit)
	}
	return nil, fmt.Errorf("unsupported gvariant type %q", gtype)
}

// TestKeyCoverage asserts that every key in the GSchema file has a typed
// accessor spec (and vice versa) and that the Memory defaults equal the
// schema defaults. It guards the package against drift between the schema,
// the accessor layer and the T25 preference widgets.
func TestKeyCoverage(t *testing.T) {
	schemaKeys := parseSchema(t)

	for key := range schemaKeys {
		if _, ok := keySpecs[key]; !ok {
			t.Errorf("schema key %q has no typed accessor (add one to keySpecs + Settings interface)", key)
		}
	}
	for key := range keySpecs {
		if _, ok := schemaKeys[key]; !ok {
			t.Errorf("accessor spec %q has no schema key in %s", key, schemaFilePath(t))
		}
	}

	m := NewMemory()
	for key, spec := range keySpecs {
		schemaKey := schemaKeys[key]

		if spec.gtype != schemaKey.Type {
			t.Errorf("%s: spec gtype %q != schema type %q", key, spec.gtype, schemaKey.Type)
		}

		def, err := parseDefault(schemaKey.Type, schemaKey.Default)
		if err != nil {
			t.Errorf("%s: %v", key, err)
			continue
		}
		if !valuesEqual(def, spec.def) {
			t.Errorf("%s: spec default %v != schema default %v", key, spec.def, def)
		}
		if !valuesEqual(m.get(key), spec.def) {
			t.Errorf("%s: Memory default %v != spec default %v", key, m.get(key), spec.def)
		}
		if valuesEqual(spec.sample, spec.def) {
			t.Errorf("%s: sample value %v equals default; roundtrip tests would be vacuous", key, spec.sample)
		}

		wantKind := map[string]reflect.Kind{
			"b": reflect.Bool, "i": reflect.Int, "d": reflect.Float64,
			"s": reflect.String, "as": reflect.Slice, "a{ss}": reflect.Map, "a{sb}": reflect.Map,
		}[schemaKey.Type]
		if gotKind := reflect.TypeOf(spec.get(m)).Kind(); gotKind != wantKind {
			t.Errorf("%s: accessor returns kind %s, want %s", key, gotKind, wantKind)
		}
	}
}

func TestSchemaCompiles(t *testing.T) {
	bin, err := exec.LookPath("glib-compile-schemas")
	if err != nil {
		t.Skip("glib-compile-schemas not available")
	}
	target := t.TempDir()
	cmd := exec.Command(bin, "--strict", "--targetdir="+target, filepath.Dir(schemaFilePath(t)))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("glib-compile-schemas --strict failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(target, "gschemas.compiled")); err != nil {
		t.Fatalf("gschemas.compiled not produced: %v", err)
	}
}
