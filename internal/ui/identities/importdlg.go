package identities

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/certs"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/ui/clock"
	"github.com/one337/murmel/internal/ui/weakref"
)

// importDialog asks for the password of a protected certificate file
// (when needed) and for the name of the new identity.
type importDialog struct {
	m        *certs.Manager
	data     []byte
	fileName string // file name without extension: fallback name
	bundle   *certs.Bundle
	done     func(meta certs.IdentityMeta, existing bool)

	dialog   *adw.Dialog
	intro    *adw.PreferencesGroup
	pwRow    *adw.PasswordEntryRow
	nameRow  *adw.EntryRow
	details  *adw.PreferencesGroup
	fpRow    *adw.ActionRow
	validRow *adw.ActionRow
	mailRow  *adw.ActionRow
	button   *gtk.Button
	spinner  *adw.Spinner
	status   *gtk.Label
	busy     bool
}

// newImportDialog shows bundle (an unprotected file) or asks for the
// password of data first (bundle nil).
func newImportDialog(m *certs.Manager, data []byte, fileName string, bundle *certs.Bundle, done func(certs.IdentityMeta, bool)) *importDialog {
	d := &importDialog{m: m, data: data, fileName: fileName, done: done}

	d.pwRow = adw.NewPasswordEntryRow()
	d.pwRow.SetTitle(i18n.Tr("Password"))
	d.intro = adw.NewPreferencesGroup()
	d.intro.SetDescription(fmt.Sprintf(i18n.Tr("“%s” is protected by a password."), fileName))
	d.intro.Add(d.pwRow)

	d.nameRow = adw.NewEntryRow()
	d.nameRow.SetTitle(i18n.Tr("Name"))
	d.fpRow = infoRow(i18n.Tr("Fingerprint"))
	d.validRow = infoRow(i18n.Tr("Valid Until"))
	d.mailRow = infoRow(i18n.Tr("Email"))
	d.details = adw.NewPreferencesGroup()
	d.details.SetDescription(i18n.Tr("The name is shown in Murmel only. Servers recognize registered accounts by the certificate."))
	d.details.Add(d.nameRow)
	d.details.Add(d.fpRow)
	d.details.Add(d.validRow)
	d.details.Add(d.mailRow)

	d.spinner = adw.NewSpinner()
	d.spinner.SetVisible(false)
	d.status = gtk.NewLabel("")
	d.status.AddCSSClass("dim-label")
	d.status.SetWrap(true)
	d.status.SetXAlign(0)
	d.status.SetHExpand(true)
	statusBox := gtk.NewBox(gtk.OrientationHorizontal, 8)
	statusBox.Append(d.spinner)
	statusBox.Append(d.status)

	box := gtk.NewBox(gtk.OrientationVertical, 12)
	box.SetMarginTop(12)
	box.SetMarginBottom(18)
	box.SetMarginStart(18)
	box.SetMarginEnd(18)
	box.Append(d.intro)
	box.Append(d.details)
	box.Append(statusBox)

	d.button = gtk.NewButton()
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
	d.dialog.SetTitle(i18n.Tr("Import Certificate"))
	d.dialog.SetContentWidth(createDialogWidth)
	d.dialog.SetChild(toolbar)
	d.dialog.SetDefaultWidget(d.button)
	d.pwRow.SetActivatesDefault(true)
	d.nameRow.SetActivatesDefault(true)

	dialogRef := weakref.New(d.dialog)
	cancel.ConnectClicked(func() { dialogRef.Do(func(dlg *adw.Dialog) { dlg.Close() }) })
	d.button.ConnectClicked(d.activate)
	d.pwRow.ConnectChanged(func() {
		d.pwRow.RemoveCSSClass("error")
		d.status.SetText("")
	})
	d.nameRow.ConnectChanged(d.update)

	if bundle != nil {
		d.show(bundle)
	} else {
		d.details.SetVisible(false)
		d.button.SetLabel(i18n.Tr("Unlock"))
	}
	return d
}

func infoRow(title string) *adw.ActionRow {
	r := adw.NewActionRow()
	r.SetUseMarkup(false)
	r.SetTitle(title)
	r.AddCSSClass("property")
	r.SetSubtitleSelectable(true)
	return r
}

