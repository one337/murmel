package admin

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
)

// ShowCreateSub presents a dialog to create a sub-channel under parentID.
// On confirmation it emits bus.ChannelCreateRequested. Called on the main loop.
func ShowCreateSub(parent gtk.Widgetter, b *bus.Bus, parentID uint32) {
	nameEntry := adw.NewEntryRow()
	nameEntry.SetTitle(i18n.Tr("Channel name"))

	tempRow := adw.NewSwitchRow()
	tempRow.SetTitle(i18n.Tr("Temporary"))
	tempRow.SetSubtitle(i18n.Tr("Removed automatically when the last user leaves"))

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.AddCSSClass("boxed-list")
	list.Append(nameEntry)
	list.Append(tempRow)

	alert := adw.NewAlertDialog(i18n.Tr("Create Sub-channel"), "")
	alert.SetExtraChild(list)
	alert.AddResponse("cancel", i18n.Tr("Cancel"))
	alert.AddResponse("create", i18n.Tr("Create"))
	alert.SetResponseAppearance("create", adw.ResponseSuggested)
	alert.SetDefaultResponse("create")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(response string) {
		if response != "create" {
			return
		}
		name := nameEntry.Text()
		if name == "" {
			return
		}
		b.Emit(bus.ChannelCreateRequested{
			ParentID:  parentID,
			Name:      name,
			Temporary: tempRow.Active(),
		})
	})
	alert.Present(parent)
}

// ShowDelete presents a destructive confirmation to remove a channel. On
// confirmation it emits bus.ChannelDeleteRequested. Called on the main loop.
func ShowDelete(parent gtk.Widgetter, b *bus.Bus, channelID uint32, name string) {
	alert := adw.NewAlertDialog(
		fmt.Sprintf(i18n.Tr("Remove %s?"), name),
		i18n.Tr("This channel and all its sub-channels will be permanently removed."),
	)
	alert.AddResponse("cancel", i18n.Tr("Cancel"))
	alert.AddResponse("delete", i18n.Tr("Remove"))
	alert.SetResponseAppearance("delete", adw.ResponseDestructive)
	alert.SetDefaultResponse("cancel")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(response string) {
		if response == "delete" {
			b.Emit(bus.ChannelDeleteRequested{ChannelID: channelID})
		}
	})
	alert.Present(parent)
}
