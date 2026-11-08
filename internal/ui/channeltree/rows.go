package channeltree

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/protocol/gumble"
)

const (
	iconExpanderCollapsed = "pan-end-symbolic"
	iconExpanderExpanded  = "pan-down-symbolic"
	iconMuted             = "microphone-disabled-symbolic"
	iconDeafened          = "audio-volume-muted-symbolic"
	iconSpeaking          = "audio-input-microphone-symbolic"
	iconPrioritySpeaker   = "starred-symbolic"
	iconRecording         = "media-record-symbolic"
	iconCurrentChannel    = "mark-location-symbolic"

	avatarSizePx     = 32
	indentPerDepthPx = 16
)

type channelRowWidgets struct {
	root    *gtk.ListBoxRow
	chevron *gtk.Button
	here    *gtk.Image
	name    *gtk.Label
	count   *gtk.Label
}

type userRowWidgets struct {
	root   *gtk.ListBoxRow
	avatar *adw.Avatar
	name   *gtk.Label
	icons  map[string]*gtk.Image
}

func newChannelRow(m *treeModel, id uint32, toggle func(), menu func(x, y float64)) *channelRowWidgets {
	n := m.channels[id]
	r := &channelRowWidgets{}

	r.chevron = gtk.NewButtonFromIconName(iconExpanderExpanded)
	r.chevron.AddCSSClass("flat")
	r.chevron.SetHasFrame(false)
	r.chevron.SetTooltipText(tr("Expand or collapse"))
	r.chevron.ConnectClicked(toggle)

	r.name = gtk.NewLabel(n.info.Name)
	r.name.SetXAlign(0.0)
	r.name.SetHExpand(true)
	r.name.SetEllipsize(pango.EllipsizeEnd)

	r.count = gtk.NewLabel("")
	r.count.AddCSSClass("dim-label")

	// "You are here" marker for the channel the local user is in.
	r.here = gtk.NewImageFromIconName(iconCurrentChannel)
	r.here.SetTooltipText(tr("You are in this channel"))

	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.Append(r.chevron)
	box.Append(r.name)
	box.Append(r.here)
	box.Append(r.count)
	applyIndent(box, depthOf(m, id))

	r.root = gtk.NewListBoxRow()
	r.root.SetChild(box)
	r.root.SetActivatable(true)
	r.root.SetSelectable(true)
	r.root.SetTooltipText(tr("Double-click to join"))
	current := id == m.selfChannel() && m.hasSelf()
	r.here.SetVisible(current)
	if current {
		r.root.AddCSSClass("current-channel")
		r.root.SetTooltipText(tr("You are in this channel"))
	}
	if menu != nil {
		rightClick(r.root, menu)
	}
	r.update(n)
	return r
}

func newUserRow(m *treeModel, session uint32, menu func(x, y float64)) *userRowWidgets {
	u := m.users[session]
	r := &userRowWidgets{icons: map[string]*gtk.Image{}}

	r.avatar = adw.NewAvatar(avatarSizePx, u.Name, true)
	r.name = gtk.NewLabel(u.Name)
	r.name.SetXAlign(0.0)
	r.name.SetHExpand(true)
	r.name.SetEllipsize(pango.EllipsizeEnd)

	for _, ic := range []struct {
		name string
		on   func(bus.UserInfo) bool
	}{
		{iconMuted, func(u bus.UserInfo) bool { return u.Muted || u.SelfMuted || u.Suppressed }},
		{iconDeafened, func(u bus.UserInfo) bool { return u.Deafened || u.SelfDeafened }},
		{iconPrioritySpeaker, func(u bus.UserInfo) bool { return u.PrioritySpeaker }},
		{iconRecording, func(u bus.UserInfo) bool { return u.Recording }},
	} {
		img := gtk.NewImageFromIconName(ic.name)
		img.SetVisible(ic.on(u))
		r.icons[ic.name] = img
	}
	speak := gtk.NewImageFromIconName(iconSpeaking)
	speak.SetVisible(false)
	r.icons[iconSpeaking] = speak

	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.Append(r.avatar)
	box.Append(r.name)
	for _, k := range []string{iconSpeaking, iconPrioritySpeaker, iconRecording, iconMuted, iconDeafened} {
		box.Append(r.icons[k])
	}
	applyIndent(box, depthOf(m, u.Channel)+1)

	r.root = gtk.NewListBoxRow()
	r.root.SetChild(box)
	r.root.SetActivatable(true)
	r.root.SetSelectable(true)
	if u.Session == m.self {
		r.root.AddCSSClass("self-user")
	}
	if menu != nil {
		rightClick(r.root, menu)
	}
	r.update(u, m.speaking[session])
	return r
}

