package chat

import (
	"context"
	"fmt"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/protocol/gumble"
	"github.com/one337/murmel/internal/settings"
	"github.com/one337/murmel/internal/ui/clock"
	"github.com/one337/murmel/internal/ui/presence"
	"github.com/one337/murmel/internal/ui/richtext"
)

// maxRows is the scrollback cap per conversation: beyond it the oldest row
// is dropped so a flood can never grow the list (and memory) without bound.
const maxRows = 1000

// channelTag is the navigation tag of the channel chat (the root page).
const channelTag = "channel"

// View is the chat region: a navigation stack whose root page is the
// channel chat; each private conversation opens as a page on top of it
// (clicking a user in the channel tree). Private messages never appear in
// the channel chat. Unread counts are published as bus.UnreadChanged.
// Construct on the main loop; ctx tears the bus subscriptions down.
type View struct {
	nav     *adw.NavigationView
	channel *conversation
	dms     map[convKey]*conversation
	unread  map[convKey]int // private conversations
	// channelUnread counts channel messages while a private conversation
	// is shown (or the window is in the background).
	channelUnread int

	bus      *bus.Bus
	sender   Sender
	settings settings.Settings // nil: log defaults
	// requestedDescription remembers channels whose description was
	// fetched, so a missing one is asked for once.
	requestedDescription map[uint32]bool
	// requestedPermission remembers channels whose permissions were asked
	// for and have not arrived yet.
	requestedPermission map[uint32]bool
	// Server limits applied to every composer.
	maxLen      int
	imageLimit  int
	htmlAllowed bool
	// windowActive is read from the toplevel once the view is mapped.
	windowActive func() bool
	// resuming: an automatic reconnect is under way, the session's chat
	// continues; lastWelcome then suppresses the repeated welcome text.
	resuming    bool
	lastWelcome string
}

// NewView assembles the view and subscribes to the bus. sender may be nil
// (sends then produce an inline error row).
func NewView(ctx context.Context, b *bus.Bus, sender Sender) *View {
	v := &View{
		bus:          b,
		sender:       sender,
		dms:          map[convKey]*conversation{},
		unread:       map[convKey]int{},
		maxLen:       defaultMaxMessageLength,
		imageLimit:   defaultImageMessageLength,
		htmlAllowed:  true,
		windowActive: func() bool { return true },
	}
	v.channel = newConversation(channelTag, i18n.Tr("Channel"), i18n.Tr("No messages yet"))
	v.channel.composer.SetSend(func(html, plain string) { v.send(v.channel, 0, html, plain) })

	v.nav = adw.NewNavigationView()
	v.nav.Add(v.channel.page)
	coreglib.BaseObject(v.nav).NotifyProperty("visible-page", v.markVisibleRead)
	v.nav.ConnectMap(v.trackWindow)

	v.subscribe(ctx)
	// Time stamps follow the clock format, also when it becomes known
	// only after the first rows (the desktop is asked asynchronously).
	removeClock := clock.OnChange(v.refreshTimes)
	context.AfterFunc(ctx, func() { glib.IdleAdd(removeClock) })
	tracker := presence.NewTracker()
	tracker.Listen(v.onPresence)
	tracker.Subscribe(ctx, b)
	v.refreshTitles()
	return v
}

// UseSettings makes the channel chat log follow the chat-log setting.
func (v *View) UseSettings(st settings.Settings) { v.settings = st }

// onPresence writes the enabled log lines for a presence event into the
// channel chat.
func (v *View) onPresence(ev presence.Event) {
	var stored map[string]bool
	if v.settings != nil {
		stored = v.settings.ChatLogEvents()
	}
	enabled := func(e LogEvent) bool { return LogEnabled(stored, e) }
	st := v.bus.State()
	name := func(id uint32) string {
		if ch, ok := st.Channels[id]; ok {
			return ch.Name
		}
		return i18n.Tr("another channel")
	}
	for _, line := range logLines(ev, enabled, st.SelfSession, name) {
		v.channel.append(rowStyle{kind: rowLog, at: time.Now(), content: line})
	}
}

// Root returns the region's top widget for the window's chat slot.
func (v *View) Root() gtk.Widgetter { return v.nav }

// AddChannelHeaderWidget packs w at the start of the channel chat's
// header (the window's narrow-layout mute toggle).
func (v *View) AddChannelHeaderWidget(w gtk.Widgetter) { v.channel.header.PackStart(w) }

// OpenConversation shows the private conversation with session (e.g. from
// a message notification).
func (v *View) OpenConversation(session uint32) {
	st := v.bus.State()
	u, ok := st.Users[session]
	if !ok || session == st.SelfSession {
		return
	}
	v.openDM(keyForUser(u), u.Name)
}

