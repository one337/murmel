package prefs

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/ui/secrets"
	"github.com/one337/murmel/internal/ui/weakref"
)

// passwordRows are the rows of the Passwords group.
type passwordRows struct {
	// status says where passwords are saved.
	status *adw.ActionRow
	// file turns the unencrypted file on and off (only without a keyring).
	file *adw.SwitchRow
	// damaged names unreadable password files that were set aside.
	damaged *adw.ActionRow
	// move offers to move passwords left in the file into the keyring.
	move       *adw.ActionRow
	moveButton *gtk.Button
}

// weakPasswordRows holds the rows weakly, for signal handlers attached to
// them (a strong reference there would keep them alive).
type weakPasswordRows struct {
	status, damaged, move weakref.Ref[*adw.ActionRow]
	file                  weakref.Ref[*adw.SwitchRow]
	moveButton            weakref.Ref[*gtk.Button]
	// syncing is set while the rows follow the state, so the switch's
	// change is not taken for the user's.
	syncing *bool
}

func (r *passwordRows) weak() weakPasswordRows {
	return weakPasswordRows{
		status: weakref.New(r.status), damaged: weakref.New(r.damaged), move: weakref.New(r.move),
		file: weakref.New(r.file), moveButton: weakref.New(r.moveButton), syncing: new(bool),
	}
}

// passwordsGroup says where passwords are saved and offers the unencrypted
// file without a keyring; it follows changes while pw is open.
func passwordsGroup(passwords secrets.Passwords, b *bus.Bus, pw *adw.PreferencesDialog) (*adw.PreferencesGroup, *passwordRows) {
	group := adw.NewPreferencesGroup()
	group.SetTitle(i18n.Tr("Passwords"))
	r := &passwordRows{
		status:     adw.NewActionRow(),
		file:       adw.NewSwitchRow(),
		damaged:    adw.NewActionRow(),
		move:       adw.NewActionRow(),
		moveButton: gtk.NewButtonWithLabel(i18n.Tr("Move to Keyring")),
	}
	for _, row := range []*adw.ActionRow{r.status, r.damaged, r.move} {
		row.SetUseMarkup(false)
	}
	r.file.SetUseMarkup(false)
	r.file.SetTitle(i18n.Tr("Save Passwords in an Unencrypted File"))
	r.file.SetSubtitle(i18n.Tr("Anyone who can read your files can read them"))
	r.damaged.SetTitle(i18n.Tr("A damaged password file was set aside"))
	r.damaged.AddCSSClass("warning")
	r.moveButton.SetVAlign(gtk.AlignCenter)
	r.move.AddSuffix(r.moveButton)
	group.Add(r.status)
	group.Add(r.file)
	group.Add(r.damaged)
	group.Add(r.move)

	w := r.weak()
	pwRef := weakref.New(pw)
	toast := func(text string) {
		pwRef.Do(func(pw *adw.PreferencesDialog) {
			t := adw.NewToast(text)
			t.SetUseMarkup(false)
			pw.AddToast(t)
		})
	}
	// ask presents a confirmation over the preferences.
	ask := func(f func(parent gtk.Widgetter)) { pwRef.Do(func(pw *adw.PreferencesDialog) { f(pw) }) }
	// run calls op off the main loop and toasts failed when it fails.
	run := func(op func() error, failed string, done func()) {
		go func() {
			err := op()
			glib.IdleAdd(func() bool {
				if err != nil {
					slog.Warn("passwords", "err", err)
					toast(failed)
				} else if done != nil {
					done()
				}
				return false
			})
		}()
	}
	w.sync(passwords)

	onNotify(r.file, "active", func(sw *adw.SwitchRow) {
		if *w.syncing {
			return
		}
		info := passwords.File()
		if sw.Active() {
			ask(func(parent gtk.Widgetter) {
				confirmPasswordFile(parent, info, func(ok bool) {
					if !ok {
						w.sync(passwords)
						return
					}
					run(passwords.UseFile, i18n.Tr("The password file could not be created."), nil)
				})
			})
			return
		}
		ask(func(parent gtk.Widgetter) {
			confirmDeletePasswordFile(parent, info, func(ok bool) {
				if !ok {
					w.sync(passwords)
					return
				}
				run(passwords.StopUsingFile, i18n.Tr("The password file could not be deleted."), nil)
			})
		})
	})
	r.moveButton.ConnectClicked(func() {
		ask(func(parent gtk.Widgetter) {
			confirmMovePasswords(parent, passwords.File(), func(ok bool) {
				if !ok {
					return
				}
				w.moveButton.Do(func(b *gtk.Button) { b.SetSensitive(false) })
				run(passwords.MoveToKeyring,
					i18n.Tr("Passwords not moved: the keyring did not take all of them. The file is kept."),
					func() { toast(i18n.Tr("Passwords moved to your keyring")) })
			})
		})
	})

	ctx, cancel := context.WithCancel(context.Background())
	bus.SubscribeCtx(ctx, b, func(bus.PasswordStorageChanged) { w.sync(passwords) })
	onClose(pw, cancel)
	return group, r
}

