package ui

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	"io.github/murmel/internal/settings"
)

// applyColorScheme sets Libadwaita's style from the color-scheme setting
// and follows later changes. Called once the application has started.
func applyColorScheme(st settings.Settings) {
	set := func() { adw.StyleManagerGetDefault().SetColorScheme(colorScheme(st.ColorScheme())) }
	set()
	st.ConnectChanged(func(key string) {
		if key == "color-scheme" {
			set()
		}
	})
}

// colorScheme maps the setting onto Libadwaita: "system" prefers light but
// follows the desktop's dark style.
func colorScheme(v string) adw.ColorScheme {
	switch v {
	case "light":
		return adw.ColorSchemeForceLight
	case "dark":
		return adw.ColorSchemeForceDark
	}
	return adw.ColorSchemeDefault
}
