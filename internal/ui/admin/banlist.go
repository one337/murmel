package admin

import (
	"fmt"
	"net"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// ShowBanList opens the ban list management dialog. It emits
// bus.BanListRequested to trigger a server fetch; when bus.BanListFetched
// arrives the list is populated. The user can mark entries for removal and
// click Save to emit bus.BanListWriteRequested. Called on the main loop.
func ShowBanList(parent gtk.Widgetter, b *bus.Bus) {
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

	saveBtn := gtk.NewButtonWithLabel(tr("Save"))
	saveBtn.AddCSSClass("suggested-action")
	saveBtn.SetSensitive(false)
	cancelBtn := gtk.NewButtonWithLabel(tr("Close"))

	hdr := adw.NewHeaderBar()
	hdr.SetShowEndTitleButtons(false)
	hdr.SetShowStartTitleButtons(false)
	hdr.PackStart(cancelBtn)
	hdr.PackEnd(saveBtn)
	title := adw.NewWindowTitle(tr("Ban List"), "")
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
	dialog.SetTitle(tr("Ban List"))
	dialog.SetContentWidth(540)
	dialog.SetContentHeight(400)
	dialog.SetChild(toolbar)

	// Track entries and which are marked for removal.
	var entries []bus.BanEntry
	removed := map[int]bool{}

	// One-shot subscription: auto-unsubscribes after first event.
	var unsub func()
	unsub = bus.Subscribe(b, func(ev bus.BanListFetched) {
		if unsub != nil {
			f := unsub
			unsub = nil
			f()
		}
		entries = ev.Entries
		removed = map[int]bool{}

		// Clear loading indicator and populate rows.
		listbox.Remove(loadingRow)

		if len(entries) == 0 {
			emptyRow := gtk.NewListBoxRow()
			emptyRow.SetSelectable(false)
			emptyRow.SetActivatable(false)
			lbl := gtk.NewLabel(tr("No bans"))
			lbl.AddCSSClass("dim-label")
			lbl.SetMarginTop(24)
			lbl.SetMarginBottom(24)
			emptyRow.SetChild(lbl)
			listbox.Append(emptyRow)
		}

		for i, e := range entries {
			idx := i // capture for closure
			row := adw.NewActionRow()
			row.SetTitle(banEntryTitle(e))
			row.SetSubtitle(banEntrySubtitle(e))

			removeBtn := gtk.NewButtonFromIconName("list-remove-symbolic")
			removeBtn.AddCSSClass("flat")
			removeBtn.SetTooltipText(tr("Remove ban"))
			removeBtn.ConnectClicked(func() {
				removed[idx] = true
				row.SetSensitive(false)
				removeBtn.SetSensitive(false)
			})
			row.AddSuffix(removeBtn)
			listbox.Append(row)
		}

		saveBtn.SetSensitive(true)
	})

	saveBtn.ConnectClicked(func() {
		if unsub != nil {
			unsub()
			unsub = nil
		}
		remaining := make([]bus.BanEntry, 0, len(entries))
		for i, e := range entries {
			if !removed[i] {
				remaining = append(remaining, e)
			}
		}
		b.Emit(bus.BanListWriteRequested{Entries: remaining})
		dialog.Close()
	})

	cancelBtn.ConnectClicked(func() {
		if unsub != nil {
			unsub()
			unsub = nil
		}
		dialog.Close()
	})

	b.Emit(bus.BanListRequested{})
	dialog.Present(parent)
}

func banEntryTitle(e bus.BanEntry) string {
	bits, _ := e.Mask.Size()
	if e.Name != "" {
		return fmt.Sprintf("%s (%s/%d)", e.Name, formatIP(e.Address), bits)
	}
	return fmt.Sprintf("%s/%d", formatIP(e.Address), bits)
}

func banEntrySubtitle(e bus.BanEntry) string {
	reason := e.Reason
	if reason == "" {
		reason = tr("No reason given")
	}
	dur := formatDuration(e.Duration)
	if dur != "" {
		return fmt.Sprintf("%s · %s", reason, dur)
	}
	return reason
}

func formatIP(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}

func formatDuration(d time.Duration) string {
	switch {
	case d == 0:
		return tr("Permanent")
	case d < time.Hour:
		return fmt.Sprintf(tr("%d min"), int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf(tr("%d h"), int(d.Hours()))
	default:
		return fmt.Sprintf(tr("%d days"), int(d.Hours()/24))
	}
}
