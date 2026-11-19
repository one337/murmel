package channeltree

import (
	"fmt"
	"strconv"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/ui/weakref"
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
	iconDragHandle        = "list-drag-handle-symbolic"
	// Volume set for oneself (shipped in the GResource, see
	// data/icons/symbolic).
	iconLocalVolume = "user-volume-symbolic"
	iconLocalMuted  = "user-local-muted-symbolic"

	avatarSizePx     = 32
	indentPerDepthPx = 16
)

type channelRowWidgets struct {
	root    *gtk.ListBoxRow
	chevron *gtk.Button
	here    *gtk.Image
	name    *gtk.Label
	count   *gtk.Label
	badge   *gtk.Label

	users   int  // users in the channel
	current bool // one is in this channel oneself
	text    rowText

	// box holds the row's content and its indent; content wraps it (see
	// rowContent) and carries the tooltip.
	box     *gtk.Box
	content *gtk.Box
	depth   int
	// shown is what update last showed, so that an unchanged channel is
	// left alone.
	shown    channelShown
	hasShown bool
}

// channelShown is what a channel row shows of its channel.
type channelShown struct {
	name           string
	users          int
	kids, expanded bool
}

type userRowWidgets struct {
	root   *gtk.ListBoxRow
	handle *gtk.Image // drag handle, shown on hover when the user can be moved
	avatar *adw.Avatar
	name   *gtk.Label
	badge  *gtk.Label
	icons  map[string]*gtk.Image
	// localAudio shows a volume changed or muted for oneself.
	localAudio *gtk.Image

	user  bus.UserInfo
	local localAudio
	text  rowText

	box   *gtk.Box // the row's content, with its indent
	depth int
	// movable is whether the handle offers dragging (see
	// refreshDragHandles); movableSet once it was set.
	movable, movableSet bool
}

// rowText is what a row tells assistive technologies: its name and, as
// description, its state (see announce.go).
type rowText struct {
	name, desc string
	set        bool
	// unread is the current number of unread messages, shownUnread the
	// one in the description.
	unread, shownUnread int
}

// apply gives row its name and description where they changed. The
// unread count of the focused row stays as it is until the focus leaves:
// screen readers would read the description again on every new message.
func (a *rowText) apply(row *gtk.ListBoxRow, name string, desc func(unread int) string) {
	if !row.HasFocus() {
		a.shownUnread = a.unread
	}
	d := desc(a.shownUnread)
	var props []gtk.AccessibleProperty
	var vals []coreglib.Value
	if !a.set || name != a.name {
		props = append(props, gtk.AccessiblePropertyLabel)
		vals = append(vals, *coreglib.NewValue(name))
	}
	if !a.set || d != a.desc {
		props = append(props, gtk.AccessiblePropertyDescription)
		vals = append(vals, *coreglib.NewValue(d))
	}
	a.name, a.desc, a.set = name, d, true
	if len(props) > 0 {
		row.UpdateProperty(props, vals)
	}
}

// rowContent wraps a row's content. The wrapper's role keeps it and
// everything in it from assistive technologies, as the row itself names
// what they show; the role can only be given when the wrapper is created
// (GTK sets the "hidden" state of each widget itself whenever it is
// shown). A row's tooltip belongs on the wrapper: on the row, GTK would
// read it out where the row's description is empty.
func rowContent(content *gtk.Box) *gtk.Box {
	b := gtk.NewBuilderFromString(`<interface><object class="GtkBox" id="box">
<property name="accessible-role">none</property></object></interface>`)
	wrap := b.GetObject("box").Cast().(*gtk.Box)
	content.SetHExpand(true)
	wrap.Append(content)
	return wrap
}

// newBadge builds the unread-messages pill shown next to a name.
func newBadge() *gtk.Label {
	b := gtk.NewLabel("")
	b.AddCSSClass("unread-badge")
	b.AddCSSClass("numeric")
	b.SetVAlign(gtk.AlignCenter)
	b.SetVisible(false)
	return b
}

// showUnread shows n unread messages on the badge (hidden at 0).
func showUnread(b *gtk.Label, n int) {
	b.SetVisible(n > 0)
	switch {
	case n > 99:
		b.SetText("99+")
	case n > 0:
		b.SetText(strconv.Itoa(n))
	}
	if n > 0 {
		b.SetTooltipText(fmt.Sprintf(tr("Unread messages: %d"), n))
	}
}

