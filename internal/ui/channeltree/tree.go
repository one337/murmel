package channeltree

import (
	"context"
	"sort"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/ui/admin"
)

// Tree is the sidebar channel/user hierarchy widget. It subscribes to bus
// events, maintains an incremental treeModel, and displays the hierarchy in a
// gtk.ListBox (one row per visible entry in depth-first order).
//
// When the user activates a channel row (double-click or Enter) it emits
// bus.ChannelJoinRequested. A click on a row and activating a user row
// emit bus.ChatTargetChanged and mark that row (see markTarget); moving the
// keyboard focus marks nothing (see onKey).
// Construct on the main loop; ctx tears the subscriptions down.
type Tree struct {
	root *gtk.ScrolledWindow
	list *gtk.ListBox

	model       *treeModel
	channelRows map[uint32]*channelRowWidgets
	userRows    map[uint32]*userRowWidgets

	b      *bus.Bus
	parent gtk.Widgetter // dialog parent for admin actions

	// menuRefresh re-evaluates the open context menu's entries (nil when
	// no menu is shown); menu is that menu and whose it is.
	menuRefresh func()
	menu        *openMenu

	// unread counts of private conversations (by session) and of the
	// channel chat, kept across row rebuilds.
	unread        map[uint32]int
	channelUnread int

	// Decoded avatars by session, and the avatars already requested from
	// the server (both reset on disconnect).
	avatars          map[uint32]avatarEntry
	textureRequested map[uint32]bool

	// drag is the user being dragged into another channel (nil otherwise);
	// permAsked lists the channels whose permissions were asked for since
	// connecting.
	drag      *treeDrag
	permAsked map[uint32]bool

	// localAudio is the volume and mute set for oneself, by session.
	localAudio map[uint32]localAudio

	// rebuildPending marks a structural change the list does not show yet;
	// rebuildScheduled, that a batched rebuild is queued on the main loop.
	// lastRebuild is when the list was last rebuilt.
	rebuildPending   bool
	rebuildScheduled bool
	lastRebuild      time.Time

	// target is the row marked as the chat target: the row last clicked
	// or activated (hasTarget false if none, or if its row went).
	target    rowID
	hasTarget bool
	// lastFocus is the row that last had the keyboard focus (hasLastFocus
	// false if none since connecting): Tab and Shift+Tab enter the list
	// there (see setEntryRow).
	lastFocus    rowID
	hasLastFocus bool
	// focusWatch follows the window's focus while the list is realized.
	focusWatch coreglib.SignalHandle
	focusRoot  *gtk.Root
	// handlePressed is set while a mouse button is down on a drag handle.
	// A rebuild may take out the row under it and break the drag that may
	// follow, so it waits (like during a drag and while a menu is open).
	handlePressed bool
	// holdScroll keeps the list at heldScroll after a rebuild until the
	// new rows have a size (see keepHeldScroll).
	holdScroll bool
	heldScroll float64
	// anchor is waiting for the new rows' layout to put the anchor row
	// back where it was on screen (see anchorAfterLayout).
	anchor *pendingAnchor
	// revealSelf: one's own channel changed; the next rebuild scrolls
	// one's own row into view.
	revealSelf bool
	// replaceAll: connected or disconnected; the next rebuild replaces
	// every row.
	replaceAll bool
	// releasing counts the widgets of replaced rows not freed yet;
	// releaseRound, the garbage collections run for them, releaseIdle
	// those since the last one was freed (see release).
	releasing    int
	releaseGen   int
	releaseRound int
	releaseLeft  int
	releaseIdle  int
}

// openMenu is the shown context menu, of a channel or (user) of a user.
type openMenu struct {
	popover *gtk.PopoverMenu
	user    bool
	id      uint32
	// keyboard: opened with the Menu key or Shift+F10; the focus goes
	// back to the row when it closes.
	keyboard bool
}

// rebuildInterval is the least time between two batched rebuilds:
// structural changes that arrive closer together (20 users leaving at
// once, or one after another) are shown by one rebuild.
const rebuildInterval = 100 * time.Millisecond

// localAudio is one user's volume (0..2, 1 = 100 %) and mute for oneself.
type localAudio struct {
	volume float64
	muted  bool
}

