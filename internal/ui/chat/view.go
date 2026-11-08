package chat

import (
	"context"
	"fmt"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
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

	bus    *bus.Bus
	sender Sender
	maxLen int
	// windowActive is read from the toplevel once the view is mapped.
	windowActive func() bool
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
		windowActive: func() bool { return true },
	}
	v.channel = newConversation(channelTag, tr("Channel"), tr("No messages yet"))
	v.channel.composer.SetSend(func(html, plain string) { v.send(v.channel, 0, html, plain) })

	v.nav = adw.NewNavigationView()
	v.nav.Add(v.channel.page)
	coreglib.InternObject(v.nav).NotifyProperty("visible-page", v.markVisibleRead)
	v.nav.ConnectMap(v.trackWindow)

	v.subscribe(ctx)
	v.refreshTitles()
	return v
}

// Root returns the region's top widget for the window's chat slot.
func (v *View) Root() gtk.Widgetter { return v.nav }

// SetSender swaps the outgoing seam.
func (v *View) SetSender(s Sender) { v.sender = s }

// AddChannelHeaderWidget packs w at the start of the channel chat's
// header (the window's narrow-layout mute toggle).
func (v *View) AddChannelHeaderWidget(w gtk.Widgetter) { v.channel.header.PackStart(w) }

// RowCount reports the channel chat's retained scrollback (QA hook).
func (v *View) RowCount() int { return v.channel.hist.len() }

// InjectCompose types text into the channel composer and submits it — the
// QA harness drives the real send path with this.
func (v *View) InjectCompose(text string) {
	v.channel.composer.buffer.SetText(text)
	v.channel.composer.Submit()
}

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
	obj := coreglib.InternObject(root)
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
			v.channel.composer.SetMaxMessageLength(v.maxLen)
			for _, c := range v.dms {
				c.composer.SetMaxMessageLength(v.maxLen)
			}
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
		if ev.Reason == bus.DenyText || ev.Reason == bus.DenyTextTooLong {
			v.visible().append(rowStyle{kind: rowError, at: time.Now(),
				content: errorRowText(denyText(ev))}, false)
		}
	})
	bus.SubscribeCtx(ctx, v.bus, func(ev bus.Connected) {
		v.refreshTitles()
		if welcome := PlainText(ev.Welcome); welcome != "" {
			v.channel.append(rowStyle{kind: rowServer, sender: tr("Welcome message"),
				at: time.Now(), content: welcome}, true)
		}
	})
	bus.SubscribeCtx(ctx, v.bus, func(bus.Disconnected) {
		v.nav.PopToTag(channelTag)
		v.refreshTitles()
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
	content := PlainText(ev.Message)
	if content == "" {
		// Fully stripped (e.g. an image-only message): nothing to render.
		return
	}
	st := v.bus.State()
	if ev.Sender != 0 && isPrivate(ev, st.SelfSession) {
		u, ok := st.Users[ev.Sender]
		if !ok {
			return
		}
		key := keyForUser(u)
		c := v.dm(key, u.Name)
		c.append(rowStyle{kind: rowMessage, sender: u.Name, at: time.Now(), content: content}, true)
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
		content: content}, true)
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

// dm returns (creating it if needed) the conversation for key.
func (v *View) dm(key convKey, name string) *conversation {
	if c, ok := v.dms[key]; ok {
		return c
	}
	c := newConversation("dm:"+string(key), name,
		fmt.Sprintf(tr("Private conversation with %s"), name))
	c.composer.SetMaxMessageLength(v.maxLen)
	c.composer.SetSend(func(html, plain string) {
		session, ok := sessionFor(v.bus.State(), key)
		if !ok {
			c.append(rowStyle{kind: rowError, at: time.Now(),
				content: errorRowText(tr("the user is offline"))}, false)
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
			content: errorRowText(tr("Not connected."))}, false)
		return
	}
	st := v.bus.State()
	var channelID uint32
	if session == 0 {
		channelID = st.Users[st.SelfSession].Channel
	}
	if err := v.sender.SendText(channelID, session, html); err != nil {
		c.append(rowStyle{kind: rowError, at: time.Now(),
			content: errorRowText(err.Error())}, false)
		return
	}
	c.append(rowStyle{kind: rowMessage, sender: selfName(st), at: time.Now(),
		content: plain}, false)
}

// --- read state -----------------------------------------------------------

func (v *View) visibleTag() string {
	if p := v.nav.VisiblePage(); p != nil {
		return p.Tag()
	}
	return ""
}

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
	name := tr("Channel")
	if ch, ok := st.Channels[st.Users[st.SelfSession].Channel]; ok && st.SelfSession != 0 {
		name = "#" + ch.Name
	}
	v.channel.setTitle(name, "")
	for key, c := range v.dms {
		v.refreshDM(key, c)
	}
}

func (v *View) refreshDM(key convKey, c *conversation) {
	st := v.bus.State()
	if session, ok := sessionFor(st, key); ok {
		c.setTitle(st.Users[session].Name, "")
		c.composer.SetEnabled(true, "")
		return
	}
	c.setTitle(c.title.Title(), tr("offline"))
	c.composer.SetEnabled(false, tr("The user is offline"))
}

// userName resolves a session to a display name.
func (v *View) userName(session uint32) string {
	if session == 0 {
		return tr("Server")
	}
	if u, ok := v.bus.State().Users[session]; ok && u.Name != "" {
		return u.Name
	}
	return tr("Unknown user")
}

// selfName returns the local user's display name.
func selfName(st bus.State) string {
	if u, ok := st.Users[st.SelfSession]; ok && u.Name != "" {
		return u.Name
	}
	return tr("You")
}

// denyText renders a send-denial for the error row.
func denyText(ev bus.PermissionDenied) string {
	if ev.Text != "" {
		return ev.Text
	}
	if ev.Reason == bus.DenyTextTooLong {
		return tr("message too long")
	}
	return ""
}
