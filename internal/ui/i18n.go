package ui

import (
	"os"
	"strings"

	"github.com/leonelquinteros/gotext"
)

// i18nDomain is the gettext domain: matches the basename of the .po/.mo file.
const i18nDomain = "murmel"

func init() {
	// Probe standard locale directories in priority order:
	//   1. /app/share/locale — Flatpak runtime
	//   2. /usr/share/locale — system-wide install
	for _, dir := range []string{"/app/share/locale", "/usr/share/locale"} {
		if _, err := os.Stat(dir); err == nil {
			lang := os.Getenv("LANGUAGE")
			if lang == "" {
				lang = os.Getenv("LANG")
			}
			lang = strings.Split(lang, ".")[0] // strip encoding suffix
			gotext.Configure(dir, lang, i18nDomain)
			return
		}
	}
	// No locale dir found — translations unavailable, strings pass through.
}

// tr marks a user-facing string as translatable and returns its translation.
//
// Extract all marked strings with:
//
//	xgettext --language=Go --keyword=tr --sort-output -o po/murmel.pot \
//	    $(find internal/ui -name '*.go')
//
// Technical strings stay outside tr by design: icon names, CSS classes,
// action names, stack child names, log messages, and URL fragments.
func tr(s string) string { return gotext.Get(s) }
