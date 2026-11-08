package chat

import (
	"context"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"io.github/murmel/internal/bus"
)

// maxRows is the scrollback cap: beyond it the oldest row is dropped so a
// flood can never grow the list (and memory) without bound.
const maxRows = 1000

// View is the conversation region: target banner on top, the bounded
// scrollback list, a jump-to-latest indicator while scrolled up, and the
// composer. Construct on the main loop; ctx tears the bus subscriptions down.
type View struct {
	root     *gtk.Box
	scroll   *gtk.ScrolledWindow
	list     *gtk.ListBox
	banner   *gtk.Label
	jump     *gtk.Revealer
	composer *composer

	bus    *bus.Bus
	sender Sender

	hist   *history[*gtk.ListBoxRow]
	target target
	// pin keeps the view scrolled to the bottom across allocation-driven
	// adjustment changes while the user is anchored there.
	pin bool
}

// NewView assembles the view and subscribes to the bus. sender may be nil
// (sends then produce an inline error row); T19 wires the live seam via
// SetSender.
func NewView(ctx context.Context, b *bus.Bus, sender Sender) *View {
	v := &View{
		bus:    b,
		sender: sender,
		hist:   newHistory[*gtk.ListBoxRow](maxRows),
	}

	v.banner = gtk.NewLabel("")
	v.banner.AddCSSClass("heading")
	v.banner.AddCSSClass("dim-label")
	v.banner.SetXAlign(0.0)
	v.banner.SetEllipsize(pango.EllipsizeEnd)
	v.banner.SetMarginTop(6)
	v.banner.SetMarginStart(6)
	v.banner.SetMarginEnd(6)

	v.list = gtk.NewListBox()
	v.list.SetSelectionMode(gtk.SelectionNone)
	empty := gtk.NewLabel(tr("No messages yet"))
	empty.AddCSSClass("dim-label")
	v.list.SetPlaceholder(empty)

	v.scroll = gtk.NewScrolledWindow()
	v.scroll.SetChild(v.list)
	v.scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	v.scroll.SetVExpand(true)

	adj := v.scroll.VAdjustment()
	adj.ConnectValueChanged(func() {
		v.pin = v.atBottom()
		if v.pin {
			v.jump.SetRevealChild(false)
		}
	})
	adj.ConnectChanged(func() {
		if v.pin {
			v.scrollToBottom()
		}
	})

	jumpBtn := gtk.NewButtonWithLabel(tr("Jump to latest"))
	jumpBtn.AddCSSClass("suggested-action")
	jumpBtn.AddCSSClass("pill")
	jumpBtn.SetTooltipText(tr("Scroll to the newest message"))
	jumpBtn.ConnectClicked(func() {
		v.pin = true
		v.scrollToBottom()
		v.jump.SetRevealChild(false)
	})
	v.jump = gtk.NewRevealer()
	v.jump.SetChild(jumpBtn)
	v.jump.SetTransitionType(gtk.RevealerTransitionTypeSlideUp)
	v.jump.SetHAlign(gtk.AlignCenter)
	v.jump.SetRevealChild(false)

	v.composer = newComposer()
	v.composer.SetSend(v.send)

	v.root = gtk.NewBox(gtk.OrientationVertical, 0)
	v.root.Append(v.banner)
	v.root.Append(v.scroll)
	v.root.Append(v.jump)
	v.root.Append(v.composer.root)

	v.subscribe(ctx)
	v.refreshBanner()
	return v
}

// Root returns the region's top widget for the window's chat slot.
func (v *View) Root() gtk.Widgetter { return v.root }

// SetSender swaps the outgoing seam (T19 wires the live connection).
func (v *View) SetSender(s Sender) { v.sender = s }

// RowCount reports the retained scrollback size (QA hook for the cap).
func (v *View) RowCount() int { return v.hist.len() }

// InjectCompose types text into the composer and submits it — the QA harness
// drives the real send path with this instead of synthesizing key events.
func (v *View) InjectCompose(text string) {
	v.composer.buffer.SetText(text)
	v.composer.Submit()
}