func (t *Tree) localAudioOf(session uint32) localAudio {
	if a, ok := t.localAudio[session]; ok {
		return a
	}
	return localAudio{volume: 1}
}

// NewTree builds the tree widget and subscribes to the bus. parent is the
// application window used as the transient parent for admin dialogs.
func NewTree(ctx context.Context, b *bus.Bus, parent gtk.Widgetter) *Tree {
	t := &Tree{
		model:       newTreeModel(),
		channelRows: map[uint32]*channelRowWidgets{},
		userRows:    map[uint32]*userRowWidgets{},
		b:           b,
		parent:      parent,
	}

	t.list = gtk.NewListBox()
	// The list selects nothing itself: it would select every row the
	// keyboard focus moves to. The tree marks the chat target instead.
	t.list.SetSelectionMode(gtk.SelectionNone)
	t.list.AddCSSClass("navigation-sidebar")
	// A single click picks the chat target; double-click (or Enter) joins.
	t.list.SetActivateOnSingleClick(false)
	// The list is one stop for Tab and Shift+Tab: they leave it from any
	// row (the arrow keys move between rows). gotk4 has no setter for it.
	t.list.SetObjectProperty("tab-behavior", gtk.ListTabItem)
	t.list.ConnectRealize(t.watchFocus)
	t.list.ConnectUnrealize(t.unwatchFocus)
	placeholder := gtk.NewLabel(i18n.Tr("Not connected"))
	placeholder.AddCSSClass("dim-label")
	t.list.SetPlaceholder(placeholder)
	t.list.ConnectRowActivated(func(row *gtk.ListBoxRow) { t.onActivate(row) })
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(t.onKey)
	t.list.AddController(keys)

	t.root = gtk.NewScrolledWindow()
	t.root.SetChild(t.list)
	t.root.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	t.root.SetVExpand(true)
	t.autoScrollOnDrag()
	t.root.VAdjustment().ConnectValueChanged(t.keepHeldScroll)

	t.subscribe(ctx)
	return t
}

// Root returns the top-level widget for the window's sidebar slot.
func (t *Tree) Root() gtk.Widgetter { return t.root }

func (t *Tree) subscribe(ctx context.Context) {
	bus.SubscribeCtx(ctx, t.b, func(ev bus.Connected) { t.handle(ev) })
	bus.SubscribeCtx(ctx, t.b, func(ev bus.Disconnected) { t.handle(ev) })
	bus.SubscribeCtx(ctx, t.b, func(ev bus.ChannelAdded) { t.handle(ev) })
	bus.SubscribeCtx(ctx, t.b, func(ev bus.ChannelRemoved) { t.handle(ev) })
	bus.SubscribeCtx(ctx, t.b, func(ev bus.ChannelUpdated) { t.handle(ev) })
	bus.SubscribeCtx(ctx, t.b, func(ev bus.UserAdded) { t.handle(ev) })
	bus.SubscribeCtx(ctx, t.b, func(ev bus.UserRemoved) { t.handle(ev) })
	bus.SubscribeCtx(ctx, t.b, func(ev bus.UserUpdated) { t.handle(ev) })
	bus.SubscribeCtx(ctx, t.b, func(ev bus.VoiceActivity) { t.handle(ev) })
	bus.SubscribeCtx(ctx, t.b, func(bus.Connected) { t.permAsked = nil })
	bus.SubscribeCtx(ctx, t.b, func(ev bus.ChannelStepRequested) {
		if id, ok := channelStep(t.model, ev.Delta); ok {
			t.b.Emit(bus.ChannelJoinRequested{ChannelID: id})
		}
	})
	bus.SubscribeCtx(ctx, t.b, func(ev bus.UserLocalAudioChanged) {
		if t.localAudio == nil {
			t.localAudio = map[uint32]localAudio{}
		}
		a := localAudio{volume: ev.Volume, muted: ev.Muted}
		t.localAudio[ev.Session] = a
		if r, ok := t.userRows[ev.Session]; ok {
			r.setLocalAudio(a)
		}
	})
	bus.SubscribeCtx(ctx, t.b, func(bus.Disconnected) {
		t.hasLastFocus = false
		t.permAsked = nil
		t.localAudio = nil
		t.unread, t.channelUnread = nil, 0
		t.avatars, t.textureRequested = nil, nil
	})
	bus.SubscribeCtx(ctx, t.b, func(ev bus.UserRemoved) {
		delete(t.avatars, ev.Session)
		delete(t.textureRequested, ev.Session)
	})
	bus.SubscribeCtx(ctx, t.b, func(ev bus.UnreadChanged) {
		if ev.Session == 0 {
			t.channelUnread = ev.Count
		} else {
			if t.unread == nil {
				t.unread = map[uint32]int{}
			}
			t.unread[ev.Session] = ev.Count
		}
		t.applyUnread()
	})
}

