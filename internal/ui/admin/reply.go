package admin

import (
	"context"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// subscribeWhileOpen subscribes handler until dialog closes. A handler must
// never unsubscribe itself: bus handlers run under their subscription's
// lock, so unsubscribing from inside one deadlocks the main loop.
func subscribeWhileOpen[T bus.Event](dialog *adw.Dialog, b *bus.Bus, handler func(T)) {
	ctx, cancel := context.WithCancel(context.Background())
	dialog.ConnectClosed(cancel)
	bus.SubscribeCtx(ctx, b, handler)
}

// awaitReply wires a request dialog: onReply runs for the first reply
// that matches; if the server refuses first (PermissionDenied), the
// loading row is replaced with an explanation instead of spinning forever.
func awaitReply[T bus.Event](dialog *adw.Dialog, b *bus.Bus, list *gtk.ListBox, loading *gtk.ListBoxRow,
	match func(T) bool, onReply func(T)) {
	done := false
	subscribeWhileOpen(dialog, b, func(ev T) {
		if done || (match != nil && !match(ev)) {
			return
		}
		done = true
		onReply(ev)
	})
	subscribeWhileOpen(dialog, b, func(ev bus.PermissionDenied) {
		if done {
			return
		}
		done = true
		list.Remove(loading)
		text := tr("You do not have permission to view this on this server.")
		if ev.Text != "" {
			text = ev.Text
		}
		list.Append(messageRow(text))
	})
}

// messageRow is a non-interactive list row with a dimmed message.
func messageRow(text string) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.SetSelectable(false)
	row.SetActivatable(false)
	lbl := gtk.NewLabel(text)
	lbl.AddCSSClass("dim-label")
	lbl.SetWrap(true)
	lbl.SetMarginTop(24)
	lbl.SetMarginBottom(24)
	lbl.SetMarginStart(12)
	lbl.SetMarginEnd(12)
	row.SetChild(lbl)
	return row
}
