package admin

import "github.com/leonelquinteros/gotext"

func tr(s string) string { return gotext.Get(s) }

// trn translates a message with a number in it: singular for n == 1 in
// English, the target language's plural rules otherwise.
func trn(singular, plural string, n int) string { return gotext.GetN(singular, plural, n) }