// applyUnread shows the unread badges: per user for private messages, on
// the local user's channel for the channel chat.
func (t *Tree) applyUnread() {
	for session, r := range t.userRows {
		r.setUnread(t.unread[session])
	}
	self := t.model.users[t.model.self].Channel
	for id, r := range t.channelRows {
		n := 0
		if id == self && t.model.self != 0 {
			n = t.channelUnread
		}
		r.setUnread(n)
	}
}

// handle folds one event into the model: if the model signals a structural
// change (insert/remove/move) the entire list is rebuilt, batched with the
// other structural changes of the moment (see scheduleRebuild); otherwise
// only the directly affected row widget is refreshed.
func (t *Tree) handle(ev bus.Event) {
	had, from := t.model.hasSelf(), t.model.selfChannel()
	switch ev.(type) {
	case bus.Connected, bus.Disconnected:
		t.replaceAll = true
	}
	if t.model.apply(ev) {
		// One's own channel changed (one moved or was moved): the next
		// rebuild opens it and shows one's own row.
		if had && t.model.hasSelf() && t.model.selfChannel() != from {
			t.model.openTo(t.model.selfChannel())
			t.revealSelf = true
		}
		t.scheduleRebuild()
	} else {
		t.updateInPlace(ev)
	}
	if t.menuRefresh != nil {
		t.menuRefresh()
	}
	t.closeOrphanedMenu()
}

// closeOrphanedMenu closes the open context menu when the user or channel
// it is for is gone; the rebuild waiting for it then runs.
func (t *Tree) closeOrphanedMenu() {
	m := t.menu
	if m == nil {
		return
	}
	gone := false
	if m.user {
		_, ok := t.model.users[m.id]
		gone = !ok
	} else {
		_, ok := t.model.channels[m.id]
		gone = !ok
	}
	if gone {
		m.popover.Popdown()
	}
}

// scheduleRebuild queues one rebuild for all structural changes until it
// runs. It runs after the events already waiting on the main loop (the bus
// delivers each event in its own idle callback), and no sooner than
// rebuildInterval after the last rebuild. Until then the rows show the
// state before the change; whatever acts on them checks the model, and the
// context menus catch up first (flushRebuild).
func (t *Tree) scheduleRebuild() {
	t.rebuildPending = true
	if t.rebuildScheduled {
		return
	}
	t.rebuildScheduled = true
	run := func() bool {
		t.rebuildScheduled = false
		t.flushRebuild()
		return false
	}
	if wait := rebuildInterval - time.Since(t.lastRebuild); wait > 0 {
		glib.TimeoutAdd(uint(wait.Milliseconds())+1, run) //nolint:gosec // 0 < wait <= rebuildInterval
	} else {
		glib.IdleAdd(run)
	}
}

// flushRebuild shows a pending structural change now. A rebuild may take
// out or replace the row a drag, a press on a handle (which may start a
// drag) or an open context menu (attached to its row) depends on, so it
// waits until the drop, the release of the button or the menu closing.
func (t *Tree) flushRebuild() {
	if !t.rebuildPending || t.handlePressed || t.menu != nil {
		return
	}
	if t.drag != nil {
		t.drag.pendingRebuild = true
		t.rebuildPending = false
		return
	}
	t.rebuild()
}

