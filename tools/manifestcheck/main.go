// Command manifestcheck reports whether two Flatpak manifests agree on the
// sandbox permissions and the runtime they build against: finish-args,
// runtime, runtime-version, sdk and sdk-extensions. tools/verify.sh runs it
// on the local manifest and the Flathub template.
//
//	go run ./tools/manifestcheck a.json b.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
)

var keys = []string{"finish-args", "runtime", "runtime-version", "sdk", "sdk-extensions"}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: manifestcheck a.json b.json")
		os.Exit(2)
	}
	a, err := load(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	b, err := load(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	differ := false
	for _, k := range keys {
		if !reflect.DeepEqual(a[k], b[k]) {
			differ = true
			fmt.Printf("%s differs:\n  %s: %s\n  %s: %s\n", k, os.Args[1], show(a[k]), os.Args[2], show(b[k]))
		}
	}
	if differ {
		os.Exit(1)
	}
}

func load(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

func show(v any) string {
	if v == nil {
		return "(missing)"
	}
	out, _ := json.Marshal(v)
	return string(out)
}