// trackWindow follows the toplevel's focus: unread messages of the shown
// conversation count as read once the window is active.
func (v *View) trackWindow() {
	root := v.nav.Root()
	if root == nil {
		return
	}
	obj := coreglib.BaseObject(root)
	v.windowActive = func() bool {
		active, _ := obj.ObjectProperty("is-active").(bool)
		return active
	}
	obj.NotifyProperty("is-active", v.markVisibleRead)
}

func (v *View) subscribe(ctx context.Context) {
	bus.SubscribeCtx(ctx, v.bus, v.onTextMessage)
	bus.SubscribeCtx(ctx, v.bus, func(ev bus.ServerConfigUpdated) {
		if ev.MaxMessageLength != nil {
			v.maxLen = *ev.MaxMessageLength
		}
		if ev.MaxImageMessageLength != nil {
			v.imageLimit = *ev.MaxImageMessageLength
		}
		if ev.AllowHTML != nil {
			v.htmlAllowed = *ev.AllowHTML
		}
		for _, c := range v.all() {
			v.configure(c)
		}
	})
	bus.SubscribeCtx(ctx, v.bus, func(ev bus.ChatTargetChanged) {
		st := v.bus.State()
		if ev.Session != 0 && ev.Session != st.SelfSession {
			if u, ok := st.Users[ev.Session]; ok {
				v.openDM(keyForUser(u), u.Name)
				return
			}
		}
		v.nav.PopToTag(channelTag)
	})
	bus.SubscribeCtx(ctx, v.bus, func(ev bus.PermissionDenied) {
		text, ok := denialText(ev, v.bus.State())
		if !ok {
			return
		}
		v.visible().append(rowStyle{kind: rowError, at: time.Now(), content: text})
		// Also told where the focus is not in the chat: a new row is not
		// read out by itself.
		v.nav.Announce(text, gtk.AccessibleAnnouncementPriorityMedium)
	})
	// A new connection or leaving the server starts a fresh chat; an
	// automatic reconnect keeps it.
	bus.SubscribeCtx(ctx, v.bus, func(ev bus.ConnectionStarting) {
		v.resuming = ev.Reconnect
		if !ev.Reconnect {
			v.reset()
		}
	})
	bus.SubscribeCtx(ctx, v.bus, func(bus.DisconnectRequested) {
		v.resuming = false
		v.reset()
	})
	bus.SubscribeCtx(ctx, v.bus, func(ev bus.Connected) {
		v.refreshTitles()
		repeated := v.resuming && ev.Welcome == v.lastWelcome
		v.resuming = false
		v.lastWelcome = ev.Welcome
		if !repeated && len(richtext.Parse(ev.Welcome)) > 0 {
			v.channel.append(rowStyle{kind: rowServer, sender: i18n.Tr("Welcome message"),
				at: time.Now(), content: richtext.PlainText(ev.Welcome), html: ev.Welcome})
		}
	})
	bus.SubscribeCtx(ctx, v.bus, func(bus.Disconnected) {
		v.nav.PopToTag(channelTag)
		v.refreshTitles()
		// Sessions change with the next connection: start counting anew.
		v.unread = map[convKey]int{}
		v.channelUnread = 0
		v.requestedDescription = nil
		v.requestedPermission = nil
	})
	bus.SubscribeCtx(ctx, v.bus, func(bus.UserAdded) { v.refreshTitles() })
	bus.SubscribeCtx(ctx, v.bus, func(bus.UserRemoved) { v.refreshTitles() })
	bus.SubscribeCtx(ctx, v.bus, func(bus.UserUpdated) { v.refreshTitles() })
	bus.SubscribeCtx(ctx, v.bus, func(bus.ChannelUpdated) { v.refreshTitles() })
}

// onTextMessage routes a message: private ones to the conversation with
// the sender, everything else (channel and server messages) to the
// channel chat.
func (v *View) onTextMessage(ev bus.TextMessage) {
	if len(richtext.Parse(ev.Message)) == 0 {
		return // nothing visible (empty or only unsupported content)
	}
	content := richtext.PlainText(ev.Message)
	st := v.bus.State()
	if ev.Sender != 0 && isPrivate(ev, st.SelfSession) {
		u, ok := st.Users[ev.Sender]
		if !ok {
			return
		}
		key := keyForUser(u)
		c := v.dm(key, u.Name)
		c.append(rowStyle{kind: rowMessage, sender: u.Name, at: time.Now(), content: content, html: ev.Message})
		if !v.isRead(c) {
			v.unread[key]++
			v.bus.Emit(bus.UnreadChanged{Session: ev.Sender, Count: v.unread[key]})
		}
		return
	}
	kind := rowMessage
	if ev.Sender == 0 {
		kind = rowServer // session 0 is never a user: the server itself
	}
	v.channel.append(rowStyle{kind: kind, sender: v.userName(ev.Sender), at: time.Now(),
		content: content, html: ev.Message})
	if !v.isRead(v.channel) {
		v.channelUnread++
		v.bus.Emit(bus.UnreadChanged{Session: 0, Count: v.channelUnread})
	}
}

