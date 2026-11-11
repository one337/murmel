package connection

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/graphene"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/protocol/gumble"
	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui/avatarpick"
	"io.github/murmel/internal/ui/secrets"
)

// savedAutoScrollEdgePx is the band at the top and bottom of the saved
// list in which a dragged row scrolls the list; savedAutoScrollStepPx is
// the distance per frame at the very edge.
const (
	savedAutoScrollEdgePx = 40
	savedAutoScrollStepPx = 12
)

// savedList is the "Saved Connections" group of the connect dialog. Its
// order is the user's: rows are dragged by their handle or moved with the
// row menu, and every change is stored right away.
type savedList struct {
	c     *Controller
	fill  func(settings.ServerEntry)
	group *adw.PreferencesGroup
	list  *gtk.ListBox
	rows  []*savedRow // in list order, mirrors the setting

	dragging *savedRow // the row being dragged, nil otherwise
	scroll   *gtk.ScrolledWindow
	speed    float64 // auto-scroll per frame while dragging near an edge
	ticking  bool
}

type savedRow struct {
	entry  settings.ServerEntry
	row    *adw.ActionRow
	up     *gio.SimpleAction
	down   *gio.SimpleAction
	users  *gtk.Label // "5/100" from the server's ping answer
	ping   *gtk.Label // "12 ms", or "No answer"
	status *gtk.Box
}

// Servers are pinged (Mumble's UDP ping, which also reports the user
// count) while the dialog shows the list.
const (
	savedPingEvery    = 15 * time.Second
	savedPingTimeout  = 2 * time.Second
	savedPingInterval = 500 * time.Millisecond // resend within the timeout
)

// savedServersGroup lists the saved connections (nil when there are none).
// Activating a row calls fill.
func (c *Controller) savedServersGroup(fill func(settings.ServerEntry)) *adw.PreferencesGroup {
	servers := c.settings.Servers()
	if len(servers) == 0 {
		return nil
	}
	s := &savedList{c: c, fill: fill}
	s.group = adw.NewPreferencesGroup()
	s.group.SetTitle(tr("Saved Connections"))
	s.list = gtk.NewListBox()
	s.list.AddCSSClass("boxed-list")
	s.list.SetSelectionMode(gtk.SelectionNone)
	s.scroll = gtk.NewScrolledWindow()
	s.scroll.SetChild(s.list)
	s.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	s.scroll.SetPropagateNaturalHeight(true)
	s.scroll.SetMaxContentHeight(savedListMaxHeightPx)
	s.scroll.SetMinContentHeight(int(min(float64(len(servers)), savedListMinRows) * savedRowPx))
	s.scroll.AddCSSClass("saved-connections")
	s.scroll.SetOverflow(gtk.OverflowHidden) // clip rows to the rounded card
	// A permanent scrollbar and a fade over the last visible row show that
	// the list continues.
	s.scroll.SetOverlayScrolling(false)
	fade := gtk.NewBox(gtk.OrientationVertical, 0)
	fade.AddCSSClass("saved-connections-fade")
	fade.SetVAlign(gtk.AlignEnd)
	fade.SetCanTarget(false)
	adj := s.scroll.VAdjustment()
	updateFade := func() {
		fade.SetVisible(adj.Value()+adj.PageSize() < adj.Upper()-1)
	}
	adj.ConnectValueChanged(updateFade)
	adj.ConnectChanged(updateFade)
	overlay := gtk.NewOverlay()
	overlay.SetChild(s.scroll)
	overlay.AddOverlay(fade)
	s.group.Add(overlay)

	// A dragged row near the top or bottom edge scrolls the list.
	motion := gtk.NewDropControllerMotion()
	motion.ConnectMotion(func(_, y float64) { s.autoScroll(y) })
	motion.ConnectLeave(func() { s.speed = 0 })
	s.scroll.AddController(motion)

	for _, e := range servers {
		r := s.newRow(e)
		s.rows = append(s.rows, r)
		s.list.Append(r.row)
	}
	s.updateActions()

	// Ping while the list is on screen.
	var stop context.CancelFunc
	s.group.ConnectMap(func() {
		var ctx context.Context
		ctx, stop = context.WithCancel(context.Background())
		go s.pingLoop(ctx)
	})
	s.group.ConnectUnmap(func() {
		if stop != nil {
			stop()
		}
	})
	return s.group
}