// show switches to naming the identity in b.
func (d *importDialog) show(b *certs.Bundle) {
	d.bundle = b
	d.intro.SetVisible(false)
	d.details.SetVisible(true)
	name := b.CommonName()
	if name == "" {
		name = d.fileName
	}
	d.nameRow.SetText(name)
	d.fpRow.SetSubtitle(strings.ToUpper(b.Fingerprint()))
	d.validRow.SetSubtitle(clock.Date(b.Leaf.NotAfter))
	if mail := b.Email(); mail != "" {
		d.mailRow.SetSubtitle(mail)
	} else {
		d.mailRow.SetVisible(false)
	}
	d.button.SetLabel(i18n.Tr("Import"))
	d.update()
}

func (d *importDialog) update() {
	if d.bundle != nil {
		d.button.SetSensitive(!d.busy && strings.TrimSpace(d.nameRow.Text()) != "")
	}
}

func (d *importDialog) setBusy(busy bool, text string) {
	d.busy = busy
	d.spinner.SetVisible(busy)
	d.intro.SetSensitive(!busy)
	d.details.SetSensitive(!busy)
	d.button.SetSensitive(!busy)
	d.status.SetText(text)
	if !busy {
		d.update()
	}
}

func (d *importDialog) activate() {
	if d.busy {
		return
	}
	if d.bundle == nil {
		d.unlock()
	} else {
		d.store()
	}
}

// unlock opens the file with the typed password, off the main loop.
func (d *importDialog) unlock() {
	pw := d.pwRow.Text()
	d.setBusy(true, i18n.Tr("Opening the file…"))
	go func() {
		b, err := certs.ReadIdentity(d.data, pw)
		glib.IdleAdd(func() {
			d.setBusy(false, "")
			switch {
			case errors.Is(err, certs.ErrWrongPassword):
				d.pwRow.AddCSSClass("error")
				msg := i18n.Tr("Wrong password. If it is correct, the file may be damaged.")
				d.status.SetText(msg)
				d.dialog.Announce(msg, gtk.AccessibleAnnouncementPriorityMedium)
				d.pwRow.GrabFocus()
			case err != nil:
				d.status.SetText(ImportError(err))
			default:
				if d.existing(b) {
					return
				}
				d.show(b)
				d.nameRow.GrabFocus()
			}
		})
	}()
}

// existing finishes the dialog when b is already stored.
func (d *importDialog) existing(b *certs.Bundle) bool {
	meta, ok := d.m.FindCertificate(b.Fingerprint())
	if ok {
		d.dialog.Close()
		d.done(meta, true)
	}
	return ok
}

func (d *importDialog) store() {
	name := strings.TrimSpace(d.nameRow.Text())
	if name == "" {
		return
	}
	b := d.bundle
	d.setBusy(true, i18n.Tr("Importing the certificate…"))
	go func() {
		ident, err := d.m.Store(name, b)
		glib.IdleAdd(func() {
			var dup *certs.DuplicateError
			switch {
			case errors.As(err, &dup):
				d.dialog.Close()
				d.done(dup.Meta, true)
			case err != nil:
				slog.Info("identity not imported", "err", err)
				d.setBusy(false, ImportError(err))
			default:
				d.dialog.Close()
				d.done(ident.Meta, false)
			}
		})
	}()
}

// ImportError is the message for a certificate file Murmel cannot import;
// it never quotes the file's content.
func ImportError(err error) string {
	switch {
	case errors.Is(err, certs.ErrWrongPassword):
		return i18n.Tr("Wrong password. If it is correct, the file may be damaged.")
	case errors.Is(err, certs.ErrUnsupportedFile):
		return i18n.Tr("This file is not a certificate Murmel can read. Use a PKCS #12 file (.p12, .pfx) or a PEM file with certificate and private key.")
	case errors.Is(err, certs.ErrTooManyIterations):
		return i18n.Tr("This file asks for an unusually costly password check and was not opened.")
	case errors.Is(err, certs.ErrNoKey):
		return i18n.Tr("The file contains no private key. Export the certificate together with its private key.")
	case errors.Is(err, certs.ErrNoCertificate):
		return i18n.Tr("The file contains no certificate.")
	case errors.Is(err, certs.ErrUnreadableCertificate):
		return i18n.Tr("Murmel cannot read the certificate in this file. Export it again from the program it comes from, without a password or with a current encryption method.")
	case errors.Is(err, certs.ErrKeyMismatch):
		return i18n.Tr("The private key in the file does not belong to its certificate.")
	}
	return i18n.Tr("The file is damaged or not a certificate.")
}