// newChevron is the button that opens and closes a channel with the
// mouse; the keyboard uses Left and Right on the row instead.
func newChevron() *gtk.Button {
	b := gtk.NewButtonFromIconName(iconExpanderExpanded)
	b.AddCSSClass("flat")
	b.SetHasFrame(false)
	b.SetCanFocus(false)
	return b
}

// chevronSpacer takes exactly a chevron's room without showing or reacting.
func chevronSpacer() *gtk.Button {
	b := newChevron()
	b.SetOpacity(0)
	b.SetCanTarget(false)
	return b
}

func newChannelRow(m *treeModel, id uint32, toggle func(), menu func(x, y float64)) *channelRowWidgets {
	n := m.channels[id]
	r := &channelRowWidgets{}

	r.chevron = newChevron()
	r.chevron.SetTooltipText(tr("Expand or collapse"))
	r.chevron.ConnectClicked(toggle)

	r.name = gtk.NewLabel(n.info.Name)
	r.name.AddCSSClass("channel-name")
	r.name.SetXAlign(0.0)
	r.name.SetEllipsize(pango.EllipsizeEnd)

	r.count = gtk.NewLabel("")
	r.count.AddCSSClass("dim-label")
	r.badge = newBadge()

	// "You are here" marker for the channel the local user is in.
	r.here = gtk.NewImageFromIconName(iconCurrentChannel)
	r.here.AddCSSClass("here-icon")
	r.here.SetTooltipText(tr("You are in this channel"))

	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.Append(r.chevron)
	box.Append(r.name)
	box.Append(r.badge)
	box.Append(expander())
	box.Append(r.here)
	box.Append(r.count)
	r.box = box
	r.depth = -1
	r.setDepth(depthOf(m, id))

	r.content = rowContent(box)
	r.root = gtk.NewListBoxRow()
	r.root.SetChild(r.content)
	r.root.SetActivatable(true)
	r.root.SetSelectable(true)
	r.showCurrent(id == m.selfChannel() && m.hasSelf())
	if menu != nil {
		contextMenu(r.root, menu)
	}
	r.update(n)
	return r
}

func newUserRow(m *treeModel, session uint32, menu func(x, y float64), open func()) *userRowWidgets {
	u := m.users[session]
	r := &userRowWidgets{icons: map[string]*gtk.Image{}}

	r.avatar = adw.NewAvatar(avatarSizePx, u.Name, true)
	r.name = gtk.NewLabel(u.Name)
	r.name.AddCSSClass("user-name")
	r.name.SetXAlign(0.0)
	r.name.SetHExpand(false)
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

	r.badge = newBadge()
	r.localAudio = gtk.NewImageFromIconName(iconLocalMuted)
	r.localAudio.AddCSSClass("local-audio")
	r.localAudio.SetVisible(false)
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	// An invisible twin of the channel chevron: the avatar starts where
	// the channel's name does. The drag handle lies on top of it.
	slot := gtk.NewOverlay()
	slot.SetChild(chevronSpacer())
	r.handle = gtk.NewImageFromIconName(iconDragHandle)
	r.handle.AddCSSClass("user-drag-handle")
	r.handle.SetTooltipText(tr("Drag into another channel"))
	slot.AddOverlay(r.handle)
	box.Append(slot)
	box.Append(r.avatar)
	box.Append(r.name)
	box.Append(r.badge)
	box.Append(expander())
	box.Append(r.localAudio)
	for _, k := range []string{iconSpeaking, iconPrioritySpeaker, iconRecording, iconMuted, iconDeafened} {
		box.Append(r.icons[k])
	}
	r.box = box
	r.depth = -1
	r.setDepth(depthOf(m, u.Channel))

	r.root = gtk.NewListBoxRow()
	r.root.SetChild(rowContent(box))
	r.root.SetActivatable(true)
	r.root.SetSelectable(true)
	r.root.AddCSSClass("user-row")
	if u.Session == m.self {
		r.root.AddCSSClass("self-user")
	}
	if menu != nil {
		contextMenu(r.root, menu)
	}
	// A click opens the conversation with the user, also when the row is
	// already selected (selection changes alone would not fire again).
	if open != nil {
		click := gtk.NewGestureClick()
		click.SetButton(gdk.BUTTON_PRIMARY)
		click.ConnectReleased(func(n int, _, _ float64) {
			if n == 1 {
				open()
			}
		})
		r.root.AddController(click)
	}
	r.update(u, m.speaking[session])
	return r
}

