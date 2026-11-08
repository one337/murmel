package prefs

import "github.com/leonelquinteros/gotext"

// tr marks a user-facing string as translatable and returns its translation.
// Uses the same global gotext locale configured by internal/ui's init().
func tr(s string) string { return gotext.Get(s) }