// rebuild brings the rows in line with the model's walk order. Rows that
// stay keep their widgets, and with them the focus and what assistive
// technologies were told: a user coming or going elsewhere does not touch
// the focused row. Rows that went are removed, new ones are inserted at
// their place, and rows that moved are taken out and put back at their new
// place (all but the longest run that is still in order). After connecting
// or disconnecting every row is replaced: the same session or channel ID
// may then be someone or something else. Focus, chat target mark and what
// is on screen are kept as before (see restoreListState).
func (t *Tree) rebuild() {
	t.rebuildPending = false
	defer func() { t.lastRebuild = time.Now() }()
	state := t.saveListState()

	walk := t.model.walk()
	want := make(map[rowID]int, len(walk))
	for i, e := range walk {
		want[e.rowID()] = i
	}
	var kept []rowID
	var keptAt []int
	var gone []gtk.Widgetter
	for _, id := range t.listOrder() {
		if i, ok := want[id]; ok && !t.replaceAll {
			kept = append(kept, id)
			keptAt = append(keptAt, i)
			continue
		}
		gone = append(gone, t.removeRow(id))
	}
	if t.replaceAll {
		t.replaceAll = false
		t.release(gone)
	}
	inPlace := make(map[rowID]bool, len(kept))
	for k, stays := range inOrder(keptAt) {
		if stays {
			inPlace[kept[k]] = true
		} else {
			t.list.Remove(t.rowFor(kept[k]))
		}
	}
	// The rows still in the list are in walk order: each entry is either
	// the next of them or is inserted before it.
	for i, e := range walk {
		id := e.rowID()
		if !inPlace[id] {
			row := t.rowFor(id)
			if row == nil {
				t.list.Insert(t.newRowFor(e), i)
			} else {
				t.list.Insert(row, i)
			}
		}
		t.refreshRow(e)
	}
	t.refreshDragHandles()
	t.applyUnread()
	t.restoreListState(state)
}

// rowID names the row of a walk entry.
func (e entry) rowID() rowID { return rowID{user: e.kind != entryChannel, id: e.id} }

// listOrder lists the rows in the order the list shows them.
func (t *Tree) listOrder() []rowID {
	ids := make(map[uintptr]rowID, len(t.channelRows)+len(t.userRows))
	for id, r := range t.channelRows {
		ids[coreglib.BaseObject(r.root).Native()] = rowID{id: id}
	}
	for session, r := range t.userRows {
		ids[coreglib.BaseObject(r.root).Native()] = rowID{user: true, id: session}
	}
	order := make([]rowID, 0, len(ids))
	for c := t.list.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		if id, ok := ids[coreglib.BaseObject(c).Native()]; ok {
			order = append(order, id)
		}
	}
	return order
}

// removeRow takes a row out of the list and forgets it; it returns the row.
func (t *Tree) removeRow(id rowID) gtk.Widgetter {
	if id.user {
		r := t.userRows[id.id]
		t.list.Remove(r.root)
		delete(t.userRows, id.id)
		return r.handle
	}
	r := t.channelRows[id.id]
	t.list.Remove(r.root)
	delete(t.channelRows, id.id)
	return r.name
}

// refreshRow brings a row's indent and state in line with the model (a
// no-op where nothing changed). A kept row may have missed changes that
// came with a move, e.g. being suppressed in the new channel.
func (t *Tree) refreshRow(e entry) {
	if e.kind != entryChannel {
		r := t.userRows[e.id]
		// A user row is indented like its channel's row; the chevron's
		// invisible twin in it does the rest.
		r.setDepth(e.depth - 1)
		if u, ok := t.model.users[e.id]; ok {
			r.update(u, t.model.speaking[e.id])
			t.applyAvatar(r, u)
		}
		return
	}
	r := t.channelRows[e.id]
	r.setDepth(e.depth)
	r.setCurrent(e.id == t.model.selfChannel() && t.model.hasSelf())
	r.update(t.model.channels[e.id])
}

// inOrder marks the longest run of positions that is in increasing order
// (the rows that can stay where they are).
func inOrder(pos []int) []bool {
	// tails[k] is the index in pos ending the best run of length k+1.
	tails := []int{}
	prev := make([]int, len(pos))
	for i, p := range pos {
		k := sort.Search(len(tails), func(j int) bool { return pos[tails[j]] >= p })
		if k > 0 {
			prev[i] = tails[k-1]
		} else {
			prev[i] = -1
		}
		if k == len(tails) {
			tails = append(tails, i)
		} else {
			tails[k] = i
		}
	}
	out := make([]bool, len(pos))
	if len(tails) > 0 {
		for i := tails[len(tails)-1]; i >= 0; i = prev[i] {
			out[i] = true
		}
	}
	return out
}