// isPrivate reports whether a message was addressed to the local user.
func isPrivate(ev bus.TextMessage, self uint32) bool {
	for _, s := range ev.UserSessions {
		if s == self && self != 0 {
			return true
		}
	}
	return false
}

// reset empties the channel chat and drops the private conversations.
func (v *View) reset() {
	v.nav.PopToTag(channelTag)
	v.channel.clear()
	v.dms = map[convKey]*conversation{}
	v.unread = map[convKey]int{}
	v.channelUnread = 0
	v.lastWelcome = ""
	v.bus.Emit(bus.UnreadChanged{Session: 0, Count: 0})
}

// refreshTimes writes the time stamps of every conversation anew.
func (v *View) refreshTimes() {
	v.channel.refreshTimes()
	for _, c := range v.dms {
		c.refreshTimes()
	}
}

// dm returns (creating it if needed) the conversation for key.
func (v *View) dm(key convKey, name string) *conversation {
	if c, ok := v.dms[key]; ok {
		return c
	}
	c := newConversation("dm:"+string(key), name,
		fmt.Sprintf(i18n.Tr("Private conversation with %s"), name))
	v.configure(c)
	c.composer.SetSend(func(html, plain string) {
		session, ok := sessionFor(v.bus.State(), key)
		if !ok {
			c.append(rowStyle{kind: rowError, at: time.Now(),
				content: errorRowText(i18n.Tr("the user is offline"))})
			return
		}
		v.send(c, session, html, plain)
	})
	v.dms[key] = c
	v.refreshDM(key, c)
	return c
}

// openDM shows the conversation for key, on top of the channel chat.
func (v *View) openDM(key convKey, name string) {
	c := v.dm(key, name)
	tag := "dm:" + string(key)
	if v.nav.FindPage(tag) == nil || v.visibleTag() != tag {
		v.nav.PopToTag(channelTag)
		v.nav.Push(c.page)
	}
	v.markVisibleRead()
	c.composer.view.GrabFocus()
}

// send delivers a message from conversation c (session 0 = the local
// user's channel) and mirrors it locally — Mumble does not echo own
// messages back.
func (v *View) send(c *conversation, session uint32, html, plain string) {
	if v.sender == nil {
		c.append(rowStyle{kind: rowError, at: time.Now(),
			content: errorRowText(i18n.Tr("Not connected."))})
		return
	}
	st := v.bus.State()
	var channelID uint32
	if session == 0 {
		channelID = st.Users[st.SelfSession].Channel
	}
	if err := v.sender.SendText(channelID, session, html); err != nil {
		c.append(rowStyle{kind: rowError, at: time.Now(),
			content: errorRowText(err.Error())})
		return
	}
	c.appendOwn(rowStyle{kind: rowMessage, sender: selfName(st), at: time.Now(),
		content: plain, html: html})
}

// all lists every conversation.
func (v *View) all() []*conversation {
	out := []*conversation{v.channel}
	for _, c := range v.dms {
		out = append(out, c)
	}
	return out
}

// configure applies the server's message limits to a conversation.
func (v *View) configure(c *conversation) {
	c.composer.SetMaxMessageLength(v.maxLen)
	c.composer.SetImageLimit(v.imageLimit)
	c.composer.SetHTMLAllowed(v.htmlAllowed)
}

// --- read state -----------------------------------------------------------

func (v *View) visibleTag() string {
	if p := v.nav.VisiblePage(); p != nil {
		return p.Tag()
	}
	return ""
}

// FocusComposer moves the keyboard focus into the message field of the
// conversation on screen and reports whether it got there.
func (v *View) FocusComposer() bool { return v.visible().composer.view.GrabFocus() }

// visible is the conversation on screen.
func (v *View) visible() *conversation {
	for _, c := range v.dms {
		if c.page.Tag() == v.visibleTag() {
			return c
		}
	}
	return v.channel
}

// isRead reports whether c is being read right now.
func (v *View) isRead(c *conversation) bool {
	return v.visibleTag() == c.page.Tag() && v.nav.Mapped() && v.windowActive()
}

