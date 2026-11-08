package admin

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// ShowCreateSub presents a dialog to create a sub-channel under parentID.
// On confirmation it emits bus.ChannelCreateRequested. Called on the main loop.
func ShowCreateSub(parent gtk.Widgetter, b *bus.Bus, parentID uint32) {
	nameEntry := adw.NewEntryRow()
	nameEntry.SetTitle(tr("Channel name"))

	tempRow := adw.NewSwitchRow()
	tempRow.SetTitle(tr("Temporary"))
	tempRow.SetSubtitle(tr("Removed automatically when the last user leaves"))

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.AddCSSClass("boxed-list")
	list.Append(nameEntry)
	list.Append(tempRow)

	alert := adw.NewAlertDialog(tr("Create Sub-channel"), "")
	alert.SetExtraChild(list)
	alert.AddResponse("cancel", tr("Cancel"))
	alert.AddResponse("create", tr("Create"))
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

// ShowRename presents a dialog to rename an existing channel. On confirmation
// it emits bus.ChannelRenameRequested. Called on the main loop.
func ShowRename(parent gtk.Widgetter, b *bus.Bus, channelID uint32, currentName string) {
	nameEntry := adw.NewEntryRow()
	nameEntry.SetTitle(tr("New name"))
	nameEntry.SetText(currentName)

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionNone)
	list.AddCSSClass("boxed-list")
	list.Append(nameEntry)

	alert := adw.NewAlertDialog(tr("Rename Channel"), "")
	alert.SetExtraChild(list)
	alert.AddResponse("cancel", tr("Cancel"))
	alert.AddResponse("rename", tr("Rename"))
	alert.SetResponseAppearance("rename", adw.ResponseSuggested)
	alert.SetDefaultResponse("rename")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(response string) {
		if response != "rename" {
			return
		}
		name := nameEntry.Text()
		if name == "" || name == currentName {
			return
		}
		b.Emit(bus.ChannelRenameRequested{ChannelID: channelID, Name: name})
	})
	alert.Present(parent)
}

// ShowDelete presents a destructive confirmation to remove a channel. On
// confirmation it emits bus.ChannelDeleteRequested. Called on the main loop.
func ShowDelete(parent gtk.Widgetter, b *bus.Bus, channelID uint32, name string) {
	alert := adw.NewAlertDialog(
		fmt.Sprintf(tr("Remove %s?"), name),
		tr("This channel and all its sub-channels will be permanently removed."),
	)
	alert.AddResponse("cancel", tr("Cancel"))
	alert.AddResponse("delete", tr("Remove"))
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
