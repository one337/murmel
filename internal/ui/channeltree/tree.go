package channeltree

import (
	"context"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/ui/admin"
)

// Tree is the sidebar channel/user hierarchy widget. It subscribes to bus
// events, maintains an incremental treeModel, and displays the hierarchy in a
// gtk.ListBox (one row per visible entry in depth-first order).
//
// When the user activates a channel row (double-click or Enter) it emits
// bus.ChannelJoinRequested. Row selection emits bus.ChatTargetChanged.
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
	// no menu is shown).
	menuRefresh func()

	// unread counts of private conversations (by session) and of the
	// channel chat, kept across row rebuilds.
	unread        map[uint32]int
	channelUnread int

	// Decoded avatars by session, and the avatars already requested from
	// the server (both reset on disconnect).
	avatars          map[uint32]avatarEntry
	textureRequested map[uint32]bool
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
	t.list.SetSelectionMode(gtk.SelectionSingle)
	t.list.AddCSSClass("navigation-sidebar")
	// Single click selects the chat target; double-click (or Enter) joins.
	t.list.SetActivateOnSingleClick(false)
	placeholder := gtk.NewLabel(tr("Not connected"))
	placeholder.AddCSSClass("dim-label")
	t.list.SetPlaceholder(placeholder)
	t.list.ConnectRowActivated(func(row *gtk.ListBoxRow) { t.onActivate(row) })
	t.list.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if row != nil {
			t.onSelect(row)
		}
	})

	t.root = gtk.NewScrolledWindow()
	t.root.SetChild(t.list)
	t.root.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	t.root.SetVExpand(true)

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
	bus.SubscribeCtx(ctx, t.b, func(bus.Disconnected) {
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
		setUnread(r.badge, t.unread[session])
	}
	self := t.model.users[t.model.self].Channel
	for id, r := range t.channelRows {
		n := 0
		if id == self && t.model.self != 0 {
			n = t.channelUnread
		}
		setUnread(r.badge, n)
	}
}

// handle folds one event into the model: if the model signals a structural
// change (insert/remove/move) the entire list is rebuilt; otherwise only the
// directly affected row widget is refreshed.
func (t *Tree) handle(ev bus.Event) {
	if t.model.apply(ev) {
		t.rebuild()
	} else {
		t.updateInPlace(ev)
	}
	if t.menuRefresh != nil {
		t.menuRefresh()
	}
}

// rebuild clears the list and reconstructs it from the model walk order.
func (t *Tree) rebuild() {
	for _, r := range t.channelRows {
		t.list.Remove(r.root)
	}
	for _, r := range t.userRows {
		t.list.Remove(r.root)
	}
	t.channelRows = map[uint32]*channelRowWidgets{}
	t.userRows = map[uint32]*userRowWidgets{}

	for _, e := range t.model.walk() {
		switch e.kind {
		case entryChannel:
			id := e.id
			r := newChannelRow(t.model, id,
				func() { t.toggleExpand(id) },
				func(x, y float64) { t.showChannelMenu(id, x, y) })
			t.channelRows[id] = r
			t.list.Append(r.root)
		case entryUser:
			session := e.id
			r := newUserRow(t.model, session,
				func(x, y float64) { t.showUserMenu(session, x, y) },
				func() { t.b.Emit(bus.ChatTargetChanged{Session: session}) })
			t.userRows[session] = r
			t.applyAvatar(r, t.model.users[session])
			t.list.Append(r.root)
		}
	}
	t.applyUnread()
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
	}
}

func (t *Tree) toggleExpand(id uint32) {
	n, ok := t.model.channels[id]
	if !ok {
		return
	}
	n.expanded = !n.expanded
	t.rebuild()
}

func (t *Tree) showChannelMenu(id uint32, x, y float64) {
	r, ok := t.channelRows[id]
	if !ok {
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
		EditACL: func(chID uint32) {
			if n, ok := t.model.channels[chID]; ok {
				admin.ShowChannelEditor(t.parent, t.b, n.info, admin.PagePermissions)
			}
		},
	}
	menu, refresh := buildChannelMenu(t.model, id, a)
	t.openMenu(menu, refresh, permissionChannels(t.model, id, 0))
	popupAt(r.root, menu, x, y)
}

func (t *Tree) showUserMenu(session uint32, x, y float64) {
	r, ok := t.userRows[session]
	if !ok {
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

		Message: func() { t.b.Emit(bus.ChatTargetChanged{Session: session}) },
		Move:    func() { t.b.Emit(bus.UserMoveRequested{Session: session}) },
		Mute:    func() { t.b.Emit(bus.UserMuteRequested{Session: session, Muted: !isMuted}) },
		Deafen:  func() { t.b.Emit(bus.UserDeafenRequested{Session: session, Deafened: !isDeafened}) },
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
	menu, refresh := buildUserMenu(t.model, session, a)
	t.openMenu(menu, refresh, permissionChannels(t.model, 0, session))
	popupAt(r.root, menu, x, y)
}

// openMenu tracks the shown context menu so permission answers arriving
// while it is open update its entries, and asks the server for the
// permissions it still lacks.
func (t *Tree) openMenu(menu *gtk.PopoverMenu, refresh func(), channels []uint32) {
	t.menuRefresh = refresh
	menu.ConnectClosed(func() { t.menuRefresh = nil })
	for _, id := range channels {
		if _, known := t.model.channelPerm(id); !known {
			t.b.Emit(bus.ChannelPermissionRequested{ChannelID: id})
		}
	}
}

// sameRow reports whether a and b wrap the same GtkListBoxRow. Signal
// callbacks hand over a fresh Go wrapper for the row, so comparing the Go
// pointers never matches; the underlying GObject pointer does.
func sameRow(a, b *gtk.ListBoxRow) bool {
	if a == nil || b == nil {
		return false
	}
	return coreglib.InternObject(a).Native() == coreglib.InternObject(b).Native()
}

// onActivate fires when the user double-clicks or presses Enter on a row.
// Channel rows request a join; user rows do nothing (T18 adds actions).
func (t *Tree) onActivate(row *gtk.ListBoxRow) {
	for id, r := range t.channelRows {
		if sameRow(r.root, row) {
			t.b.Emit(bus.ChannelJoinRequested{ChannelID: id})
			return
		}
	}
}

// onSelect fires on a row selection change and emits ChatTargetChanged so
// the chat view follows the sidebar selection.
func (t *Tree) onSelect(row *gtk.ListBoxRow) {
	for id, r := range t.channelRows {
		if sameRow(r.root, row) {
			t.b.Emit(bus.ChatTargetChanged{ChannelID: id})
			return
		}
	}
	for session, r := range t.userRows {
		if sameRow(r.root, row) {
			t.b.Emit(bus.ChatTargetChanged{Session: session})
			return
		}
	}
}
