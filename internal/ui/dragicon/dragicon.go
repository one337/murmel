// Package dragicon shows a widget as the icon of a drag and takes the icon
// down after the drag.
//
// GTK keeps a drag's icon on the drag's own surface. Go holds the drag and
// the icon by references of their own and may release them in any order;
// on Wayland, finalizing the drag destroys that surface, and an icon
// released after it is taken down without a frame clock (GTK critical
// "gtk_native_unrealize: assertion 'clock != NULL' failed"). Icon takes
// the icon down itself while the drag, which it holds until then, and its
// surface still exist; the later release then has nothing left to do.
package dragicon

import (
	"runtime"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// returnTime bounds the wait for the drag surface to go: a cancelled drag
// flies back to where it started, and on Wayland the compositor animates
// that without telling GTK when it is over.
const returnTime = time.Second

// Icon is the icon of one drag; nil is valid and does nothing.
type Icon struct {
	icon *gtk.DragIcon
	drag gdk.Dragger
}

// Set shows child as the icon of drag. Main loop only.
func Set(drag gdk.Dragger, child gtk.Widgetter) *Icon {
	icon := gtk.NewDragIconGetForDrag(drag)
	icon.SetChild(child)
	return &Icon{icon: icon, drag: drag}
}

// End takes the icon down once the drag surface is no longer shown (a
// cancelled drag has flown back), at the latest after returnTime. Call it
// from the drag source's drag-end handler. Main loop only.
func (i *Icon) End() {
	if i == nil || i.icon == nil {
		return
	}
	icon, drag := i.icon, i.drag
	i.icon, i.drag = nil, nil

	var surface *gdk.Surface
	if s := gdk.BaseDrag(drag).DragSurface(); s != nil {
		surface = gdk.BaseSurface(s)
	}
	var (
		done    bool
		mapped  coreglib.SignalHandle
		timeout glib.SourceHandle
	)
	finish := func() {
		if done {
			return
		}
		done = true
		if mapped != 0 {
			coreglib.BaseObject(surface).HandlerDisconnect(mapped)
		}
		if timeout != 0 {
			glib.SourceRemove(timeout)
		}
		if icon.Realized() {
			icon.Unrealize()
		}
		// Until here the drag, and with it its surface, stays alive.
		runtime.KeepAlive(drag)
	}
	if surface == nil || !surface.Mapped() {
		finish()
		return
	}
	mapped = coreglib.BaseObject(surface).NotifyProperty("mapped", func() {
		if !surface.Mapped() {
			finish()
		}
	})
	timeout = glib.TimeoutAdd(uint(returnTime/time.Millisecond), func() bool {
		timeout = 0
		finish()
		return false
	})
}
