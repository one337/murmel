package ui

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// The About dialog names every component of tools/licenses.txt once, and
// nothing that is not listed there; the icons are the only entry without a
// line, as they are not a Go module.
func TestAboutCreditsEveryLicensedComponent(t *testing.T) {
	f, err := os.Open("../../tools/licenses.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	listed := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) >= 3 && !strings.HasPrefix(fields[0], "#") {
			listed[fields[0]] = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	credited := map[string]string{}
	for _, s := range legalSections() {
		if len(s.components) == 0 && s.name != "GNOME Icon Development Kit" {
			t.Errorf("About entry %q names no line of tools/licenses.txt", s.name)
		}
		for _, c := range s.components {
			if !listed[c] {
				t.Errorf("About entry %q names %q, which tools/licenses.txt does not list", s.name, c)
			}
			if other, ok := credited[c]; ok {
				t.Errorf("%q is credited twice, in %q and %q", c, other, s.name)
			}
			credited[c] = s.name
		}
	}
	for c := range listed {
		if _, ok := credited[c]; !ok {
			t.Errorf("tools/licenses.txt lists %q, but the About dialog does not credit it", c)
		}
	}
}
