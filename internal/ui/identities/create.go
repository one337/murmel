package identities

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/certs"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/ui/weakref"
)

const createDialogWidth = 420

// ShowCreate asks for a name (prefilled with name) and an optional e-mail
// address and creates a new identity: an RSA 2048 self-signed certificate
// like the official Mumble client's. done runs with the new identity; the
// user is then offered to back it up.
func ShowCreate(parent gtk.Widgetter, m *certs.Manager, name string, done func(certs.IdentityMeta)) {
	nameRow := adw.NewEntryRow()
	nameRow.SetTitle(i18n.Tr("Name"))
	nameRow.SetText(name)
	emailRow := adw.NewEntryRow()
	emailRow.SetTitle(i18n.Tr("Email (optional)"))
	emailRow.SetInputPurpose(gtk.InputPurposeEmail)

	group := adw.NewPreferencesGroup()
	group.SetDescription(i18n.Tr("A certificate is your identity on Mumble servers: registered accounts are recognized by it. Keep a backup — if it is lost, so is the registration. The email address is stored in the certificate and visible to the servers you connect to."))
	group.Add(nameRow)
	group.Add(emailRow)

	spinner := adw.NewSpinner()
	spinner.SetVisible(false)
	status := gtk.NewLabel("")
	status.AddCSSClass("dim-label")
	status.SetWrap(true)
	status.SetXAlign(0)
	status.SetHExpand(true)
	// Full width, starting at the fields' left edge like the description.
	statusBox := gtk.NewBox(gtk.OrientationHorizontal, 8)
	statusBox.Append(spinner)
	statusBox.Append(status)

	box := gtk.NewBox(gtk.OrientationVertical, 12)
	box.SetMarginTop(12)
	box.SetMarginBottom(18)
	box.SetMarginStart(18)
	box.SetMarginEnd(18)
	box.Append(group)
	box.Append(statusBox)

	create := gtk.NewButtonWithLabel(i18n.Tr("Create"))
	create.AddCSSClass("suggested-action")
	cancel := gtk.NewButtonWithLabel(i18n.Tr("Cancel"))
	hdr := adw.NewHeaderBar()
	hdr.SetShowEndTitleButtons(false)
	hdr.SetShowStartTitleButtons(false)
	hdr.PackStart(cancel)
	hdr.PackEnd(create)
	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(hdr)
	toolbar.SetContent(box)

	dialog := adw.NewDialog()
	dialog.SetTitle(i18n.Tr("New Certificate"))
	dialog.SetContentWidth(createDialogWidth)
	dialog.SetChild(toolbar)
	dialog.SetDefaultWidget(create)
	nameRow.SetActivatesDefault(true)
	emailRow.SetActivatesDefault(true)

	// The closures below hold the fields, the Create button and the dialog
	// only weakly: they are attached to them (see internal/ui/weakref).
	nameRef, emailRef := weakref.New(nameRow), weakref.New(emailRow)
	createRef, dialogRef := weakref.New(create), weakref.New(dialog)

	// emailOK checks the address as typed: an invalid one is marked and
	// blocks Create; one servers cannot read only gets a hint.
	emailOK := func() bool {
		emailRow, ok := emailRef.Get()
		if !ok {
			return false
		}
		email := strings.TrimSpace(emailRow.Text())
		if email == "" {
			emailRow.RemoveCSSClass("error")
			status.SetText("")
			return true
		}
		unreadable, err := certs.CheckEmail(email)
		switch {
		case err != nil:
			emailRow.AddCSSClass("error")
			status.SetText(i18n.Tr("This is not a valid email address."))
			return false
		case unreadable:
			status.SetText(i18n.Tr("Mumble servers cannot read addresses with letters such as ä or é before the @. The address is stored in the certificate, but servers will not see it."))
		default:
			status.SetText("")
		}
		emailRow.RemoveCSSClass("error")
		return true
	}
	update := func() {
		nameRow, ok1 := nameRef.Get()
		create, ok2 := createRef.Get()
		if ok1 && ok2 {
			create.SetSensitive(strings.TrimSpace(nameRow.Text()) != "" && emailOK())
		}
	}
	nameRow.ConnectChanged(update)
	emailRow.ConnectChanged(update)
	update()
	cancel.ConnectClicked(func() { dialogRef.Do(func(d *adw.Dialog) { d.Close() }) })

	create.ConnectClicked(func() {
		nameRow, ok1 := nameRef.Get()
		emailRow, ok2 := emailRef.Get()
		create, ok3 := createRef.Get()
		if !ok1 || !ok2 || !ok3 {
			return
		}
		n := strings.TrimSpace(nameRow.Text())
		email := strings.TrimSpace(emailRow.Text())
		if n == "" {
			return
		}
		// Generating the RSA key takes a moment: off the main loop.
		create.SetSensitive(false)
		group.SetSensitive(false)
		spinner.SetVisible(true)
		status.SetText(i18n.Tr("Creating the certificate…"))
		go func() {
			ident, err := m.Generate(n, email)
			glib.IdleAdd(func() {
				if err != nil {
					slog.Info("identity not created", "err", err)
					spinner.SetVisible(false)
					group.SetSensitive(true)
					update()
					status.SetText(fmt.Sprintf(i18n.Tr("The certificate could not be created: %v"), err))
					return
				}
				dialogRef.Do(func(d *adw.Dialog) { d.Close() })
				done(ident.Meta)
				offerBackup(parent, m, ident.Meta)
			})
		}()
	})
	dialog.Present(parent)
	nameRow.GrabFocus()
}

// offerBackup suggests exporting a freshly created identity.
func offerBackup(parent gtk.Widgetter, m *certs.Manager, meta certs.IdentityMeta) {
	alert := adw.NewAlertDialog(i18n.Tr("Certificate Created"),
		fmt.Sprintf(i18n.Tr("Save a backup of “%s” now? Without it you cannot use your registered accounts on another computer or after reinstalling."), meta.Name)+
			"\n\n"+i18n.Tr("The backup contains your private key. Keep it in a safe place."))
	// TRANSLATORS: Button: back up the new certificate later.
	alert.AddResponse("later", i18n.Tr("Later"))
	alert.AddResponse("backup", i18n.Tr("Back Up…"))
	alert.SetResponseAppearance("backup", adw.ResponseSuggested)
	alert.SetDefaultResponse("backup")
	alert.SetCloseResponse("later")
	alert.ConnectResponse(func(r string) {
		if r != "backup" {
			return
		}
		Export(parent, m, meta, func(exposed bool, err error) {
			if err != nil {
				slog.Info("identity backup failed", "err", err)
				failed := adw.NewAlertDialog(i18n.Tr("Backup Failed"), err.Error())
				failed.AddResponse("ok", i18n.Tr("OK"))
				failed.Present(parent)
				return
			}
			if exposed {
				WarnExposedBackup(parent)
			}
		})
	})
	alert.Present(parent)
}