// newRowFor builds the row for one walk entry and registers it.
func (t *Tree) newRowFor(e entry) gtk.Widgetter {
	switch e.kind {
	case entryChannel:
		id := e.id
		r := newChannelRow(t.model, id,
			func() { t.toggleExpand(id) },
			func(x, y float64) { t.showChannelMenu(id, x, y) },
			func() {
				// The row may outlive the channel until the pending rebuild.
				if _, ok := t.model.channels[id]; ok {
					t.markTarget(rowID{id: id})
					t.b.Emit(bus.ChatTargetChanged{ChannelID: id})
				}
			})
		t.channelRows[id] = r
		onFocusLeave(r.root, func() {
			if r, ok := t.channelRows[id]; ok {
				r.announce()
			}
		})
		t.acceptDrops(r.root, func() (uint32, bool) { return id, true }, id)
		return r.root
	default:
		session := e.id
		r := newUserRow(t.model, session,
			func(x, y float64) { t.showUserMenu(session, x, y) },
			func() {
				// The row may outlive the user until the pending rebuild.
				if _, ok := t.model.users[session]; ok {
					t.markTarget(rowID{user: true, id: session})
					t.b.Emit(bus.ChatTargetChanged{Session: session})
				}
			})
		t.userRows[session] = r
		onFocusLeave(r.root, func() {
			if r, ok := t.userRows[session]; ok {
				r.announce()
			}
		})
		t.applyAvatar(r, t.model.users[session])
		r.setLocalAudio(t.localAudioOf(session))
		t.dragUser(r, session)
		t.acceptDrops(r.root, func() (uint32, bool) {
			u, ok := t.model.users[session]
			return u.Channel, ok
		}, noExpand)
		return r.root
	}
}

// updateInPlace refreshes a single row without changing list structure.
func (t *Tree) updateInPlace(ev bus.Event) {
	switch e := ev.(type) {
	case bus.VoiceActivity:
		if r, ok := t.userRows[e.Session]; ok {
			if u, ok2 := t.model.users[e.Session]; ok2 {
				r.update(u, e.Active)
			}
		}
	case bus.UserUpdated:
		if r, ok := t.userRows[e.User.Session]; ok {
			r.update(e.User, t.model.speaking[e.User.Session])
			t.applyAvatar(r, e.User)
		}
	case bus.ChannelUpdated:
		if r, ok := t.channelRows[e.Channel.ID]; ok {
			if n, ok2 := t.model.channels[e.Channel.ID]; ok2 {
				r.update(n)
			}
		}
		// Permission answers arrive as channel updates.
		t.refreshDragHandles()
		t.refreshDropHover()
	}
}

func (t *Tree) toggleExpand(id uint32) {
	if n, ok := t.model.channels[id]; ok {
		t.setExpanded(id, !n.expanded)
	}
}

// setExpanded opens or closes a channel by adding or removing the rows
// below it; every other row stays as it is (focus, chat target mark,
// place on screen). While the rows do not match the model (a rebuild pending or
// held back by a menu, a drag or a pressed handle), the rebuild shows it.
func (t *Tree) setExpanded(id uint32, expanded bool) {
	n, ok := t.model.channels[id]
	if !ok || n.expanded == expanded {
		return
	}
	if t.rebuildPending || t.menu != nil || t.drag != nil || t.handlePressed {
		n.expanded = expanded
		t.scheduleRebuild()
		return
	}
	if expanded {
		n.expanded = true
		t.insertMissingRows()
	} else {
		t.removeRowsBelow(id)
		n.expanded = false
	}
	if r, ok := t.channelRows[id]; ok {
		r.update(n)
	}
	t.refreshDragHandles()
	t.applyUnread()
}

// removeRowsBelow removes the rows shown inside channel id (its users and
// sub-channels, as far as they are open). The focus, if it was on one of
// them, goes to the channel's row.
func (t *Tree) removeRowsBelow(id uint32) {
	var focused rowID
	hasFocus := false
	if root := t.list.Root(); root != nil {
		if row := focusedRow(root.Focus(), t.list); row != nil {
			focused, hasFocus = t.rowIDOf(row)
		}
	}
	lost := false
	for _, e := range t.model.below(id) {
		if e.kind == entryChannel {
			if r, ok := t.channelRows[e.id]; ok {
				t.list.Remove(r.root)
				delete(t.channelRows, e.id)
			}
		} else if r, ok := t.userRows[e.id]; ok {
			t.list.Remove(r.root)
			delete(t.userRows, e.id)
		}
		lost = lost || (hasFocus && focused == rowID{user: e.kind != entryChannel, id: e.id})
	}
	if r, ok := t.channelRows[id]; ok && lost {
		r.root.GrabFocus()
	}
}