// pingLoop pings every listed server now and then every savedPingEvery
// until ctx ends.
func (s *savedList) pingLoop(ctx context.Context) {
	for {
		rows := make(chan []*savedRow, 1)
		glib.IdleAdd(func() bool {
			rows <- append([]*savedRow(nil), s.rows...)
			return false
		})
		var list []*savedRow
		select {
		case list = <-rows:
		case <-ctx.Done():
			return
		}
		for _, r := range list {
			go s.pingRow(ctx, r)
		}
		select {
		case <-time.After(savedPingEvery):
		case <-ctx.Done():
			return
		}
	}
}

func (s *savedList) pingRow(ctx context.Context, r *savedRow) {
	addr := net.JoinHostPort(r.entry.Host, strconv.Itoa(r.entry.Port))
	resp, err := gumble.Ping(addr, savedPingInterval, savedPingTimeout)
	glib.IdleAdd(func() bool {
		if ctx.Err() == nil {
			r.showPing(resp, err)
		}
		return false
	})
}

// showPing shows a ping answer: users and round trip, or that the server
// did not answer.
func (r *savedRow) showPing(resp *gumble.PingResponse, err error) {
	r.status.SetVisible(true)
	if err != nil || resp == nil {
		r.users.SetVisible(false)
		r.ping.SetText(tr("No answer"))
		r.status.SetTooltipText(tr("The server did not answer. It may be offline, or it does not answer pings."))
		return
	}
	ms := max(int(resp.Ping.Round(time.Millisecond)/time.Millisecond), 1)
	r.users.SetVisible(true)
	r.users.SetText(fmt.Sprintf("%d/%d", resp.ConnectedUsers, resp.MaximumUsers))
	r.ping.SetText(fmt.Sprintf(tr("%d ms"), ms))
	major, minor, _ := resp.Version.SemanticVersion()
	r.status.SetTooltipText(fmt.Sprintf(tr("Mumble %d.%d · %d of %d users · %d ms"),
		major, minor, resp.ConnectedUsers, resp.MaximumUsers, ms))
}

func (s *savedList) newRow(e settings.ServerEntry) *savedRow {
	r := &savedRow{entry: e, row: adw.NewActionRow()}
	r.row.SetTitle(e.Name)
	r.row.SetTitleLines(1)
	r.row.SetSubtitle(net.JoinHostPort(e.Host, strconv.Itoa(e.Port)))
	r.row.SetActivatable(true)
	r.row.ConnectActivated(func() { s.fill(r.entry) })

	handle := gtk.NewImageFromIconName("list-drag-handle-symbolic")
	handle.AddCSSClass("drag-handle")
	handle.SetTooltipText(tr("Drag to reorder"))
	handle.SetCursorFromName("grab")
	r.row.AddPrefix(handle)
	s.dragSource(r, handle)
	s.dropTarget(r)

	r.users = gtk.NewLabel("")
	r.ping = gtk.NewLabel("")
	r.status = gtk.NewBox(gtk.OrientationVertical, 0)
	r.status.AddCSSClass("server-status")
	r.status.SetVAlign(gtk.AlignCenter)
	for _, l := range []*gtk.Label{r.users, r.ping} {
		l.AddCSSClass("caption")
		l.AddCSSClass("numeric")
		l.AddCSSClass("dim-label")
		l.SetXAlign(1)
		r.status.Append(l)
	}
	r.status.SetVisible(false) // until the first answer
	r.row.AddSuffix(r.status)

	actions := gio.NewSimpleActionGroup()
	r.up = gio.NewSimpleAction("up", nil)
	r.up.ConnectActivate(func(*glib.Variant) { s.move(r, s.index(r)-1) })
	r.down = gio.NewSimpleAction("down", nil)
	r.down.ConnectActivate(func(*glib.Variant) { s.move(r, s.index(r)+1) })
	forget := gio.NewSimpleAction("forget", nil)
	forget.ConnectActivate(func(*glib.Variant) { s.forget(r) })
	actions.AddAction(r.up)
	actions.AddAction(r.down)
	actions.AddAction(forget)
	r.row.InsertActionGroup("saved", actions)

	menu := gio.NewMenu()
	order := gio.NewMenu()
	order.Append(tr("Move Up"), "saved.up")
	order.Append(tr("Move Down"), "saved.down")
	menu.AppendSection("", order)
	remove := gio.NewMenu()
	remove.Append(tr("Forget Connection"), "saved.forget")
	menu.AppendSection("", remove)
	more := gtk.NewMenuButton()
	more.SetIconName("view-more-symbolic")
	more.AddCSSClass("flat")
	more.SetVAlign(gtk.AlignCenter)
	more.SetTooltipText(tr("More"))
	more.SetMenuModel(menu)
	r.row.AddSuffix(more)
	return r
}

