// Package i18n translates the user-facing texts of the UI packages. The
// language and the catalogs are set up by package ui at startup.
//
// tools/update-pot.sh extracts every string passed to Tr and TrN.
// Technical strings stay outside on purpose: icon names, CSS classes,
// action names, stack child names, log messages and URL fragments.
package i18n

import "github.com/leonelquinteros/gotext"

// Tr returns the translation of s.
func Tr(s string) string { return gotext.Get(s) }

// TrN translates a message with a number in it: singular for n == 1 in
// English, the target language's plural rules otherwise.
func TrN(singular, plural string, n int) string { return gotext.GetN(singular, plural, n) }