// contextMenu opens the row's menu on a right click and, on touch
// screens, on a long press (touch only, like GTK's own widgets). The long
// press claims the touch, so it does not also count as a tap (which opens
// a conversation).
func contextMenu(row *gtk.ListBoxRow, menu func(x, y float64)) {
	// Assistive technologies announce that the row has a menu (opened
	// with the Menu key or Shift+F10, see Tree.onKey).
	row.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyHasPopup},
		[]coreglib.Value{*coreglib.NewValue(true)})

	g := gtk.NewGestureClick()
	g.SetButton(gdk.BUTTON_SECONDARY)
	g.ConnectPressed(func(_ int, x, y float64) { menu(x, y) })
	row.AddController(g)

	hold := gtk.NewGestureLongPress()
	hold.SetTouchOnly(true)
	self := weakref.New(hold)
	hold.ConnectPressed(func(x, y float64) {
		self.Do(func(hold *gtk.GestureLongPress) { hold.SetState(gtk.EventSequenceClaimed) })
		menu(x, y)
	})
	row.AddController(hold)
}

// onFocusLeave calls f when the keyboard focus leaves row.
func onFocusLeave(row *gtk.ListBoxRow, f func()) {
	c := gtk.NewEventControllerFocus()
	c.ConnectLeave(f)
	row.AddController(c)
}

// setDepth indents the row for its depth in the tree.
func (r *channelRowWidgets) setDepth(depth int) {
	if r.depth != depth {
		r.depth = depth
		r.box.SetMarginStart(indentPerDepthPx * depth)
	}
}

func (r *userRowWidgets) setDepth(depth int) {
	if r.depth != depth {
		r.depth = depth
		r.box.SetMarginStart(indentPerDepthPx * depth)
	}
}

// setCurrent marks the channel one is in oneself.
func (r *channelRowWidgets) setCurrent(current bool) {
	if r.current != current {
		r.showCurrent(current)
		r.announce()
	}
}