// dragSource lets the handle drag its row.
func (s *savedList) dragSource(r *savedRow, handle *gtk.Image) {
	src := gtk.NewDragSource()
	src.SetActions(gdk.ActionMove)
	var hotX, hotY float64
	src.ConnectPrepare(func(x, y float64) *gdk.ContentProvider {
		hotX, hotY = x, y
		if p, ok := handle.ComputePoint(r.row, graphene.NewPointAlloc().Init(float32(x), float32(y))); ok {
			hotX, hotY = float64(p.X()), float64(p.Y())
		}
		s.dragging = r
		return gdk.NewContentProviderForValue(coreglib.NewValue(r.entry.Name))
	})
	src.ConnectDragBegin(func(drag gdk.Dragger) {
		// The icon is a copy of the row in a card of the row's width, as
		// GNOME Settings does; the row itself stays in place, dimmed.
		copyList := gtk.NewListBox()
		copyList.AddCSSClass("boxed-list")
		copyList.SetSizeRequest(r.row.Width(), r.row.Height())
		copyRow := adw.NewActionRow()
		copyRow.SetTitle(r.row.Title())
		copyRow.SetTitleLines(1)
		copyRow.SetSubtitle(r.row.Subtitle())
		copyRow.AddPrefix(gtk.NewImageFromIconName("list-drag-handle-symbolic"))
		copyList.Append(copyRow)
		icon := gtk.NewDragIconGetForDrag(drag)
		icon.SetChild(copyList)
		gdk.BaseDrag(drag).SetHotspot(int(hotX), int(hotY))
		r.row.AddCSSClass("dragged")
	})
	src.ConnectDragEnd(func(gdk.Dragger, bool) {
		r.row.RemoveCSSClass("dragged")
		s.dragging = nil
		s.speed = 0
	})
	handle.AddController(src)
}

// dropTarget makes r accept a dragged row: dropped on its upper half the
// dragged row goes above r, on its lower half below it. A line shows where.
func (s *savedList) dropTarget(r *savedRow) {
	target := gtk.NewDropTarget(coreglib.TypeString, gdk.ActionMove)
	mark := func(y float64) gdk.DragAction {
		r.row.RemoveCSSClass("drop-above")
		r.row.RemoveCSSClass("drop-below")
		if s.dragging == nil || s.dragging == r {
			return 0
		}
		if y < float64(r.row.Height())/2 {
			r.row.AddCSSClass("drop-above")
		} else {
			r.row.AddCSSClass("drop-below")
		}
		return gdk.ActionMove
	}
	target.ConnectEnter(func(_, y float64) gdk.DragAction { return mark(y) })
	target.ConnectMotion(func(_, y float64) gdk.DragAction { return mark(y) })
	target.ConnectLeave(func() {
		r.row.RemoveCSSClass("drop-above")
		r.row.RemoveCSSClass("drop-below")
	})
	target.ConnectDrop(func(_ *coreglib.Value, _, y float64) bool {
		r.row.RemoveCSSClass("drop-above")
		r.row.RemoveCSSClass("drop-below")
		src := s.dragging
		if src == nil || src == r {
			return false
		}
		from, pos := s.index(src), s.index(r)
		if y >= float64(r.row.Height())/2 {
			pos++ // below r
		}
		if from < pos {
			pos-- // the dragged row no longer counts above pos
		}
		s.move(src, pos)
		return true
	})
	r.row.AddController(target)
}