func rightClick(row *gtk.ListBoxRow, menu func(x, y float64)) {
	g := gtk.NewGestureClick()
	g.SetButton(gdk.BUTTON_SECONDARY)
	g.ConnectPressed(func(_ int, x, y float64) { menu(x, y) })
	row.AddController(g)
}

func applyIndent(box *gtk.Box, depth int) {
	if depth > 0 {
		box.SetMarginStart(indentPerDepthPx * depth)
	}
}

func depthOf(m *treeModel, channelID uint32) int {
	d := 0
	// The bound guards against a (server-side) parent cycle.
	for !isRoot(channelID) && d < 64 {
		if _, ok := m.channels[channelID]; !ok {
			break
		}
		d++
		channelID = m.parentOf(channelID)
	}
	return d
}

func (r *channelRowWidgets) update(n *channelNode) {
	r.name.SetText(n.info.Name)
	hasKids := len(n.children) > 0 || len(n.users) > 0
	r.chevron.SetVisible(hasKids)
	if n.expanded {
		r.chevron.SetIconName(iconExpanderExpanded)
	} else {
		r.chevron.SetIconName(iconExpanderCollapsed)
	}
	if len(n.users) > 0 {
		r.count.SetText(fmt.Sprintf("%d", len(n.users)))
		r.count.SetVisible(true)
	} else {
		r.count.SetVisible(false)
	}
}

func (r *userRowWidgets) update(u bus.UserInfo, speaking bool) {
	r.name.SetText(u.Name)
	r.avatar.SetText(u.Name)
	r.icons[iconMuted].SetVisible(u.Muted || u.SelfMuted || u.Suppressed)
	r.icons[iconDeafened].SetVisible(u.Deafened || u.SelfDeafened)
	r.icons[iconPrioritySpeaker].SetVisible(u.PrioritySpeaker)
	r.icons[iconRecording].SetVisible(u.Recording)
	r.icons[iconSpeaking].SetVisible(speaking)
	setBold(r.name, speaking)
}

func setBold(lbl *gtk.Label, bold bool) {
	attr := pango.NewAttrList()
	if bold {
		attr.Insert(pango.NewAttrWeight(pango.WeightBold))
	}
	lbl.SetAttributes(attr)
}

// ChannelMenuActions bundles the action callbacks used by buildChannelMenu.
// Nil callbacks cause the corresponding menu item to remain insensitive.
type ChannelMenuActions struct {
	Join      func(channelID uint32)
	CreateSub func(channelID uint32)
	Edit      func(channelID uint32)
	Delete    func(channelID uint32)
	EditACL   func(channelID uint32)
}

// buildChannelMenu assembles the channel context menu. Nil callbacks leave
// items insensitive; permission checks further gate admin actions.
func buildChannelMenu(m *treeModel, id uint32, a ChannelMenuActions) *gtk.PopoverMenu {
	n := m.channels[id]
	group := gio.NewSimpleActionGroup()

	addChan := func(name string, perm gumble.Permission, cb func(uint32)) {
		act := gio.NewSimpleAction(name, nil)
		if cb != nil && permAllowed(n.info, perm) {
			act.ConnectActivate(func(*glib.Variant) { cb(id) })
			act.SetEnabled(true)
		} else {
			act.SetEnabled(false)
		}
		group.AddAction(act)
	}

	joinAct := gio.NewSimpleAction("join", nil)
	if a.Join != nil {
		joinAct.ConnectActivate(func(*glib.Variant) { a.Join(id) })
	}
	joinAct.SetEnabled(a.Join != nil && canJoin(n.info))
	group.AddAction(joinAct)

	addChan("create-sub", gumble.PermissionMakeChannel, a.CreateSub)
	addChan("edit", gumble.PermissionWrite, a.Edit)
	addChan("remove", gumble.PermissionWrite, a.Delete)
	addChan("edit-acl", gumble.PermissionWrite, a.EditACL)

	// Link channel is a future feature; always insensitive for now.
	linkAct := gio.NewSimpleAction("link", nil)
	linkAct.SetEnabled(false)
	group.AddAction(linkAct)

	menu := gio.NewMenu()
	menu.Append(tr("Join"), "ctx.join")
	menu.Append(tr("Create sub-channel…"), "ctx.create-sub")
	menu.Append(tr("Edit…"), "ctx.edit")
	menu.Append(tr("Remove…"), "ctx.remove")
	menu.Append(tr("Edit ACL…"), "ctx.edit-acl")
	menu.Append(tr("Link…"), "ctx.link")

	pm := gtk.NewPopoverMenuFromModel(menu)
	pm.InsertActionGroup("ctx", group)
	return pm
}

