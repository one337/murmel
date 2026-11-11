package ui

import (
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui/presence"
)

// Notification identifiers. PM notifications use one id per sender so a
// burst from the same peer replaces its previous notification instead of
// piling up (GNOME HIG); the reconnect notification is a singleton.
const (
	notificationIDPM        = "pm-"
	notificationIDReconnect = "reconnect"
	// notificationIDPresence + session: join/leave notifications, one per
	// user so a quick leave-and-rejoin replaces itself.
	notificationIDPresence = "presence-"
	// openChatActionName is the app-level action activated when a PM
	// notification's default action fires. GIO requires the "app." prefix
	// for notification actions.
	openChatActionName = "app.open-chat"
)

// previewMaxRunes bounds the message preview body; longer messages are
// truncated at a rune boundary with an ellipsis.
const previewMaxRunes = 120

// notifier implements the desktop notifications, each with its own
// setting: private messages, users joining or leaving (the channel or the
// server), and a restored connection after a drop. All of them only while
// the window is not in the foreground; whether notifications show at all
// is the desktop's setting. Channel messages never notify.
//
// Everything GTK-touching is funnelled through the sendNotification /
// windowActive seams so the decision logic is testable without a display.
type notifier struct {
	app      *App
	settings settings.Settings
	bus      *bus.Bus

	// sendNotification delivers (id, title, body) and wires target (the
	// open-chat action argument, "" for none) as the default action.
	sendNotification func(id, title, body, target string)
	// windowActive reports whether the main window has focus; nil window
	// counts as unfocused — a hidden app may not miss a PM.
	windowActive func() bool

	// wasDropped is true between an error-disconnect and the next
	// Connected event; that transition is the "reconnected" trigger.
	wasDropped bool
}

// newNotifier builds the notifier with its production seams. It subscribes
// immediately; handlers consult the app/window state lazily, so creating it
// before the first window is fine.
func newNotifier(app *App) *notifier {
	n := &notifier{
		app:      app,
		settings: app.settings,
		bus:      app.bus,
		windowActive: func() bool {
			return app.window != nil && app.window.IsActive()
		},
	}
	n.sendNotification = n.sendViaGio
	n.subscribe()
	return n
}

// subscribe wires the three triggers; the app context tears them down.
func (n *notifier) subscribe() {
	bus.SubscribeCtx(n.app.ctx, n.bus, n.onTextMessage)
	bus.SubscribeCtx(n.app.ctx, n.bus, n.onConnected)
	bus.SubscribeCtx(n.app.ctx, n.bus, n.onDisconnected)
	n.app.presence.Listen(n.onPresence)
}

// onTextMessage notifies on private messages received while unfocused.
func (n *notifier) onTextMessage(ev bus.TextMessage) {
	if !n.settings.NotifyPrivateMessages() {
		return
	}
	state := n.bus.State()
	if ev.Sender == state.SelfSession {
		return // never notify for own (echoed) messages
	}
	if !isPrivateMessage(ev, state.SelfSession) {
		return
	}
	if n.windowActive() {
		return
	}

	sender := "session-" + strconv.FormatUint(uint64(ev.Sender), 10)
	if u, ok := state.Users[ev.Sender]; ok && u.Name != "" {
		sender = u.Name
	}
	target := strconv.FormatUint(uint64(ev.Sender), 10)
	n.sendNotification(
		notificationIDPM+target,
		fmt.Sprintf(tr("Private message from %s"), sender),
		messagePreview(ev.Message),
		target,
	)
}

// onConnected clears the drop marker and — after a drop — announces the
// restored connection.
func (n *notifier) onConnected(bus.Connected) {
	dropped := n.wasDropped
	n.wasDropped = false
	if !dropped || !n.settings.NotifyReconnect() || n.windowActive() {
		return
	}
	n.sendNotification(
		notificationIDReconnect,
		tr("Reconnected"),
		tr("The connection to the server was restored."),
		"",
	)
}

// onDisconnected arms the reconnect notification only for dropped
// connections; user-initiated disconnects and kick/ban are silent here
// (the status bar covers them).
func (n *notifier) onDisconnected(ev bus.Disconnected) {
	if ev.Reason == bus.DisconnectError {
		n.wasDropped = true
	}
}