// sync makes the rows show the state of passwords (rows already gone are
// left alone).
func (w weakPasswordRows) sync(passwords secrets.Passwords) {
	status, ok1 := w.status.Get()
	file, ok2 := w.file.Get()
	damaged, ok3 := w.damaged.Get()
	move, ok4 := w.move.Get()
	button, ok5 := w.moveButton.Get()
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return
	}
	state, info := passwords.State(), passwords.File()
	title, subtitle := passwordStorageText(state, secrets.Sandboxed("/.flatpak-info"))
	if state == secrets.InFile {
		subtitle = info.Path
	}
	status.SetTitle(title)
	status.SetSubtitle(subtitle)

	*w.syncing = true
	file.SetVisible(info.Offered)
	file.SetActive(state == secrets.InFile)
	*w.syncing = false

	damaged.SetVisible(len(info.Damaged) > 0)
	damaged.SetSubtitle(strings.Join(info.Damaged, "\n"))

	left := state == secrets.InKeyring && info.Exists
	move.SetVisible(left)
	move.SetTitle(fmt.Sprintf(i18n.TrN("%d password is in an unencrypted file",
		"%d passwords are in an unencrypted file", info.Count), info.Count))
	move.SetSubtitle(info.Path)
	button.SetSensitive(left)
}

// passwordStorageText is the title and subtitle of the Passwords row.
func passwordStorageText(s secrets.State, sandboxed bool) (title, subtitle string) {
	switch s {
	case secrets.InKeyring:
		return i18n.Tr("Saved in your keyring"), ""
	case secrets.InFile:
		return i18n.Tr("Saved in an unencrypted file"), ""
	case secrets.NoKeyring:
		if sandboxed {
			return i18n.Tr("Passwords are not saved"),
				i18n.Tr("No keyring found: the desktop offers no Secret portal.")
		}
		return i18n.Tr("Passwords are not saved"),
			i18n.Tr("No keyring found. To save passwords, set up a keyring such as GNOME Keyring, KWallet or oo7.")
	}
	return i18n.Tr("Looking for a keyring…"), ""
}

// confirm asks heading/body with a cancel and an action response and
// tells answer whether the action was chosen. Tests replace it to answer.
var confirm = func(parent gtk.Widgetter, heading, body, action string, appearance adw.ResponseAppearance, answer func(bool)) {
	alert := adw.NewAlertDialog(heading, body)
	alert.AddResponse("cancel", i18n.Tr("Cancel"))
	alert.AddResponse("ok", action)
	alert.SetResponseAppearance("ok", appearance)
	alert.SetDefaultResponse("cancel")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(response string) { answer(response == "ok") })
	alert.Present(parent)
}

func confirmPasswordFile(parent gtk.Widgetter, info secrets.FileInfo, answer func(bool)) {
	confirm(parent, i18n.Tr("Save Passwords Unencrypted?"),
		fmt.Sprintf(i18n.Tr("Passwords will be saved as plain text in %s. Only your user account can open the file, but every program you run can read it, and so can anyone with access to your files or backups."), info.Path),
		i18n.Tr("Save Unencrypted"), adw.ResponseDestructive, answer)
}

func confirmDeletePasswordFile(parent gtk.Widgetter, info secrets.FileInfo, answer func(bool)) {
	body := fmt.Sprintf(i18n.TrN("The %d password saved in %s will be deleted.",
		"The %d passwords saved in %s will be deleted.", info.Count), info.Count, info.Path)
	if len(info.Damaged) > 0 {
		body += "\n\n" + i18n.Tr("These damaged copies will be deleted as well:") + "\n" + strings.Join(info.Damaged, "\n")
	}
	confirm(parent, i18n.Tr("Delete Saved Passwords?"), body, i18n.Tr("Delete"), adw.ResponseDestructive, answer)
}

func confirmMovePasswords(parent gtk.Widgetter, info secrets.FileInfo, answer func(bool)) {
	body := fmt.Sprintf(i18n.Tr("Each password is saved in your keyring and read back. When all of them are there, %s is deleted."), info.Path)
	if len(info.Damaged) > 0 {
		body += "\n\n" + i18n.Tr("These damaged copies will be deleted as well:") + "\n" + strings.Join(info.Damaged, "\n")
	}
	confirm(parent, i18n.Tr("Move Passwords to Your Keyring?"), body, i18n.Tr("Move"), adw.ResponseSuggested, answer)
}
