package ui

import (
	"os"
	"path/filepath"
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
		if lang := pickLanguage(os.Getenv, func(l string) bool { return hasTranslation(dir, l) }); lang != "" {
			gotext.Configure(dir, lang, i18nDomain)
			return
		}
	}
	// No translation for the user's languages: strings stay English.
}

// pickLanguage chooses the first of the user's languages that has a
// translation, following gettext: LANGUAGE (a colon-separated list, used
// unless the locale is C/POSIX), then LC_ALL, LC_MESSAGES and LANG. Encoding
// and modifier are dropped ("de_AT.UTF-8@euro" → "de_AT"); a regional
// variant falls back to the language ("de_AT" → "de"). "" = English.
func pickLanguage(getenv func(string) string, has func(string) bool) string {
	locale := ""
	for _, v := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if locale = getenv(v); locale != "" {
			break
		}
	}
	base := normalizeLocale(locale)
	if base == "C" || base == "POSIX" {
		return ""
	}
	var candidates []string
	for _, l := range strings.Split(getenv("LANGUAGE"), ":") {
		if l = normalizeLocale(l); l != "" {
			candidates = append(candidates, l)
		}
	}
	if base != "" {
		candidates = append(candidates, base)
	}
	for _, l := range candidates {
		if strings.HasPrefix(l, "en") {
			return "" // English is the source language
		}
		if has(l) {
			return l
		}
		if i := strings.IndexByte(l, '_'); i > 0 && has(l[:i]) {
			return l[:i]
		}
	}
	return ""
}

// normalizeLocale strips encoding and modifier: "de_DE.UTF-8@euro" → "de_DE".
func normalizeLocale(l string) string {
	if i := strings.IndexAny(l, ".@"); i >= 0 {
		l = l[:i]
	}
	return strings.TrimSpace(l)
}

// hasTranslation reports whether dir holds Murmel's catalog for lang.
func hasTranslation(dir, lang string) bool {
	for _, ext := range []string{"mo", "po"} {
		if _, err := os.Stat(filepath.Join(dir, lang, "LC_MESSAGES", i18nDomain+"."+ext)); err == nil {
			return true
		}
	}
	return false
}

// tr marks a user-facing string as translatable and returns its translation.
//
// Extract all marked strings with tools/update-pot.sh.
//
// Technical strings stay outside tr by design: icon names, CSS classes,
// action names, stack child names, log messages, and URL fragments.
func tr(s string) string { return gotext.Get(s) }