// onPresence turns presence events into join/leave notifications. A
// connect/disconnect is announced as a server event when those are
// enabled, otherwise as a channel event if it happened in our channel.
func (n *notifier) onPresence(ev presence.Event) {
	if n.windowActive() {
		return // the channel tree shows it
	}
	u := ev.User
	switch ev.Kind {
	case presence.UserConnected:
		switch {
		case n.wantServerPresence():
			n.sendPresence(u, fmt.Sprintf(tr("%s connected"), u.Name), n.channelLabel(tr("In %s"), ev.To))
		case ev.InMyChannel && n.wantChannelPresence():
			n.sendPresence(u, fmt.Sprintf(tr("%s joined your channel"), u.Name), n.channelLabel(tr("In %s"), ev.To))
		}
	case presence.UserDisconnected:
		switch {
		case n.wantServerPresence():
			n.sendPresence(u, fmt.Sprintf(tr("%s disconnected"), u.Name), ev.Reason)
		case ev.InMyChannel && n.wantChannelPresence():
			n.sendPresence(u, fmt.Sprintf(tr("%s left your channel"), u.Name), ev.Reason)
		}
	case presence.UserJoinedChannel:
		if n.wantChannelPresence() {
			n.sendPresence(u, fmt.Sprintf(tr("%s joined your channel"), u.Name), n.channelLabel(tr("From %s"), ev.From))
		}
	case presence.UserLeftChannel:
		if n.wantChannelPresence() {
			n.sendPresence(u, fmt.Sprintf(tr("%s left your channel"), u.Name), n.channelLabel(tr("Moved to %s"), ev.To))
		}
	}
}

func (n *notifier) wantChannelPresence() bool {
	return n.settings.NotifyChannelPresence()
}

func (n *notifier) wantServerPresence() bool {
	return n.settings.NotifyServerPresence()
}

func (n *notifier) sendPresence(u bus.UserInfo, title, body string) {
	n.sendNotification(notificationIDPresence+strconv.FormatUint(uint64(u.Session), 10), title, body, "")
}

// channelName resolves a channel ID to its name ("" when unknown).
func (n *notifier) channelName(id uint32) string {
	return n.bus.State().Channels[id].Name
}

// channelLabel formats a notification body naming channel id ("" when
// the channel is unknown).
func (n *notifier) channelLabel(format string, id uint32) string {
	if name := n.channelName(id); name != "" {
		return fmt.Sprintf(format, name)
	}
	return ""
}

// sendViaGio is the production sendNotification seam: a gio.Notification
// (Flatpak-portal compatible — no libnotify) with the app icon and, for
// PMs, a default action that focuses the window and opens the chat.
func (n *notifier) sendViaGio(id, title, body, target string) {
	slog.Debug("notification", "id", id, "title", title, "body", body)
	notification := gio.NewNotification(title)
	notification.SetBody(body)
	notification.SetIcon(gio.NewThemedIcon(settings.SchemaID))
	if target != "" {
		notification.SetPriority(gio.NotificationPriorityHigh)
		notification.SetDefaultActionAndTarget(
			openChatActionName, glib.NewVariantString(target))
	} else {
		notification.SetPriority(gio.NotificationPriorityNormal)
	}
	n.app.SendNotification(id, notification)
}

// isPrivateMessage reports whether the message targets the local user
// directly (a PM): the recipient session list contains self. A session of 0
// never matches — sessions start at 1, so an unsynced store cannot produce
// false positives.
func isPrivateMessage(ev bus.TextMessage, self uint32) bool {
	if self == 0 {
		return false
	}
	for _, session := range ev.UserSessions {
		if session == self {
			return true
		}
	}
	return false
}

// messagePreview renders a chat message as a single-line notification body:
// HTML tags stripped, entities decoded, whitespace collapsed, truncated to
// previewMaxRunes at a rune boundary.
func messagePreview(msg string) string {
	var b strings.Builder
	b.Grow(min(len(msg), previewMaxRunes*4))

	inTag := false
	for _, r := range msg {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			if inTag {
				inTag = false
			} else {
				b.WriteRune(r)
			}
		case !inTag:
			b.WriteRune(r)
		}
	}

	preview := strings.Join(strings.Fields(html.UnescapeString(b.String())), " ")
	if preview == "" {
		return ""
	}
	if utf8.RuneCountInString(preview) <= previewMaxRunes {
		return preview
	}
	runes := []rune(preview)
	return string(runes[:previewMaxRunes-1]) + "…"
}

// openChat routes a notification activation: focus the window, then hand
// the sender session to the chat seam (OnOpenChat). Without a chat view the
// request is only logged.
func (a *App) openChat(session uint32) {
	slog.Info("open chat requested", "session", session)
	a.ensureWindow()
	a.window.Present()
	if a.onOpenChat != nil {
		a.onOpenChat(session)
	}
}
