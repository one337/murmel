package identities

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/certs"
)

const createDialogWidth = 420

// ShowCreate asks for a name (prefilled with name) and an optional e-mail
// address and creates a new identity: an RSA 2048 self-signed certificate
// like the official Mumble client's. done runs with the new identity; the
// user is then offered to back it up.
func ShowCreate(parent gtk.Widgetter, m *certs.Manager, name string, done func(certs.IdentityMeta)) {
	nameRow := adw.NewEntryRow()
	nameRow.SetTitle(tr("Name"))
	nameRow.SetText(name)
	emailRow := adw.NewEntryRow()
	emailRow.SetTitle(tr("E-mail (optional)"))
	emailRow.SetInputPurpose(gtk.InputPurposeEmail)

	group := adw.NewPreferencesGroup()
	group.SetDescription(tr("A certificate is your identity on Mumble servers: registered accounts are recognized by it. Keep a backup — if it is lost, so is the registration. The e-mail address is only stored in the certificate."))
	group.Add(nameRow)
	group.Add(emailRow)

	spinner := adw.NewSpinner()
	spinner.SetVisible(false)
	status := gtk.NewLabel("")
	status.AddCSSClass("dim-label")
	status.SetWrap(true)
	statusBox := gtk.NewBox(gtk.OrientationHorizontal, 8)
	statusBox.SetHAlign(gtk.AlignCenter)
	statusBox.Append(spinner)
	statusBox.Append(status)

	box := gtk.NewBox(gtk.OrientationVertical, 12)
	box.SetMarginTop(12)
	box.SetMarginBottom(18)
	box.SetMarginStart(18)
	box.SetMarginEnd(18)
	box.Append(group)
	box.Append(statusBox)

	create := gtk.NewButtonWithLabel(tr("Create"))
	create.AddCSSClass("suggested-action")
	cancel := gtk.NewButtonWithLabel(tr("Cancel"))
	hdr := adw.NewHeaderBar()
	hdr.SetShowEndTitleButtons(false)
	hdr.SetShowStartTitleButtons(false)
	hdr.PackStart(cancel)
	hdr.PackEnd(create)
	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(hdr)
	toolbar.SetContent(box)

	dialog := adw.NewDialog()
	dialog.SetTitle(tr("New Certificate"))
	dialog.SetContentWidth(createDialogWidth)
	dialog.SetChild(toolbar)
	dialog.SetDefaultWidget(create)
	nameRow.SetActivatesDefault(true)
	emailRow.SetActivatesDefault(true)

	update := func() { create.SetSensitive(strings.TrimSpace(nameRow.Text()) != "") }
	nameRow.ConnectChanged(update)
	update()
	cancel.ConnectClicked(func() { dialog.Close() })

	create.ConnectClicked(func() {
		n := strings.TrimSpace(nameRow.Text())
		email := strings.TrimSpace(emailRow.Text())
		if n == "" {
			return
		}
		// Generating the RSA key takes a moment: off the main loop.
		create.SetSensitive(false)
		group.SetSensitive(false)
		spinner.SetVisible(true)
		status.SetText(tr("Creating the certificate…"))
		go func() {
			ident, err := m.Generate(n, email)
			glib.IdleAdd(func() {
				if err != nil {
					slog.Info("identity not created", "err", err)
					spinner.SetVisible(false)
					status.SetText(fmt.Sprintf(tr("The certificate could not be created: %v"), err))
					group.SetSensitive(true)
					update()
					return
				}
				dialog.Close()
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
	alert := adw.NewAlertDialog(tr("Certificate Created"),
		fmt.Sprintf(tr("Save a backup of “%s” now? Without it you cannot use your registered accounts on another computer or after reinstalling."), meta.Name))
	alert.AddResponse("later", tr("Later"))
	alert.AddResponse("backup", tr("Back Up…"))
	alert.SetResponseAppearance("backup", adw.ResponseSuggested)
	alert.SetDefaultResponse("backup")
	alert.SetCloseResponse("later")
	alert.ConnectResponse(func(r string) {
		if r != "backup" {
			return
		}
		Export(WindowOf(parent), m, meta, func(err error) {
			if err != nil {
				slog.Info("identity backup failed", "err", err)
				failed := adw.NewAlertDialog(tr("Backup Failed"), err.Error())
				failed.AddResponse("ok", tr("OK"))
				failed.Present(parent)
			}
		})
	})
	alert.Present(parent)
}

// WindowOf finds the toplevel window of w for file dialogs (nil when w is
// not shown).
func WindowOf(w gtk.Widgetter) *gtk.Window {
	if w == nil {
		return nil
	}
	root := gtk.BaseWidget(w).Root()
	if root == nil {
		return nil
	}
	switch win := coreglib.InternObject(root).Cast().(type) {
	case *adw.PreferencesWindow:
		return &win.Window.Window
	case *adw.ApplicationWindow:
		return &win.ApplicationWindow.Window
	case *adw.Window:
		return &win.Window
	case *gtk.ApplicationWindow:
		return &win.Window
	case *gtk.Window:
		return win
	}
	return nil
}
