package admin

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
)

// ShowKick presents a confirmation dialog for kicking a user. On confirmation
// it emits bus.UserKickRequested. Called on the main loop.
func ShowKick(parent gtk.Widgetter, b *bus.Bus, session uint32, name string) {
	reasonEntry := adw.NewEntryRow()
	reasonEntry.SetTitle(i18n.Tr("Reason (optional)"))

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.AddCSSClass("boxed-list")
	list.Append(reasonEntry)

	alert := adw.NewAlertDialog(
		fmt.Sprintf(i18n.Tr("Kick %s?"), name),
		i18n.Tr("The user will be disconnected and may reconnect immediately."),
	)
	alert.SetExtraChild(list)
	alert.AddResponse("cancel", i18n.Tr("Cancel"))
	// TRANSLATORS: Button: kick (remove) the user from the server.
	alert.AddResponse("kick", i18n.Tr("Kick"))
	alert.SetResponseAppearance("kick", adw.ResponseDestructive)
	alert.SetDefaultResponse("cancel")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(response string) {
		if response == "kick" {
			b.Emit(bus.UserKickRequested{Session: session, Reason: reasonEntry.Text()})
		}
	})
	alert.Present(parent)
}
