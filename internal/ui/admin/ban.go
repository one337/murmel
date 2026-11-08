package admin

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// ShowBan presents a confirmation dialog for banning a user. On confirmation
// it emits bus.UserBanRequested. Called on the main loop.
func ShowBan(parent gtk.Widgetter, b *bus.Bus, session uint32, name string) {
	reasonEntry := adw.NewEntryRow()
	reasonEntry.SetTitle(tr("Reason (optional)"))

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.AddCSSClass("boxed-list")
	list.Append(reasonEntry)

	alert := adw.NewAlertDialog(
		fmt.Sprintf(tr("Ban %s?"), name),
		tr("The user will be permanently banned from the server."),
	)
	alert.SetExtraChild(list)
	alert.AddResponse("cancel", tr("Cancel"))
	alert.AddResponse("ban", tr("Ban"))
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
