// Package identities holds the dialogs for client certificates
// ("identities"): create, import and export. The connect dialog, the
// preferences and the controller's missing-certificate prompt share them.
package identities

import "github.com/leonelquinteros/gotext"

// tr marks a user-facing string as translatable (same global gotext locale
// as package ui).
func tr(s string) string { return gotext.Get(s) }
