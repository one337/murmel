package channeltree

import (
	"strconv"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// Users are moved into other channels by dragging the handle that appears
// left of their avatar on hover (or on the selected row, for touch). Only
// the handle starts a drag: a click on the name still opens the private
// conversation. Which drags and drops are offered follows the server's
// rules, see dropTarget.

const (
	// expandOnHoverMs opens a collapsed channel held under a drag.
	expandOnHoverMs = 700
	// The band at the top and bottom of the tree in which a drag scrolls
	// it, and the distance per frame at the very edge.
	treeAutoScrollEdgePx = 40
	treeAutoScrollStepPx = 12
	// noExpand marks drop rows that do not expand (user rows).
	noExpand = ^uint32(0)
)

// treeDrag is the state of one drag.
type treeDrag struct {
	session        uint32
	row            *userRowWidgets
	pendingRebuild bool // a structural change waits for the drop

	hover      uint32 // channel row highlighted as the target
	hovering   bool
	expandID   uint32
	expandSrc  glib.SourceHandle
	expandWait bool

	scrollSpeed float64
	ticking     bool
}

// askPermissions asks the server for the local user's permissions in the
// channels whose permissions are still unknown (once per connection).
func (t *Tree) askPermissions(ids ...uint32) {
	for _, id := range ids {
		if _, known := t.model.channelPerm(id); known || t.permAsked[id] {
			continue
		}
		if t.permAsked == nil {
			t.permAsked = map[uint32]bool{}
		}
		t.permAsked[id] = true
		t.b.Emit(bus.ChannelPermissionRequested{ChannelID: id})
	}
}

// refreshDragHandles offers the handle on the users that can be moved.
func (t *Tree) refreshDragHandles() {
	for session, r := range t.userRows {
		movable := canDragUser(t.model, session)
		if movable {
			r.handle.AddCSSClass("movable")
			r.handle.SetCursorFromName("grab")
		} else {
			r.handle.RemoveCSSClass("movable")
			r.handle.SetCursor(nil)
		}
		r.handle.SetCanTarget(movable)
	}
}

// dragUser makes r's handle drag the user, and asks for the permissions
// the handle depends on when the pointer comes near.
func (t *Tree) dragUser(r *userRowWidgets, session uint32) {
	hover := gtk.NewEventControllerMotion()
	hover.ConnectEnter(func(float64, float64) {
		if u, ok := t.model.users[session]; ok {
			t.askPermissions(u.Channel)
		}
	})
	r.root.AddController(hover)

	src := gtk.NewDragSource()
	src.SetActions(gdk.ActionMove)
	src.ConnectPrepare(func(float64, float64) *gdk.ContentProvider {
		if t.drag != nil || !canDragUser(t.model, session) {
			return nil
		}
		t.drag = &treeDrag{session: session, row: r}
		// The targets' permissions decide how they are highlighted.
		ids := make([]uint32, 0, len(t.model.channels))
		for id := range t.model.channels {
			ids = append(ids, id)
		}
		t.askPermissions(ids...)
		return gdk.NewContentProviderForValue(coreglib.NewValue(strconv.FormatUint(uint64(session), 10)))
	})
	src.ConnectDragBegin(func(drag gdk.Dragger) {
		u := t.model.users[session]
		icon := gtk.NewBox(gtk.OrientationHorizontal, 8)
		icon.AddCSSClass("user-drag-icon")
		av := adw.NewAvatar(avatarSizePx, u.Name, true)
		if img := r.avatar.CustomImage(); img != nil {
			av.SetCustomImage(img)
		}
		icon.Append(av)
		icon.Append(gtk.NewLabel(u.Name))
		gtk.NewDragIconGetForDrag(drag).SetChild(icon)
		gdk.BaseDrag(drag).SetHotspot(-8, avatarSizePx/2)
		r.root.AddCSSClass("dragged")
		t.setRowTooltips(false) // "Double-click to join" would cover the targets

	})
	src.ConnectDragEnd(func(gdk.Dragger, bool) { t.endDrag() })
	r.handle.AddController(src)
}

// endDrag clears the drag state and catches up on deferred rebuilds.
func (t *Tree) endDrag() {
	d := t.drag
	if d == nil {
		return
	}
	t.drag = nil
	d.row.root.RemoveCSSClass("dragged")
	t.setRowTooltips(true)
	t.setDropHover(d, 0, dropNo)
	t.cancelExpand(d)
	if d.pendingRebuild {
		t.rebuild()
	}
}

func (t *Tree) setRowTooltips(on bool) {
	for _, r := range t.channelRows {
		r.root.SetHasTooltip(on)
	}
	for _, r := range t.userRows {
		r.root.SetHasTooltip(on)
	}
}

// acceptDrops lets row take a dragged user. channelOf names the channel a
// drop on the row means (a user row: that user's channel); expandID is the
// row's own channel, opened when held there, or noExpand.
func (t *Tree) acceptDrops(row *gtk.ListBoxRow, channelOf func() (uint32, bool), expandID uint32) {
	target := gtk.NewDropTarget(coreglib.TypeString, gdk.ActionMove)
	judge := func() gdk.DragAction {
		d := t.drag
		if d == nil {
			return 0
		}
		ch, ok := channelOf()
		v := dropNo
		if ok {
			v = dropTarget(t.model, d.session, ch)
		}
		t.setDropHover(d, ch, v)
		t.scheduleExpand(d, expandID)
		if v == dropNo {
			return 0
		}
		return gdk.ActionMove
	}
	target.ConnectEnter(func(float64, float64) gdk.DragAction { return judge() })
	target.ConnectMotion(func(float64, float64) gdk.DragAction { return judge() })
	target.ConnectLeave(func() {
		if d := t.drag; d != nil {
			t.setDropHover(d, 0, dropNo)
			t.cancelExpand(d)
		}
	})
	target.ConnectDrop(func(*coreglib.Value, float64, float64) bool {
		d := t.drag
		if d == nil {
			return false
		}
		ch, ok := channelOf()
		if !ok || dropTarget(t.model, d.session, ch) == dropNo {
			return false
		}
		if d.session == t.model.self {
			// Like a double click on the channel (asks for a password
			// where one is needed).
			t.b.Emit(bus.ChannelJoinRequested{ChannelID: ch})
		} else {
			t.b.Emit(bus.UserMoveRequested{Session: d.session, ChannelID: ch, ToChannel: true})
		}
		return true
	})
	row.AddController(target)
}

// setDropHover highlights channel ch as the drop target: strongly where
// the move is sure, faintly where the server decides.
func (t *Tree) setDropHover(d *treeDrag, ch uint32, v dropVerdict) {
	if d.hovering {
		if r, ok := t.channelRows[d.hover]; ok {
			r.root.RemoveCSSClass("drop-target")
			r.root.RemoveCSSClass("drop-target-maybe")
		}
		d.hovering = false
	}
	if v == dropNo {
		return
	}
	if r, ok := t.channelRows[ch]; ok {
		if v == dropYes {
			r.root.AddCSSClass("drop-target")
		} else {
			r.root.AddCSSClass("drop-target-maybe")
		}
		d.hover, d.hovering = ch, true
	}
}

// refreshDropHover re-judges the highlighted target, e.g. when its
// permissions arrive during the drag.
func (t *Tree) refreshDropHover() {
	if d := t.drag; d != nil && d.hovering {
		t.setDropHover(d, d.hover, dropTarget(t.model, d.session, d.hover))
	}
}

// scheduleExpand opens the collapsed channel id after the drag rested on
// it for a moment.
func (t *Tree) scheduleExpand(d *treeDrag, id uint32) {
	if d.expandWait && d.expandID == id {
		return
	}
	t.cancelExpand(d)
	n, ok := t.model.channels[id]
	if id == noExpand || !ok || n.expanded || (len(n.children) == 0 && len(n.users) == 0) {
		return
	}
	d.expandID, d.expandWait = id, true
	d.expandSrc = glib.TimeoutAdd(expandOnHoverMs, func() bool {
		d.expandWait = false
		if t.drag != d {
			return false
		}
		if n, ok := t.model.channels[id]; ok && !n.expanded {
			n.expanded = true
			if r, ok := t.channelRows[id]; ok {
				r.update(n)
			}
			t.insertMissingRows()
		}
		return false
	})
}

func (t *Tree) cancelExpand(d *treeDrag) {
	if d.expandWait {
		glib.SourceRemove(d.expandSrc)
		d.expandWait = false
	}
}

// insertMissingRows adds the rows a channel opened during a drag shows,
// without rebuilding the rows that exist (the dragged one among them).
func (t *Tree) insertMissingRows() {
	for i, e := range t.model.walk() {
		if e.kind == entryChannel {
			if _, ok := t.channelRows[e.id]; ok {
				continue
			}
		} else if _, ok := t.userRows[e.id]; ok {
			continue
		}
		t.list.Insert(t.newRowFor(e), i)
	}
	t.refreshDragHandles()
	t.applyUnread()
}

// autoScrollOnDrag scrolls the tree while a user is dragged near its top
// or bottom edge, faster the closer to the edge.
func (t *Tree) autoScrollOnDrag() {
	motion := gtk.NewDropControllerMotion()
	motion.ConnectMotion(func(_, y float64) {
		d := t.drag
		if d == nil {
			return
		}
		h := float64(t.root.Height())
		switch {
		case y < treeAutoScrollEdgePx:
			d.scrollSpeed = -treeAutoScrollStepPx * (1 - y/treeAutoScrollEdgePx)
		case y > h-treeAutoScrollEdgePx:
			d.scrollSpeed = treeAutoScrollStepPx * (1 - (h-y)/treeAutoScrollEdgePx)
		default:
			d.scrollSpeed = 0
		}
		if d.scrollSpeed == 0 || d.ticking {
			return
		}
		d.ticking = true
		t.root.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
			if t.drag != d || d.scrollSpeed == 0 {
				d.ticking = false
				return false
			}
			adj := t.root.VAdjustment()
			adj.SetValue(min(max(adj.Value()+d.scrollSpeed, adj.Lower()), adj.Upper()-adj.PageSize()))
			return true
		})
	})
	motion.ConnectLeave(func() {
		if d := t.drag; d != nil {
			d.scrollSpeed = 0
		}
	})
	t.root.AddController(motion)
}
