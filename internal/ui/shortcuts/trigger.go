package shortcuts

import (
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Accelerators returns the keys of b in GTK accelerator syntax
// ("<Alt>Page_Up"). The portal reports them only inside a sentence for
// people (GNOME: "Press <Alt>Page_Up or <Control>k", translated), so the
// words that parse as an accelerator with a modifier or as a key name
// with more than one letter are taken.
func (b Binding) Accelerators() []string {
	var out []string
	for _, word := range strings.Fields(b.Trigger) {
		word = strings.Trim(word, ".,;:\"'«»„“”()")
		key, mods, ok := gtk.AcceleratorParse(word)
		if !ok || key == 0 {
			continue
		}
		// A bare one-letter word ("a", "o") is more likely prose than a
		// key; global shortcuts come with a modifier anyway.
		if mods == 0 && len([]rune(word)) == 1 {
			continue
		}
		out = append(out, gtk.AcceleratorName(key, mods))
	}
	return out
}

// Label is the keys of b as the desktop shows them ("Alt+Page Up"),
// several joined by " / "; the portal's text when no key can be read
// from it, "" without a binding.
func (b Binding) Label() string {
	accels := b.Accelerators()
	if len(accels) == 0 {
		return b.Trigger
	}
	labels := make([]string, len(accels))
	for i, a := range accels {
		key, mods, _ := gtk.AcceleratorParse(a)
		labels[i] = gtk.AcceleratorGetLabel(key, mods)
	}
	return strings.Join(labels, " / ")
}
