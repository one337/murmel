package avatarpick

import "github.com/leonelquinteros/gotext"

// tr marks a user-facing string as translatable (same global gotext locale
// as package ui).
func tr(s string) string { return gotext.Get(s) }