// permAllowed treats unknown permissions (nil) as allowed: unknown is not
// insufficient.
func permAllowed(ch bus.ChannelInfo, perm gumble.Permission) bool {
	if ch.Permissions == nil {
		return true
	}
	return gumble.Permission(*ch.Permissions).Has(perm)
}

func canJoin(ch bus.ChannelInfo) bool {
	return permAllowed(ch, gumble.PermissionEnter)
}

// UserMenuActions bundles the action callbacks used by buildUserMenu.
// Nil callbacks cause the corresponding menu item to remain insensitive.
type UserMenuActions struct {
	IsSelf     bool // true when session == self; suppresses server-action items
	IsMuted    bool // current server-mute state for the toggle label
	IsDeafened bool // current server-deafen state

	Message  func()
	Move     func()
	Volume   func()
	Mute     func()
	Deafen   func()
	Priority func()
	Kick     func()
	Ban      func()
	Register func()
	Stats    func()
}

// buildUserMenu assembles the user context menu wired to the provided action
// callbacks. Nil callbacks leave the corresponding item insensitive.
func buildUserMenu(a UserMenuActions) *gtk.PopoverMenu {
	group := gio.NewSimpleActionGroup()

	add := func(name string, cb func()) {
		act := gio.NewSimpleAction(name, nil)
		if cb != nil {
			act.ConnectActivate(func(*glib.Variant) { cb() })
			act.SetEnabled(true)
		} else {
			act.SetEnabled(false)
		}
		group.AddAction(act)
	}

	add("message", a.Message)
	// Server-admin actions are only relevant for other users.
	if a.IsSelf {
		for _, name := range []string{"move", "mute", "deafen", "priority", "kick", "ban", "register", "stats"} {
			act := gio.NewSimpleAction(name, nil)
			act.SetEnabled(false)
			group.AddAction(act)
		}
	} else {
		add("move", a.Move)
		add("mute", a.Mute)
		add("deafen", a.Deafen)
		add("priority", a.Priority)
		add("kick", a.Kick)
		add("ban", a.Ban)
		add("register", a.Register)
		add("stats", a.Stats)
	}
	add("volume", a.Volume)

	muteLabel := tr("Mute on server")
	if a.IsMuted {
		muteLabel = tr("Unmute on server")
	}
	deafenLabel := tr("Deafen on server")
	if a.IsDeafened {
		deafenLabel = tr("Undeafen on server")
	}

	menu := gio.NewMenu()
	menu.Append(tr("Send message…"), "ctx.message")
	menu.Append(tr("Move to my channel"), "ctx.move")
	menu.Append(tr("Local volume…"), "ctx.volume")
	menu.Append(muteLabel, "ctx.mute")
	menu.Append(deafenLabel, "ctx.deafen")
	menu.Append(tr("Priority speaker"), "ctx.priority")
	menu.Append(tr("Kick…"), "ctx.kick")
	menu.Append(tr("Ban…"), "ctx.ban")
	menu.Append(tr("Register"), "ctx.register")
	menu.Append(tr("View stats"), "ctx.stats")
	pm := gtk.NewPopoverMenuFromModel(menu)
	pm.InsertActionGroup("ctx", group)
	return pm
}

func popupAt(row gtk.Widgetter, menu *gtk.PopoverMenu, x, y float64) {
	menu.SetParent(row)
	rect := gdk.NewRectangle(int(x), int(y), 1, 1)
	menu.SetPointingTo(&rect)
	menu.Popup()
}
