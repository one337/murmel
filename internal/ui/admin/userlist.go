package admin

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// ShowUserList opens the registered user management dialog. It emits
// bus.UserListRequested to trigger a server fetch; when bus.UserListFetched
// arrives the list is populated. The user can deregister individual users
// which emits bus.UserUnregisterRequested. Called on the main loop.
func ShowUserList(parent gtk.Widgetter, b *bus.Bus) {
	listbox := gtk.NewListBox()
	listbox.SetSelectionMode(gtk.SelectionNone)
	listbox.AddCSSClass("boxed-list")

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(listbox)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	scroll.SetMinContentHeight(200)

	spinner := gtk.NewSpinner()
	spinner.SetSpinning(true)
	spinner.SetMarginTop(24)
	spinner.SetMarginBottom(24)
	spinner.SetHAlign(gtk.AlignCenter)

	loadingRow := gtk.NewListBoxRow()
	loadingRow.SetChild(spinner)
	loadingRow.SetSelectable(false)
	loadingRow.SetActivatable(false)
	listbox.Append(loadingRow)

	closeBtn := gtk.NewButtonWithLabel(tr("Close"))

	hdr := adw.NewHeaderBar()
	hdr.SetShowEndTitleButtons(false)
	hdr.SetShowStartTitleButtons(false)
	hdr.PackStart(closeBtn)
	title := adw.NewWindowTitle(tr("Registered Users"), "")
	hdr.SetTitleWidget(title)

	content := gtk.NewBox(gtk.OrientationVertical, 12)
	content.SetMarginTop(12)
	content.SetMarginBottom(12)
	content.SetMarginStart(12)
	content.SetMarginEnd(12)
	content.Append(scroll)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(hdr)
	toolbar.SetContent(content)

	dialog := adw.NewDialog()
	dialog.SetTitle(tr("Registered Users"))
	dialog.SetContentWidth(480)
	dialog.SetContentHeight(400)
	dialog.SetChild(toolbar)

	// One-shot subscription: auto-unsubscribes after first event.
	var unsub func()
	unsub = bus.Subscribe(b, func(ev bus.UserListFetched) {
		if unsub != nil {
			f := unsub
			unsub = nil
			f()
		}

		listbox.Remove(loadingRow)

		if len(ev.Users) == 0 {
			emptyRow := gtk.NewListBoxRow()
			emptyRow.SetSelectable(false)
			emptyRow.SetActivatable(false)
			lbl := gtk.NewLabel(tr("No registered users"))
			lbl.AddCSSClass("dim-label")
			lbl.SetMarginTop(24)
			lbl.SetMarginBottom(24)
			emptyRow.SetChild(lbl)
			listbox.Append(emptyRow)
			return
		}

		for _, u := range ev.Users {
			userID := u.UserID
			row := adw.NewActionRow()
			row.SetTitle(u.Name)
			row.SetSubtitle(fmt.Sprintf(tr("ID: %d"), u.UserID))

			deregBtn := gtk.NewButtonFromIconName("user-trash-symbolic")
			deregBtn.AddCSSClass("flat")
			deregBtn.SetTooltipText(tr("Deregister user"))
			deregBtn.ConnectClicked(func() {
				b.Emit(bus.UserUnregisterRequested{UserID: userID})
				row.SetSensitive(false)
			})
			row.AddSuffix(deregBtn)
			listbox.Append(row)
		}
	})

	closeBtn.ConnectClicked(func() {
		if unsub != nil {
			unsub()
			unsub = nil
		}
		dialog.Close()
	})

	b.Emit(bus.UserListRequested{})
	dialog.Present(parent)
}
