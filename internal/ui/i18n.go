package ui

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/leonelquinteros/gotext"
)

// i18nDomain is the gettext domain: matches the basename of the .po/.mo file.
const i18nDomain = "murmel"

// localeDirs are probed in priority order:
//  1. $MURMEL_LOCALEDIR — development (tools/install-translations.sh <dir>)
//  2. <prefix>/share/locale next to the program's <prefix>/bin — any
//     install prefix: Flatpak (/app), packages (/usr), install.sh
//     (/usr/local, ~/.local), Nix store paths, the snap ($SNAP/usr)
//  3. /app/share/locale and /usr/share/locale — fallback for a program
//     in an unusual place
func localeDirs() []string {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	return localeDirsFor(os.Getenv("MURMEL_LOCALEDIR"), exe)
}

// localeDirsFor is localeDirs for a given $MURMEL_LOCALEDIR and program
// path. Symlinks in the program path are resolved; if that fails, the
// path is used as it is.
func localeDirsFor(envDir, exe string) []string {
	var dirs []string
	add := func(d string) {
		if slices.Contains(dirs, d) {
			return
		}
		dirs = append(dirs, d)
	}
	if envDir != "" {
		add(envDir)
	}
	if exe != "" {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		add(filepath.Join(filepath.Dir(exe), "..", "share", "locale"))
	}
	add("/app/share/locale")
	add("/usr/share/locale")
	return dirs
}

// init follows the system language; ApplyLanguage may override it once
// the settings are loaded.
func init() {
	for _, dir := range localeDirs() {
		if lang := pickLanguage(os.Getenv, func(l string) bool { return hasTranslation(dir, l) }); lang != "" {
			gotext.Configure(dir, lang, i18nDomain)
			return
		}
	}
	// No translation for the user's languages: strings stay English.
}

// ApplyLanguage switches to the language the user chose in Preferences
// (a code such as "de"; "en" = English; "" keeps the system language).
// Call it before building any UI: texts are translated when created.
//
// It also sets LANGUAGE, which the C gettext of GTK and Libadwaita reads:
// their own texts (the About dialog, file chooser, emoji chooser) would
// otherwise stay in the system language.
func ApplyLanguage(code string) {
	if code != "" {
		_ = os.Setenv("LANGUAGE", code)
	}
	switch code {
	case "":
		return
	case "en":
		gotext.Configure("", "en", i18nDomain) // no catalog: source texts
		return
	}
	for _, dir := range localeDirs() {
		if hasTranslation(dir, code) {
			gotext.Configure(dir, code, i18nDomain)
			return
		}
	}
}

// Languages lists the codes Murmel has translations for, sorted, without
// English (the source language).
func Languages() []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range localeDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if code := e.Name(); e.IsDir() && !seen[code] && hasTranslation(dir, code) {
				seen[code] = true
				out = append(out, code)
			}
		}
	}
	sort.Strings(out)
	return out
}

// LanguageName is a language's name in that language ("Deutsch"), as
// language menus show them; unknown codes are shown as they are.
func LanguageName(code string) string {
	if n, ok := languageNames[code]; ok {
		return n
	}
	return code
}

var languageNames = map[string]string{
	"en": "English", "de": "Deutsch", "fr": "Français", "es": "Español",
	"it": "Italiano", "nl": "Nederlands", "pl": "Polski", "pt": "Português",
	"pt_BR": "Português (Brasil)", "ru": "Русский", "uk": "Українська",
	"cs": "Čeština", "sv": "Svenska", "da": "Dansk", "nb": "Norsk bokmål",
	"fi": "Suomi", "tr": "Türkçe", "ja": "日本語", "zh_CN": "简体中文",
	"zh_TW": "繁體中文", "ko": "한국어",
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
	for l := range strings.SplitSeq(getenv("LANGUAGE"), ":") {
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