// showChannelMenu opens a channel's menu at a point of its row (a right
// click or long press).
func (t *Tree) showChannelMenu(id uint32, x, y float64) {
	t.channelMenu(id, gdk.NewRectangle(int(x), int(y), 1, 1), false)
}

// channelMenu opens a channel's menu pointing at the area at of its row.
func (t *Tree) channelMenu(id uint32, at gdk.Rectangle, keyboard bool) {
	t.flushRebuild()
	r, ok := t.channelRows[id]
	if _, known := t.model.channels[id]; !ok || !known {
		return
	}
	name := ""
	if n, ok2 := t.model.channels[id]; ok2 {
		name = n.info.Name
	}
	a := ChannelMenuActions{
		Join: func(chID uint32) {
			t.b.Emit(bus.ChannelJoinRequested{ChannelID: chID})
		},
		CreateSub: func(chID uint32) {
			admin.ShowCreateSub(t.parent, t.b, chID)
		},
		Edit: func(chID uint32) {
			if n, ok := t.model.channels[chID]; ok {
				admin.ShowChannelEditor(t.parent, t.b, n.info, admin.PageGeneral)
			}
		},
		Delete: func(chID uint32) {
			admin.ShowDelete(t.parent, t.b, chID, name)
		},
	}
	menu, refresh := buildChannelMenu(t.model, id, a)
	t.openMenu(&openMenu{popover: menu, id: id, keyboard: keyboard}, refresh, permissionChannels(t.model, id, 0))
	popupAt(r.root, menu, at)
}

// showUserMenu opens a user's menu at a point of their row (a right click
// or long press).
func (t *Tree) showUserMenu(session uint32, x, y float64) {
	t.userMenu(session, gdk.NewRectangle(int(x), int(y), 1, 1), false)
}

// userMenu opens a user's menu pointing at the area at of their row.
func (t *Tree) userMenu(session uint32, at gdk.Rectangle, keyboard bool) {
	t.flushRebuild()
	r, ok := t.userRows[session]
	if _, known := t.model.users[session]; !ok || !known {
		return
	}
	u, hasUser := t.model.users[session]

	var isMuted, isDeafened bool
	if hasUser {
		isMuted = u.Muted
		isDeafened = u.Deafened
	}

	userName := ""
	if hasUser {
		userName = u.Name
	}

	a := UserMenuActions{
		IsMuted:    isMuted,
		IsDeafened: isDeafened,

		LocalAudio: t.localAudioOf(session),

		Message:    func() { t.b.Emit(bus.ChatTargetChanged{Session: session}) },
		Move:       func() { t.b.Emit(bus.UserMoveRequested{Session: session}) },
		Mute:       func() { t.b.Emit(bus.UserMuteRequested{Session: session, Muted: !isMuted}) },
		Unsuppress: func() { t.b.Emit(bus.UserUnsuppressRequested{Session: session}) },
		Deafen:     func() { t.b.Emit(bus.UserDeafenRequested{Session: session, Deafened: !isDeafened}) },
		Priority: func() {
			priority := true
			if hasUser {
				priority = !u.PrioritySpeaker
			}
			t.b.Emit(bus.UserPriorityRequested{Session: session, Priority: priority})
		},
		Kick:     func() { admin.ShowKick(t.parent, t.b, session, userName) },
		Ban:      func() { admin.ShowBan(t.parent, t.b, session, userName) },
		Register: func() { t.b.Emit(bus.UserRegisterRequested{Session: session}) },
		Stats:    func() { admin.ShowUserStats(t.parent, t.b, session, userName) },
	}
	if session != t.model.self {
		a.SetLocalAudio = func(la localAudio) {
			t.b.Emit(bus.UserLocalAudioRequested{Session: session, Volume: la.volume, Muted: la.muted})
		}
	}
	menu, refresh := buildUserMenu(t.model, session, a)
	t.openMenu(&openMenu{popover: menu, user: true, id: session, keyboard: keyboard}, refresh, permissionChannels(t.model, 0, session))
	popupAt(r.root, menu, at)
}

