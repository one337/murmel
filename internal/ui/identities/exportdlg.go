package identities

import (
	"errors"
	"log/slog"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/certs"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/ui/weakref"
)

// exportDialog asks for the password that protects a backup.
type exportDialog struct {
	m      *certs.Manager
	meta   certs.IdentityMeta
	done   func(data []byte) // runs with the encrypted backup
	dialog *adw.Dialog
	pw     *adw.PasswordEntryRow
	repeat *adw.PasswordEntryRow
	button *gtk.Button
	status *gtk.Label
	spin   *adw.Spinner
	busy   bool
}

func newExportDialog(m *certs.Manager, meta certs.IdentityMeta, rsaKey bool, done func([]byte)) *exportDialog {
	d := &exportDialog{m: m, meta: meta, done: done}
	d.pw = adw.NewPasswordEntryRow()
	d.pw.SetTitle(i18n.Tr("Password"))
	d.repeat = adw.NewPasswordEntryRow()
	d.repeat.SetTitle(i18n.Tr("Repeat Password"))

	group := adw.NewPreferencesGroup()
	group.SetDescription(i18n.Tr("The backup contains your private key, protected by this password. Keep the file and the password in a safe place. The official Mumble client can import it."))
	group.Add(d.pw)
	group.Add(d.repeat)

	d.spin = adw.NewSpinner()
	d.spin.SetVisible(false)
	d.status = gtk.NewLabel("")
	d.status.AddCSSClass("dim-label")
	d.status.SetWrap(true)
	d.status.SetXAlign(0)
	d.status.SetHExpand(true)
	statusBox := gtk.NewBox(gtk.OrientationHorizontal, 8)
	statusBox.Append(d.spin)
	statusBox.Append(d.status)

	box := gtk.NewBox(gtk.OrientationVertical, 12)
	box.SetMarginTop(12)
	box.SetMarginBottom(18)
	box.SetMarginStart(18)
	box.SetMarginEnd(18)
	box.Append(group)
	if !rsaKey {
		hint := gtk.NewLabel(i18n.Tr("The official Mumble client cannot import this certificate: it accepts RSA keys only."))
		hint.SetWrap(true)
		hint.SetXAlign(0)
		hint.AddCSSClass("warning")
		box.Append(hint)
	}
	box.Append(statusBox)

	d.button = gtk.NewButtonWithLabel(i18n.Tr("Save…"))
	d.button.AddCSSClass("suggested-action")
	cancel := gtk.NewButtonWithLabel(i18n.Tr("Cancel"))
	hdr := adw.NewHeaderBar()
	hdr.SetShowEndTitleButtons(false)
	hdr.SetShowStartTitleButtons(false)
	hdr.PackStart(cancel)
	hdr.PackEnd(d.button)
	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(hdr)
	toolbar.SetContent(box)

	d.dialog = adw.NewDialog()
	d.dialog.SetTitle(i18n.Tr("Back Up Certificate"))
	d.dialog.SetContentWidth(createDialogWidth)
	d.dialog.SetChild(toolbar)
	d.dialog.SetDefaultWidget(d.button)
	d.pw.SetActivatesDefault(true)
	d.repeat.SetActivatesDefault(true)

	dialogRef := weakref.New(d.dialog)
	cancel.ConnectClicked(func() { dialogRef.Do(func(dlg *adw.Dialog) { dlg.Close() }) })
	d.pw.ConnectChanged(d.update)
	d.repeat.ConnectChanged(d.update)
	d.button.ConnectClicked(d.save)
	d.update()
	return d
}

// update enables Save once both fields hold the same password and says
// why not otherwise.
func (d *exportDialog) update() {
	pw, rep := d.pw.Text(), d.repeat.Text()
	mismatch := rep != "" && pw != rep
	if mismatch {
		d.repeat.AddCSSClass("error")
		d.status.SetText(i18n.Tr("The passwords do not match."))
	} else {
		d.repeat.RemoveCSSClass("error")
		d.status.SetText("")
	}
	d.button.SetSensitive(!d.busy && pw != "" && pw == rep)
}

func (d *exportDialog) save() {
	pw := d.pw.Text()
	if d.busy || pw == "" || pw != d.repeat.Text() {
		return
	}
	d.busy = true
	d.button.SetSensitive(false)
	d.spin.SetVisible(true)
	d.status.SetText(i18n.Tr("Encrypting the backup…"))
	id := d.meta.ID
	go func() {
		data, err := d.m.ExportP12(id, pw)
		glib.IdleAdd(func() {
			d.busy = false
			d.spin.SetVisible(false)
			if err != nil {
				d.status.SetText("")
				d.update()
				msg := ExportError(err)
				if errors.Is(err, certs.ErrPasswordCharacters) {
					d.pw.AddCSSClass("error")
				} else {
					slog.Info("identity backup not encrypted", "err", err)
				}
				d.status.SetText(msg)
				d.dialog.Announce(msg, gtk.AccessibleAnnouncementPriorityMedium)
				return
			}
			d.dialog.Close()
			d.done(data)
		})
	}()
}

// ExportError is the message for a backup Murmel could not write; it
// names no password or key.
func ExportError(err error) string {
	switch {
	case errors.Is(err, certs.ErrPasswordCharacters):
		return i18n.Tr("The password contains characters a certificate file cannot hold, such as emoji. Choose another password.")
	case errors.Is(err, certs.ErrEmptyPassword):
		return i18n.Tr("Enter a password to protect the backup.")
	}
	return i18n.Tr("The backup could not be created.")
}