func (r *channelRowWidgets) showCurrent(current bool) {
	r.current = current
	r.here.SetVisible(current)
	if current {
		r.root.AddCSSClass("current-channel")
		r.content.SetTooltipText(tr("You are in this channel"))
	} else {
		r.root.RemoveCSSClass("current-channel")
		r.content.SetTooltipText(tr("Double-click to join"))
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
	hasKids := len(n.children) > 0 || len(n.users) > 0
	shown := channelShown{name: n.info.Name, users: len(n.users), kids: hasKids, expanded: n.expanded}
	if r.hasShown && shown == r.shown {
		return
	}
	r.shown, r.hasShown = shown, true
	r.name.SetText(n.info.Name)
	// Without children the chevron keeps its place (invisible, not
	// clickable), so sibling channel names line up.
	r.chevron.SetOpacity(boolOpacity(hasKids))
	r.chevron.SetCanTarget(hasKids)
	if n.expanded {
		r.chevron.SetIconName(iconExpanderExpanded)
	} else {
		r.chevron.SetIconName(iconExpanderCollapsed)
	}
	// Every channel tells assistive technologies whether it is open, an
	// empty one too: a state that appeared when a user enters would be
	// announced like a change.
	r.setExpandedState(n.expanded)
	if len(n.users) > 0 {
		r.count.SetText(fmt.Sprintf("%d", len(n.users)))
		r.count.SetVisible(true)
	} else {
		r.count.SetVisible(false)
	}
	r.users = len(n.users)
	r.announce()
}

func (r *channelRowWidgets) setExpandedState(expanded bool) {
	// GTK reads "boolean or undefined" states as an int (-1 is
	// undefined); a boolean GValue would read as false.
	open := int32(0)
	if expanded {
		open = 1
	}
	r.root.UpdateState([]gtk.AccessibleState{gtk.AccessibleStateExpanded},
		[]coreglib.Value{*coreglib.NewValue(open)})
}

func (r *channelRowWidgets) announce() {
	r.text.apply(r.root, r.name.Text(), func(unread int) string {
		return channelDescription(r.users, r.current, unread)
	})
}

func (r *channelRowWidgets) setUnread(n int) {
	if n == r.text.unread {
		return
	}
	showUnread(r.badge, n)
	r.text.unread = n
	r.announce()
}

func (r *userRowWidgets) update(u bus.UserInfo, speaking bool) {
	r.name.SetText(u.Name)
	r.avatar.SetText(u.Name)
	muted := u.Muted || u.SelfMuted || u.Suppressed
	r.icons[iconMuted].SetVisible(muted)
	r.icons[iconDeafened].SetVisible(u.Deafened || u.SelfDeafened)
	r.icons[iconPrioritySpeaker].SetVisible(u.PrioritySpeaker)
	r.icons[iconRecording].SetVisible(u.Recording)
	setTooltip(r.icons[iconMuted], iconTooltip(stateIf{u.SelfMuted, stateSelfMuted},
		stateIf{u.Muted, stateServerMuted}, stateIf{u.Suppressed, stateSuppressed}))
	setTooltip(r.icons[iconDeafened], iconTooltip(stateIf{u.SelfDeafened, stateSelfDeafened},
		stateIf{u.Deafened, stateServerDeafened}))
	setTooltip(r.icons[iconPrioritySpeaker], iconTooltip(stateIf{u.PrioritySpeaker, statePrioritySpeaker}))
	setTooltip(r.icons[iconRecording], iconTooltip(stateIf{u.Recording, stateRecording}))
	// Speech while muted (the own microphone's voice detection) is not
	// transmitted: the crossed-out microphone lights up yellow instead of a
	// second, misleading microphone icon, and the name stays regular.
	r.icons[iconSpeaking].SetVisible(speaking && !muted)
	if speaking && muted {
		r.icons[iconMuted].AddCSSClass("muted-speaking")
	} else {
		r.icons[iconMuted].RemoveCSSClass("muted-speaking")
	}
	setBold(r.name, speaking && !muted)
	r.user = u
	r.announce()
}

// setTooltip leaves an unchanged tooltip alone: rows update on every word
// spoken.
func setTooltip(img *gtk.Image, text string) {
	if img.TooltipText() != text {
		img.SetTooltipText(text)
	}
}

func (r *userRowWidgets) announce() {
	r.text.apply(r.root, r.user.Name, func(unread int) string {
		return userDescription(r.user, r.local, unread)
	})
}

func (r *userRowWidgets) setUnread(n int) {
	if n == r.text.unread {
		return
	}
	showUnread(r.badge, n)
	r.text.unread = n
	r.announce()
}

// setLocalAudio shows the volume set for oneself: nothing at 100 %, a
// person with a speaker otherwise (the level in the tooltip), a crossed-out
// person in the error colour when muted for oneself.
func (r *userRowWidgets) setLocalAudio(a localAudio) {
	r.localAudio.RemoveCSSClass("local-muted")
	switch {
	case a.muted:
		r.localAudio.SetFromIconName(iconLocalMuted)
		r.localAudio.AddCSSClass("local-muted")
		r.localAudio.SetTooltipText(tr("Muted for you"))
	case a.volume != 1:
		r.localAudio.SetFromIconName(iconLocalVolume)
		r.localAudio.SetTooltipText(fmt.Sprintf(tr("Volume for you: %d %%"), percent(a.volume)))
	}
	r.localAudio.SetVisible(a.muted || a.volume != 1)
	r.local = a
	r.announce()
}

func percent(v float64) int { return int(v*100 + 0.5) }

func boolOpacity(on bool) float64 {
	if on {
		return 1
	}
	return 0
}

func setBold(lbl *gtk.Label, bold bool) {
	attr := pango.NewAttrList()
	if bold {
		attr.Insert(pango.NewAttrWeight(pango.WeightBold))
	}
	lbl.SetAttributes(attr)
}

// ChannelMenuActions bundles the action callbacks used by buildChannelMenu.
type ChannelMenuActions struct {
	Join      func(uint32)
	CreateSub func(uint32)
	Edit      func(uint32)
	Delete    func(uint32)
}

// buildChannelMenu assembles the channel context menu. Entries are enabled
// by the local user's permissions (channelMenuPerms); the returned refresh
// re-evaluates them, e.g. when the server answers a permission query while
// the menu is open.
func buildChannelMenu(m *treeModel, id uint32, a ChannelMenuActions) (*gtk.PopoverMenu, func()) {
	group := gio.NewSimpleActionGroup()
	acts := map[string]*gio.SimpleAction{}
	add := func(name string, cb func(uint32)) {
		act := gio.NewSimpleAction(name, nil)
		if cb != nil {
			act.ConnectActivate(func(*glib.Variant) { cb(id) })
		}
		act.SetEnabled(false)
		group.AddAction(act)
		acts[name] = act
	}
	add("join", a.Join)
	add("create-sub", a.CreateSub)
	add("edit", a.Edit)
	add("remove", a.Delete)

	refresh := func() {
		st := channelMenuPerms(m, id)
		acts["join"].SetEnabled(a.Join != nil && st.join)
		acts["create-sub"].SetEnabled(a.CreateSub != nil && st.createSub)
		acts["edit"].SetEnabled(a.Edit != nil && st.edit)
		acts["remove"].SetEnabled(a.Delete != nil && st.remove)
	}
	refresh()

	menu := gio.NewMenu()
	// TRANSLATORS: Context menu entry: move yourself into this channel.
	menu.Append(tr("Join"), "ctx.join")
	menu.Append(tr("Create sub-channel…"), "ctx.create-sub")
	menu.Append(tr("Edit…"), "ctx.edit")
	menu.Append(tr("Remove…"), "ctx.remove")

	pm := gtk.NewPopoverMenuFromModel(menu)
	pm.InsertActionGroup("ctx", group)
	return pm, refresh
}

// UserMenuActions bundles the action callbacks used by buildUserMenu.
type UserMenuActions struct {
	IsMuted    bool // current server-mute state for the toggle label
	IsDeafened bool // current server-deafen state

	// Local audio (for oneself only; nil SetLocalAudio hides the section,
	// e.g. on one's own row).
	LocalAudio    localAudio
	SetLocalAudio func(localAudio)

	Message  func()
	Move     func()
	Mute     func()
	Deafen   func()
	Priority func()
	Kick     func()
	Ban      func()
	Register func()
	Stats    func()
}

// buildUserMenu assembles the user context menu. Entries are enabled by
// the local user's permissions (userMenuPerms); the returned refresh
// re-evaluates them.
func buildUserMenu(m *treeModel, session uint32, a UserMenuActions) (*gtk.PopoverMenu, func()) {
	group := gio.NewSimpleActionGroup()
	acts := map[string]*gio.SimpleAction{}
	cbs := map[string]func(){}
	add := func(name string, cb func()) {
		act := gio.NewSimpleAction(name, nil)
		if cb != nil {
			act.ConnectActivate(func(*glib.Variant) { cb() })
		}
		act.SetEnabled(false)
		group.AddAction(act)
		acts[name] = act
		cbs[name] = cb
	}
	add("message", a.Message)
	add("move", a.Move)
	add("mute", a.Mute)
	add("deafen", a.Deafen)
	add("priority", a.Priority)
	add("kick", a.Kick)
	add("ban", a.Ban)
	add("register", a.Register)
	add("stats", a.Stats)

	refresh := func() {
		st := userMenuPerms(m, session)
		for name, ok := range map[string]bool{
			"message": st.message, "move": st.move, "mute": st.mute, "deafen": st.deafen,
			"priority": st.priority, "kick": st.kick, "ban": st.ban,
			"register": st.register, "stats": st.stats,
		} {
			acts[name].SetEnabled(ok && cbs[name] != nil)
		}
	}
	refresh()

	muteLabel := tr("Mute on server")
	if a.IsMuted {
		muteLabel = tr("Unmute on server")
	}
	deafenLabel := tr("Deafen on server")
	if a.IsDeafened {
		deafenLabel = tr("Undeafen on server")
	}

	menu := gio.NewMenu()
	root := menu
	menu.Append(tr("Send message…"), "ctx.message")
	var volume gtk.Widgetter
	if a.SetLocalAudio != nil {
		state := a.LocalAudio
		mute := gio.NewSimpleActionStateful("local-mute", nil, glib.NewVariantBoolean(state.muted))
		muteRef := weakref.New(mute)
		mute.ConnectActivate(func(*glib.Variant) {
			state.muted = !state.muted
			muteRef.Do(func(mute *gio.SimpleAction) { mute.SetState(glib.NewVariantBoolean(state.muted)) })
			a.SetLocalAudio(state)
		})
		group.AddAction(mute)
		volume = localVolumeSlider(state.volume, func(v float64) {
			state.volume = v
			a.SetLocalAudio(state)
		})
		local := gio.NewMenu()
		slider := gio.NewMenuItem("", "")
		slider.SetAttributeValue("custom", glib.NewVariantString("local-volume"))
		local.AppendItem(slider)
		local.Append(tr("Mute for me"), "ctx.local-mute")
		menu.AppendSection("", local)
	}
	admin := gio.NewMenu()
	menu.AppendSection("", admin)
	menu = admin
	menu.Append(tr("Move to my channel"), "ctx.move")
	menu.Append(muteLabel, "ctx.mute")
	menu.Append(deafenLabel, "ctx.deafen")
	menu.Append(tr("Priority speaker"), "ctx.priority")
	menu.Append(tr("Kick…"), "ctx.kick")
	menu.Append(tr("Ban…"), "ctx.ban")
	menu.Append(tr("Register"), "ctx.register")
	menu.Append(tr("View stats"), "ctx.stats")
	pm := gtk.NewPopoverMenuFromModel(root)
	pm.InsertActionGroup("ctx", group)
	if volume != nil {
		pm.AddChild(volume, "local-volume")
	}
	return pm, refresh
}

// localVolumeSlider is the "Volume for me" slider of the user menu,
// 0–200 % with a mark at 100 %; set gets the new volume (0..2).
func localVolumeSlider(volume float64, set func(float64)) gtk.Widgetter {
	box := gtk.NewBox(gtk.OrientationVertical, 2)
	box.AddCSSClass("local-volume")
	title := gtk.NewLabel(tr("Volume for me"))
	title.SetXAlign(0)
	title.AddCSSClass("caption-heading")
	scale := gtk.NewScaleWithRange(gtk.OrientationHorizontal, 0, 200, 5)
	scale.SetValue(volume * 100)
	scale.AddMark(100, gtk.PosBottom, "")
	scale.SetDrawValue(true)
	scale.SetValuePos(gtk.PosRight)
	scale.SetFormatValueFunc(func(_ *gtk.Scale, v float64) string { return volumeText(v) })
	scale.SetSizeRequest(200, -1)
	self := weakref.New(scale)
	scale.ConnectValueChanged(func() {
		self.Do(func(scale *gtk.Scale) {
			scale.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyValueText},
				[]coreglib.Value{*coreglib.NewValue(volumeText(scale.Value()))})
			set(scale.Value() / 100)
		})
	})
	// Up and Down go to the menu entry above or below, as everywhere else
	// in the menu; Left and Right (Page Up/Down, Home, End) set the value.
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		handled := false
		self.Do(func(scale *gtk.Scale) { handled = sliderKey(scale, keyval, state) })
		return handled
	})
	scale.AddController(keys)
	// Screen readers name the slider by its title and read its value as it
	// is shown.
	scale.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel, gtk.AccessiblePropertyValueText},
		[]coreglib.Value{*coreglib.NewValue(title.Text()), *coreglib.NewValue(volumeText(scale.Value()))})
	box.Append(title)
	box.Append(scale)
	return box
}