// onKey handles keys pressed while the focus is in the list, before the
// list itself (capture phase):
//
//   - The arrow keys, Page Up/Down, Home and End only move the cursor, as
//     Ctrl with them does: the chat target, and with it the conversation
//     shown, stays. Enter (or Space) opens the conversation or joins the
//     channel, a click does as before; Ctrl+Space does nothing.
//   - The Menu key and Shift+F10 open the context menu of the focused
//     row, pointing at the whole row.
//   - Right and Left open and close channels and move along the tree
//     (see sideways).
func (t *Tree) onKey(keyval, _ uint, state gdk.ModifierType) bool {
	mods := state & (gdk.ShiftMask | gdk.ControlMask | gdk.AltMask | gdk.SuperMask)
	switch {
	case keyval == gdk.KEY_Menu && mods == 0, keyval == gdk.KEY_F10 && mods == gdk.ShiftMask:
		return t.menuForFocused()
	case (keyval == gdk.KEY_Right || keyval == gdk.KEY_KP_Right) && mods == 0:
		return t.sideways(true)
	case (keyval == gdk.KEY_Left || keyval == gdk.KEY_KP_Left) && mods == 0:
		return t.sideways(false)
	case (keyval == gdk.KEY_space || keyval == gdk.KEY_KP_Space) && mods == gdk.ControlMask:
		// GTK would activate the row (join the channel): Ctrl+Space
		// does nothing here.
		return true
	case mods&^gdk.ShiftMask != 0:
		return false // Ctrl, Alt, Super: GTK's own meaning
	}
	step, count := gtk.MovementDisplayLines, 0
	switch keyval {
	case gdk.KEY_Up, gdk.KEY_KP_Up:
		count = -1
	case gdk.KEY_Down, gdk.KEY_KP_Down:
		count = 1
	case gdk.KEY_Page_Up, gdk.KEY_KP_Page_Up:
		step, count = gtk.MovementPages, -1
	case gdk.KEY_Page_Down, gdk.KEY_KP_Page_Down:
		step, count = gtk.MovementPages, 1
	case gdk.KEY_Home, gdk.KEY_KP_Home:
		step, count = gtk.MovementBufferEnds, -1
	case gdk.KEY_End, gdk.KEY_KP_End:
		step, count = gtk.MovementBufferEnds, 1
	default:
		return false
	}
	// extend false, modify true: move the cursor without selecting.
	coreglib.BaseObject(t.list).Emit("move-cursor", step, count, false, true)
	return true
}

// sideways handles Right and Left on the focused row, as tree views do:
//
//   - Right opens a closed channel; on an open one it moves the cursor to
//     the first row inside.
//   - Left closes an open channel; on a closed (or empty) channel it moves
//     the cursor to the parent channel, on a user to their channel.
//
// Otherwise (Right on an empty channel or a user, Left on the top
// channel) nothing happens. The chat target, and with it the
// conversation, stays.
func (t *Tree) sideways(right bool) bool {
	root := t.list.Root()
	if root == nil {
		return false
	}
	row := focusedRow(root.Focus(), t.list)
	if row == nil {
		return false
	}
	id, ok := t.rowIDOf(row)
	if !ok {
		return false
	}
	if id.user {
		if !right {
			if ch, ok := t.channelRows[t.model.users[id.id].Channel]; ok {
				ch.root.GrabFocus()
			}
		}
		return true
	}
	n, ok := t.model.channels[id.id]
	if !ok {
		return true
	}
	hasContent := len(n.children) > 0 || len(n.users) > 0
	switch {
	case right && hasContent && !n.expanded:
		t.setExpanded(id.id, true)
	case right && hasContent:
		if next := t.list.RowAtIndex(row.Index() + 1); next != nil {
			next.GrabFocus()
		}
	case !right && hasContent && n.expanded:
		t.setExpanded(id.id, false)
	case !right && !isRoot(id.id):
		if parent, ok := t.channelRows[n.info.ParentID]; ok {
			parent.root.GrabFocus()
		}
	}
	return true
}

