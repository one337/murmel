package ui

import "github.com/diamondburned/gotk4-adwaita/pkg/adw"

// showShortcuts presents the keyboard shortcuts overview (Main Menu →
// Keyboard Shortcuts, Ctrl+?): the app's own shortcuts, which are fixed,
// and the global ones with the keys the desktop assigned (changed in
// Preferences → General).
func (a *App) showShortcuts() {
	d := adw.NewShortcutsDialog()

	general := adw.NewShortcutsSection(tr("General"))
	general.Add(adw.NewShortcutsItem(tr("Show keyboard shortcuts"), "<Control>question"))
	general.Add(adw.NewShortcutsItem(tr("Open the main menu"), "F10"))
	general.Add(adw.NewShortcutsItem(tr("Preferences"), "<Control>comma"))
	general.Add(adw.NewShortcutsItem(tr("Fullscreen"), "F11"))
	general.Add(adw.NewShortcutsItem(tr("Quit"), "<Control>q"))
	d.Add(general)

	messages := adw.NewShortcutsSection(tr("Messages"))
	messages.Add(adw.NewShortcutsItem(tr("Send the message"), "Return"))
	messages.Add(adw.NewShortcutsItem(tr("New line"), "<Shift>Return"))
	messages.Add(adw.NewShortcutsItem(tr("Bold"), "<Control>b"))
	messages.Add(adw.NewShortcutsItem(tr("Italic"), "<Control>i"))
	messages.Add(adw.NewShortcutsItem(tr("Insert emoji"), "<Control>period"))
	d.Add(messages)

	if a.window != nil {
		g := a.window.shortcuts
		if available, _ := g.Available(); available {
			keys := map[string][]string{}
			for _, b := range g.Bindings() {
				keys[b.ID] = b.Accelerators()
			}
			global := adw.NewShortcutsSection(tr("Global Shortcuts"))
			for _, s := range g.Shortcuts() {
				accel := ""
				for i, k := range keys[s.ID] {
					if i > 0 {
						accel += " "
					}
					accel += k
				}
				item := adw.NewShortcutsItem(s.Description, accel)
				if accel == "" {
					item.SetSubtitle(tr("Not assigned – set up in Preferences → General"))
				}
				global.Add(item)
			}
			d.Add(global)
		}
	}

	d.Present(a.window)
}