// autoScroll scrolls the list while a row is dragged near its top or
// bottom edge, faster the closer it gets.
func (s *savedList) autoScroll(y float64) {
	if s.dragging == nil {
		s.speed = 0
		return
	}
	h := float64(s.scroll.Height())
	switch {
	case y < savedAutoScrollEdgePx:
		s.speed = -savedAutoScrollStepPx * (1 - y/savedAutoScrollEdgePx)
	case y > h-savedAutoScrollEdgePx:
		s.speed = savedAutoScrollStepPx * (1 - (h-y)/savedAutoScrollEdgePx)
	default:
		s.speed = 0
	}
	if s.speed == 0 || s.ticking {
		return
	}
	s.ticking = true
	s.scroll.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		if s.speed == 0 || s.dragging == nil {
			s.ticking = false
			return false
		}
		adj := s.scroll.VAdjustment()
		adj.SetValue(min(max(adj.Value()+s.speed, adj.Lower()), adj.Upper()-adj.PageSize()))
		return true
	})
}

func (s *savedList) index(r *savedRow) int {
	for i, x := range s.rows {
		if x == r {
			return i
		}
	}
	return -1
}

// move puts r at index to (in the list and the setting).
func (s *savedList) move(r *savedRow, to int) {
	from := s.index(r)
	if from < 0 || to < 0 || to >= len(s.rows) || from == to {
		return
	}
	stored := s.c.settings.Servers()
	if i := serverIndex(stored, r.entry.Host, r.entry.Port, r.entry.Username); i >= 0 {
		// The setting may hold entries the dialog does not show yet (saved
		// meanwhile); move relative to the shown target row.
		target := s.rows[to].entry
		if j := serverIndex(stored, target.Host, target.Port, target.Username); j >= 0 {
			s.c.settings.SetServers(moveServer(stored, i, j))
		}
	}
	s.rows = moveRows(s.rows, from, to)
	s.list.Remove(r.row)
	s.list.Insert(r.row, to)
	s.updateActions()
}

func moveRows(rows []*savedRow, from, to int) []*savedRow {
	r := rows[from]
	out := append(rows[:from:from], rows[from+1:]...)
	out = append(out[:to:to], append([]*savedRow{r}, out[to:]...)...)
	return out
}

// forget removes the connection with its keyring password and avatar.
func (s *savedList) forget(r *savedRow) {
	e := r.entry
	s.c.settings.SetServers(removeServer(s.c.settings.Servers(), e.Host, e.Port, e.Username))
	if err := s.c.avatarStore().Save(avatarpick.AccountKey(e.Host, e.Port, e.Username), nil); err != nil {
		slog.Info("avatar not removed", "err", err)
	}
	if i := s.index(r); i >= 0 {
		s.rows = append(s.rows[:i:i], s.rows[i+1:]...)
	}
	s.list.Remove(r.row)
	if len(s.rows) == 0 {
		s.group.SetVisible(false)
	}
	s.updateActions()
	key := secrets.Key{Host: e.Host, Port: e.Port, Username: e.Username}
	go func() {
		if err := secrets.Delete(key); err != nil {
			slog.Info("keyring delete failed", "err", err)
		}
	}()
}

// updateActions disables "Move Up" on the first row and "Move Down" on
// the last.
func (s *savedList) updateActions() {
	for i, r := range s.rows {
		r.up.SetEnabled(i > 0)
		r.down.SetEnabled(i < len(s.rows)-1)
	}
}