// menuForFocused opens the context menu of the row that has the focus.
func (t *Tree) menuForFocused() bool {
	root := t.list.Root()
	if root == nil {
		return false
	}
	row := focusedRow(root.Focus(), t.list)
	if row == nil {
		return false
	}
	id, ok := t.rowIDOf(row)
	if !ok {
		return false
	}
	at := gdk.NewRectangle(0, 0, row.Width(), row.Height())
	if id.user {
		t.userMenu(id.id, at, true)
	} else {
		t.channelMenu(id.id, at, true)
	}
	return true
}

// openMenu tracks the shown context menu so permission answers arriving
// while it is open update its entries, and asks the server for the
// permissions it still lacks. Rebuilds wait while it is open.
func (t *Tree) openMenu(m *openMenu, refresh func(), channels []uint32) {
	t.menuRefresh = refresh
	t.menu = m
	m.popover.ConnectClosed(func() {
		if t.menu != m {
			return
		}
		t.menu, t.menuRefresh = nil, nil
		t.flushRebuild()
		if m.keyboard {
			t.focusAfterMenu(rowID{user: m.user, id: m.id})
		}
	})
	for _, id := range channels {
		if _, known := t.model.channelPerm(id); !known {
			t.b.Emit(bus.ChannelPermissionRequested{ChannelID: id})
		}
	}
}

// focusAfterMenu gives the focus back to the row of a menu opened with the
// keyboard, once the chosen entry has run: unless that entry moved the
// focus elsewhere (a dialog, the message composer). The row may have been
// replaced by the rebuild that waited for the menu.
func (t *Tree) focusAfterMenu(id rowID) {
	glib.IdleAdd(func() {
		root := t.list.Root()
		if root == nil {
			return
		}
		if f := root.Focus(); f != nil && focusedRow(f, t.list) == nil {
			return
		}
		if row := t.rowFor(id); row != nil {
			row.GrabFocus()
		}
	})
}

// sameRow reports whether a and b wrap the same GtkListBoxRow. Signal
// callbacks hand over a fresh Go wrapper for the row, so comparing the Go
// pointers never matches; the underlying GObject pointer does.
func sameRow(a, b *gtk.ListBoxRow) bool {
	if a == nil || b == nil {
		return false
	}
	return coreglib.BaseObject(a).Native() == coreglib.BaseObject(b).Native()
}

// onActivate fires when the user double-clicks or presses Enter (or
// Space) on a row. The row becomes the chat target; channel rows also
// request a join, user rows open the conversation, also when the row is
// the chat target already. The row may still show a channel or user that
// a pending rebuild removes: only those the model knows count.
func (t *Tree) onActivate(row *gtk.ListBoxRow) {
	for id, r := range t.channelRows {
		if sameRow(r.root, row) {
			if _, ok := t.model.channels[id]; ok {
				t.markTarget(rowID{id: id})
				t.b.Emit(bus.ChatTargetChanged{ChannelID: id})
				t.b.Emit(bus.ChannelJoinRequested{ChannelID: id})
			}
			return
		}
	}
	for session, r := range t.userRows {
		if sameRow(r.root, row) {
			if _, ok := t.model.users[session]; ok {
				t.markTarget(rowID{user: true, id: session})
				t.b.Emit(bus.ChatTargetChanged{Session: session})
			}
			return
		}
	}
}

// markTarget marks id's row as the chat target, as a selected row looks
// and as assistive technologies know a selected row, and unmarks the row
// marked before.
func (t *Tree) markTarget(id rowID) {
	if t.hasTarget && t.target != id {
		if row := t.rowFor(t.target); row != nil {
			showTarget(row, false)
		}
	}
	t.target, t.hasTarget = id, true
	if row := t.rowFor(id); row != nil {
		showTarget(row, true)
	}
}

// showTarget shows row as the chat target or as an ordinary row.
func showTarget(row *gtk.ListBoxRow, on bool) {
	if !on {
		row.UnsetStateFlags(gtk.StateFlagSelected)
		row.ResetState(gtk.AccessibleStateSelected)
		return
	}
	row.SetStateFlags(gtk.StateFlagSelected, false)
	// A "boolean or undefined" state: GTK reads it as an int (see
	// setExpandedState).
	row.UpdateState([]gtk.AccessibleState{gtk.AccessibleStateSelected},
		[]coreglib.Value{*coreglib.NewValue(int32(1))})
}
