package gfileio

import (
	"context"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/i18n"
)

// OpenImage asks for an image file in a dialog over owner's window and
// passes the chosen file to done; done is not called when the user
// cancels.
func OpenImage(owner gtk.Widgetter, title string, done func(*gio.File)) {
	filter := gtk.NewFileFilter()
	filter.SetName(i18n.Tr("Images"))
	filter.AddPixbufFormats()
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(filter.Object)
	fd := gtk.NewFileDialog()
	fd.SetTitle(title)
	fd.SetModal(true)
	fd.SetFilters(filters)
	fd.SetDefaultFilter(filter)
	fd.Open(context.Background(), WindowOf(owner), func(res gio.AsyncResulter) {
		file, err := fd.OpenFinish(res)
		if err != nil || file == nil {
			return
		}
		done(file)
	})
}