// volumeText is the slider's value (0..200) as it is shown.
func volumeText(v float64) string { return fmt.Sprintf("%d %%", int(v+0.5)) }

// sliderKey moves the focus from the volume slider to the menu entry
// above or below it on Up or Down.
func sliderKey(scale *gtk.Scale, keyval uint, state gdk.ModifierType) bool {
	if state&(gdk.ShiftMask|gdk.ControlMask|gdk.AltMask|gdk.SuperMask) != 0 {
		return false
	}
	var dir gtk.DirectionType
	switch keyval {
	case gdk.KEY_Up, gdk.KEY_KP_Up:
		dir = gtk.DirUp
	case gdk.KEY_Down, gdk.KEY_KP_Down:
		dir = gtk.DirDown
	default:
		return false
	}
	if p := scale.Ancestor(gtk.GTypePopover); p != nil {
		gtk.BaseWidget(p).ChildFocus(dir)
	}
	return true
}

// popupAt shows menu pointing at the area at of row.
func popupAt(row gtk.Widgetter, menu *gtk.PopoverMenu, at gdk.Rectangle) {
	menu.SetParent(row)
	menu.SetPointingTo(&at)
	menu.Popup()
}

// expander is an empty widget that takes the free space in a row, so
// names and badges stay left and status icons right.
func expander() *gtk.Box {
	b := gtk.NewBox(gtk.OrientationHorizontal, 0)
	b.SetHExpand(true)
	return b
}