// subscribe wires every bus event the view reacts to. All handlers run on
// the main loop (bus guarantee).
func (v *View) subscribe(ctx context.Context) {
	bus.SubscribeCtx(ctx, v.bus, v.onTextMessage)
	bus.SubscribeCtx(ctx, v.bus, func(ev bus.ServerConfigUpdated) {
		if ev.MaxMessageLength != nil {
			v.composer.SetMaxMessageLength(*ev.MaxMessageLength)
		}
	})
	bus.SubscribeCtx(ctx, v.bus, func(ev bus.ChatTargetChanged) {
		v.target = targetFromEvent(ev)
		v.refreshBanner()
	})
	bus.SubscribeCtx(ctx, v.bus, func(ev bus.PermissionDenied) {
		if ev.Reason == bus.DenyText || ev.Reason == bus.DenyTextTooLong {
			v.appendRow(rowStyle{kind: rowError, at: time.Now(),
				content: errorRowText(denyText(ev))}, false)
		}
	})
	// Names may resolve only after these events arrive; the banner follows.
	bus.SubscribeCtx(ctx, v.bus, func(ev bus.Connected) {
		v.refreshBanner()
		if welcome := PlainText(ev.Welcome); welcome != "" {
			v.appendRow(rowStyle{kind: rowServer, sender: tr("Welcome message"),
				at: time.Now(), content: welcome}, true)
		}
	})
	bus.SubscribeCtx(ctx, v.bus, func(bus.UserAdded) { v.refreshBanner() })
	bus.SubscribeCtx(ctx, v.bus, func(bus.UserRemoved) { v.refreshBanner() })
	bus.SubscribeCtx(ctx, v.bus, func(bus.UserUpdated) { v.refreshBanner() })
}

func (v *View) onTextMessage(ev bus.TextMessage) {
	kind := rowMessage
	if len(ev.UserSessions) > 0 {
		kind = rowPrivate
	}
	if ev.Sender == 0 {
		// Session 0 is never a user: the server itself sent this.
		kind = rowServer
	}
	content := PlainText(ev.Message)
	if content == "" {
		// Fully stripped (e.g. an image-only message): nothing to render.
		return
	}
	v.appendRow(rowStyle{
		kind:    kind,
		sender:  v.userName(ev.Sender),
		at:      time.Now(),
		content: content,
	}, true)
}

// send is the composer submit handler: resolve the current target, hand the
// text to the Sender seam, and mirror it locally (Mumble does not echo own
// messages back to the sender).
func (v *View) send(text string) {
	if v.sender == nil {
		v.appendRow(rowStyle{kind: rowError, at: time.Now(),
			content: errorRowText(tr("Not connected."))}, false)
		return
	}
	st := v.bus.State()
	kind := rowMessage
	var channelID, session uint32
	if v.target.mode == targetUser {
		session = v.target.session
		kind = rowPrivate
	} else {
		channelID = v.target.resolvedChannelID(selfChannel(st))
	}
	if err := v.sender.SendText(channelID, session, text); err != nil {
		v.appendRow(rowStyle{kind: rowError, at: time.Now(),
			content: errorRowText(err.Error())}, false)
		return
	}
	v.appendRow(rowStyle{kind: kind, sender: selfName(st), at: time.Now(),
		content: text}, false)
}

// appendRow adds one rendered row, enforces the scrollback cap, and keeps
// the view pinned to the bottom while anchored. fromOthers drives the
// jump-to-latest indicator when the user scrolled up.
func (v *View) appendRow(st rowStyle, fromOthers bool) {
	wasAnchored := v.atBottom()
	row := newRow(st)
	v.list.Append(row)
	if dropped, ok := v.hist.push(row); ok {
		v.list.Remove(dropped)
	}
	v.pin = wasAnchored
	if wasAnchored {
		v.scrollToBottom()
	} else if fromOthers {
		v.jump.SetRevealChild(true)
	}
}

func (v *View) atBottom() bool {
	adj := v.scroll.VAdjustment()
	return anchored(adj.Value(), adj.Upper(), adj.PageSize())
}

func (v *View) scrollToBottom() {
	adj := v.scroll.VAdjustment()
	adj.SetValue(adj.Upper() - adj.PageSize())
}

// refreshBanner recomputes the target banner from the bus state snapshot.
func (v *View) refreshBanner() {
	st := v.bus.State()
	chName, uName := "", ""
	if v.target.mode == targetUser {
		if u, ok := st.Users[v.target.session]; ok {
			uName = u.Name
		}
	} else if ch, ok := st.Channels[v.target.resolvedChannelID(selfChannel(st))]; ok {
		chName = ch.Name
	}
	v.banner.SetText(v.target.bannerText(chName, uName))
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

// selfChannel returns the local user's channel (0 while unknown).
func selfChannel(st bus.State) uint32 {
	return st.Users[st.SelfSession].Channel
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
