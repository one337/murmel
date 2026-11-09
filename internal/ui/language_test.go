package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/leonelquinteros/gotext"
)

func TestPickLanguage(t *testing.T) {
	have := map[string]bool{"de": true, "pt_BR": true}
	has := func(l string) bool { return have[l] }
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"LANG": "de_DE.UTF-8"}, "de"},
		{map[string]string{"LANG": "de_AT.UTF-8@euro"}, "de"},
		{map[string]string{"LANG": "en_US.UTF-8", "LC_MESSAGES": "de_DE.UTF-8"}, "de"},
		{map[string]string{"LANG": "de_DE.UTF-8", "LC_ALL": "C"}, ""},
		{map[string]string{"LANG": "fr_FR.UTF-8", "LANGUAGE": "fr:de"}, "de"},
		{map[string]string{"LANG": "de_DE.UTF-8", "LANGUAGE": "en:de"}, ""},
		{map[string]string{"LANG": "pt_BR.UTF-8"}, "pt_BR"},
		{map[string]string{"LANG": "fr_FR.UTF-8"}, ""},
		{map[string]string{}, ""},
	} {
		got := pickLanguage(func(k string) string { return tc.env[k] }, has)
		if got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.env, got, tc.want)
		}
	}
}

// TestCompiledCatalog: a catalog compiled with msgfmt (as the Flatpak
// build does) is found by the language choice and translates singular
// and plural messages.
func TestCompiledCatalog(t *testing.T) {
	if _, err := exec.LookPath("msgfmt"); err != nil {
		t.Skip("msgfmt not installed")
	}
	dir := t.TempDir()
	po := filepath.Join(dir, "de.po")
	if err := os.WriteFile(po, []byte(`msgid ""
msgstr ""
"Content-Type: text/plain; charset=UTF-8\n"
"Plural-Forms: nplurals=2; plural=(n != 1);\n"

msgid "Not connected"
msgstr "Nicht verbunden"

msgid "%d member"
msgid_plural "%d members"
msgstr[0] "%d Mitglied"
msgstr[1] "%d Mitglieder"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "de", "LC_MESSAGES"), 0o755); err != nil {
		t.Fatal(err)
	}
	mo := filepath.Join(dir, "de", "LC_MESSAGES", i18nDomain+".mo")
	if out, err := exec.Command("msgfmt", "--check", "-o", mo, po).CombinedOutput(); err != nil {
		t.Fatalf("msgfmt: %v\n%s", err, out)
	}
	env := map[string]string{"LANG": "de_AT.UTF-8"}
	lang := pickLanguage(func(k string) string { return env[k] }, func(l string) bool { return hasTranslation(dir, l) })
	if lang != "de" {
		t.Fatalf("pickLanguage = %q, want de", lang)
	}
	loc := gotext.NewLocale(dir, lang)
	loc.AddDomain(i18nDomain)
	if got := loc.GetD(i18nDomain, "Not connected"); got != "Nicht verbunden" {
		t.Errorf("singular: %q", got)
	}
	for n, want := range map[int]string{1: "1 Mitglied", 3: "3 Mitglieder"} {
		if got := fmt.Sprintf(loc.GetND(i18nDomain, "%d member", "%d members", n), n); got != want {
			t.Errorf("plural %d: %q, want %q", n, got, want)
		}
	}
}
