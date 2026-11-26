package ui

import (
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	"github.com/one337/murmel/internal/i18n"
)

// showShortcuts presents the keyboard shortcuts overview (Main Menu →
// Keyboard Shortcuts, Ctrl+?): the app's own shortcuts, which are fixed,
// and the global ones with the keys the desktop assigned (changed in
// Preferences → General).
func (a *App) showShortcuts() {
	d := adw.NewShortcutsDialog()

	general := adw.NewShortcutsSection(i18n.Tr("General"))
	general.Add(adw.NewShortcutsItem(i18n.Tr("Show keyboard shortcuts"), "<Control>question"))
	general.Add(adw.NewShortcutsItem(i18n.Tr("Open the main menu"), "F10"))
	general.Add(adw.NewShortcutsItem(i18n.Tr("Connect to a server"), "<Control>n"))
	general.Add(adw.NewShortcutsItem(i18n.Tr("Preferences"), "<Control>comma"))
	general.Add(adw.NewShortcutsItem(i18n.Tr("Fullscreen"), "F11"))
	general.Add(adw.NewShortcutsItem(i18n.Tr("Quit"), "<Control>q"))
	d.Add(general)

	messages := adw.NewShortcutsSection(i18n.Tr("Messages"))
	messages.Add(adw.NewShortcutsItem(i18n.Tr("Send the message"), "Return"))
	messages.Add(adw.NewShortcutsItem(i18n.Tr("New line"), "<Shift>Return"))
	messages.Add(adw.NewShortcutsItem(i18n.Tr("Bold"), "<Control>b"))
	messages.Add(adw.NewShortcutsItem(i18n.Tr("Italic"), "<Control>i"))
	messages.Add(adw.NewShortcutsItem(i18n.Tr("Insert emoji"), "<Control>period"))
	d.Add(messages)

	if a.window != nil {
		g := a.window.shortcuts
		if available, _ := g.Available(); available {
			keys := map[string][]string{}
			for _, b := range g.Bindings() {
				keys[b.ID] = b.Accelerators()
			}
			global := adw.NewShortcutsSection(i18n.Tr("Global Shortcuts"))
			for _, s := range g.Shortcuts() {
				accel := strings.Join(keys[s.ID], " ")
				item := adw.NewShortcutsItem(s.Description, accel)
				if accel == "" {
					item.SetSubtitle(i18n.Tr("Not assigned – set up in Preferences → General"))
				}
				global.Add(item)
			}
			d.Add(global)
		}
	}

	d.Present(a.window)
}
