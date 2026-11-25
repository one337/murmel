package admin

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
)

// ShowBan presents a confirmation dialog for banning a user. On confirmation
// it emits bus.UserBanRequested. Called on the main loop.
func ShowBan(parent gtk.Widgetter, b *bus.Bus, session uint32, name string) {
	reasonEntry := adw.NewEntryRow()
	reasonEntry.SetTitle(i18n.Tr("Reason (optional)"))

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.AddCSSClass("boxed-list")
	list.Append(reasonEntry)

	alert := adw.NewAlertDialog(
		fmt.Sprintf(i18n.Tr("Ban %s?"), name),
		i18n.Tr("The user will be permanently banned from the server."),
	)
	alert.SetExtraChild(list)
	alert.AddResponse("cancel", i18n.Tr("Cancel"))
	// TRANSLATORS: Button: ban the user from the server.
	alert.AddResponse("ban", i18n.Tr("Ban"))
	alert.SetResponseAppearance("ban", adw.ResponseDestructive)
	alert.SetDefaultResponse("cancel")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(response string) {
		if response == "ban" {
			b.Emit(bus.UserBanRequested{Session: session, Reason: reasonEntry.Text()})
		}
	})
	alert.Present(parent)
}