// markVisibleRead clears the unread count of the conversation on screen.
func (v *View) markVisibleRead() {
	c := v.visible()
	if !v.isRead(c) {
		return
	}
	if c == v.channel {
		if v.channelUnread > 0 {
			v.channelUnread = 0
			v.bus.Emit(bus.UnreadChanged{Session: 0, Count: 0})
		}
		return
	}
	for key, dc := range v.dms {
		if dc == c && v.unread[key] > 0 {
			v.unread[key] = 0
			session, _ := sessionFor(v.bus.State(), key)
			v.bus.Emit(bus.UnreadChanged{Session: session, Count: 0})
		}
	}
}

// --- titles ---------------------------------------------------------------

// refreshTitles names the channel chat after the local user's channel and
// updates every private conversation's online state.
func (v *View) refreshTitles() {
	st := v.bus.State()
	name := i18n.Tr("Channel")
	if ch, ok := st.Channels[st.Users[st.SelfSession].Channel]; ok && st.SelfSession != 0 {
		name = "#" + ch.Name
	}
	v.channel.setTitle(name, "")
	v.refreshDescription(st)
	reason := ""
	if me, ok := st.Users[st.SelfSession]; ok && st.SelfSession != 0 && !v.mayWrite(st, me.Channel) {
		reason = i18n.Tr("You may not send messages in this channel.")
	}
	v.lock(v.channel, reason)
	for key, c := range v.dms {
		v.refreshDM(key, c)
	}
}

// refreshDescription shows the local user's channel description above
// the channel chat, fetching it when the server sent only its hash.
func (v *View) refreshDescription(st bus.State) {
	if st.SelfSession == 0 {
		v.channel.info.set("")
		return
	}
	id := st.Users[st.SelfSession].Channel
	ch, ok := st.Channels[id]
	if !ok {
		v.channel.info.set("")
		return
	}
	if ch.Description == "" && ch.DescriptionPending && !v.requestedDescription[id] {
		if v.requestedDescription == nil {
			v.requestedDescription = map[uint32]bool{}
		}
		v.requestedDescription[id] = true
		v.bus.Emit(bus.ChannelDescriptionRequested{ChannelID: id})
	}
	v.channel.info.set(ch.Description)
}

func (v *View) refreshDM(key convKey, c *conversation) {
	st := v.bus.State()
	if session, ok := sessionFor(st, key); ok {
		u := st.Users[session]
		c.setTitle(u.Name, "")
		reason := ""
		if !v.mayWrite(st, u.Channel) {
			reason = fmt.Sprintf(i18n.Tr("%s is in a channel where you may not write."), u.Name)
		}
		v.lock(c, reason)
		return
	}
	c.setTitle(c.title.Title(), i18n.Tr("offline"))
	v.lock(c, fmt.Sprintf(i18n.Tr("%s is offline."), c.title.Title()))
}

// mayWrite tells whether the server lets the local user send text to
// channel id (a channel message there, or a private message to someone
// in it): only known permissions without Write and Text Message refuse.
// Unknown permissions are asked for.
func (v *View) mayWrite(st bus.State, id uint32) bool {
	ch, ok := st.Channels[id]
	if !ok {
		return true
	}
	if ch.Permissions == nil {
		if !v.requestedPermission[id] {
			if v.requestedPermission == nil {
				v.requestedPermission = map[uint32]bool{}
			}
			v.requestedPermission[id] = true
			v.bus.Emit(bus.ChannelPermissionRequested{ChannelID: id})
		}
		return true
	}
	delete(v.requestedPermission, id) // asked again after the server drops them
	return gumble.Permission(*ch.Permissions)&(gumble.PermissionWrite|gumble.PermissionTextMessage) != 0
}

// lock locks or unlocks a conversation's field. Locking the focused field
// is also announced: the line above it is not read out by itself.
func (v *View) lock(c *conversation, reason string) (announced bool) {
	if reason == c.composer.locked {
		return false
	}
	c.composer.SetLocked(reason)
	if reason == "" || !c.composer.view.HasFocus() {
		return false
	}
	v.nav.Announce(reason, gtk.AccessibleAnnouncementPriorityMedium)
	return true
}

// userName resolves a session to a display name.
func (v *View) userName(session uint32) string {
	if session == 0 {
		return i18n.Tr("Server")
	}
	if u, ok := v.bus.State().Users[session]; ok && u.Name != "" {
		return u.Name
	}
	return i18n.Tr("Unknown user")
}

// selfName returns the local user's display name.
func selfName(st bus.State) string {
	if u, ok := st.Users[st.SelfSession]; ok && u.Name != "" {
		return u.Name
	}
	return i18n.Tr("You")
}
