package admin

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// ShowKick presents a confirmation dialog for kicking a user. On confirmation
// it emits bus.UserKickRequested. Called on the main loop.
func ShowKick(parent gtk.Widgetter, b *bus.Bus, session uint32, name string) {
	reasonEntry := adw.NewEntryRow()
	reasonEntry.SetTitle(tr("Reason (optional)"))

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.AddCSSClass("boxed-list")
	list.Append(reasonEntry)

	alert := adw.NewAlertDialog(
		fmt.Sprintf(tr("Kick %s?"), name),
		tr("The user will be disconnected and may reconnect immediately."),
	)
	alert.SetExtraChild(list)
	alert.AddResponse("cancel", tr("Cancel"))
	// TRANSLATORS: Button: kick (remove) the user from the server.
	alert.AddResponse("kick", tr("Kick"))
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
