package ui

import (
	"context"
	"net"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/settings"
)

const (
	// narrowBreakpointPx is the documented narrow-mode threshold: window
	// widths at or below it collapse the NavigationSplitView so sidebar and
	// content become a drill-down navigation stack (adw.Breakpoint,
	// MaxWidth condition).
	narrowBreakpointPx = 600

	defaultWindowWidth  = 900
	defaultWindowHeight = 700
	minWindowWidth      = 360
	minWindowHeight     = 360

	// Sidebar width bounds (px) for the split view; the sidebar takes
	// sidebarWidthFraction of the window between these limits.
	minSidebarWidth      = 220
	maxSidebarWidth      = 340
	sidebarWidthFraction = 0.3
)

// Slot is a mount point where cmd/murmel installs a region widget:
// the sidebar slot is filled by the channel tree, the chat slot by
// the conversation view. The shell ships placeholder content in each
// slot; Set replaces it. Main loop only.
type Slot struct {
	holder *gtk.Box
	child  gtk.Widgetter
	// focus moves the keyboard focus into the content (see SetFocus).
	focus func() bool
}

func newSlot() *Slot {
	holder := gtk.NewBox(gtk.OrientationVertical, 0)
	holder.SetVExpand(true)
	holder.SetHExpand(true)
	return &Slot{holder: holder}
}

// Set replaces the slot content, removing any previous widget first.
func (s *Slot) Set(child gtk.Widgetter) {
	if s.child != nil {
		s.holder.Remove(s.child)
	}
	s.child = child
	if child != nil {
		s.holder.Append(child)
	}
}

// SetFocus gives the slot the function that moves the keyboard focus
// into its content and reports whether it did (F6 and Shift+F6).
func (s *Slot) SetFocus(focus func() bool) { s.focus = focus }

// contains reports whether w lies inside the slot.
func (s *Slot) contains(w gtk.Widgetter) bool {
	return w != nil && (sameWidget(w, s.holder) || gtk.BaseWidget(w).IsAncestor(s.holder))
}

func sameWidget(a, b gtk.Widgetter) bool {
	return coreglib.BaseObject(a).Native() == coreglib.BaseObject(b).Native()
}

// Window is the main application window: an adw.NavigationSplitView with
// the channel sidebar on the start side and the conversation (or the empty
// state) as content. The voice status bar sits at the bottom of the content
// page. Below narrowBreakpointPx the split view collapses into a navigation
// stack (sidebar first, content pushed on top).
type Window struct {
	*adw.ApplicationWindow

	app    *App
	ctx    context.Context
	cancel context.CancelFunc

	split       *adw.NavigationSplitView
	toasts      *adw.ToastOverlay
	sidebarPage *adw.NavigationPage
	contentPage *adw.NavigationPage
	chatBtn     *gtk.Button
	// serverTitle shows the connected server's name in the sidebar header.
	serverTitle *adw.WindowTitle
	// narrowMute is a mute toggle for the chat header in the narrow
	// layout, where the sidebar's voice controls are out of sight.
	narrowMute *gtk.ToggleButton

	sidebar *Slot
	chat    *Slot
	stack   *gtk.Stack
	status  *statusBar

	connected bool
	shortcuts *globalShortcuts
	// quitConfirmed skips the quit confirmation once the user agreed.
	quitConfirmed bool
	// quitAsking is set while the quit confirmation is shown.
	quitAsking bool
}

func newWindow(app *App) *Window {
	ctx, cancel := context.WithCancel(app.ctx)
	w := &Window{
		app:    app,
		ctx:    ctx,
		cancel: cancel,
	}
	w.ApplicationWindow = adw.NewApplicationWindow(&app.Application.Application)
	// TRANSLATORS: the application name, do not translate.
	w.SetTitle(i18n.Tr("Murmel"))
	w.SetIconName(settings.SchemaID)
	w.SetSizeRequest(minWindowWidth, minWindowHeight)

	w.status = newStatusBar(app.settings, app.bus)
	w.shortcuts = newGlobalShortcuts(w)
	w.sidebar = newSlot()
	w.chat = newSlot()

	// --- Sidebar page: channel tree with the primary menu in its header.
	w.chatBtn = gtk.NewButtonFromIconName("go-next-symbolic")
	w.chatBtn.SetTooltipText(i18n.Tr("Show chat"))
	w.chatBtn.SetVisible(false)
	w.chatBtn.ConnectClicked(func() { w.split.SetShowContent(true) })

	// The header shows the server name as its title and the connection
	// dot in the top-left corner: as far from the left edge as from the
	// top, so it lines up vertically with the title (status-dot-corner).
	w.serverTitle = adw.NewWindowTitle(i18n.Tr("Murmel"), "")
	sidebarHeader := adw.NewHeaderBar()
	sidebarHeader.SetTitleWidget(w.serverTitle)
	w.status.dotHolder.AddCSSClass("status-dot-corner")
	w.status.dotHolder.SetVAlign(gtk.AlignCenter)
	sidebarHeader.PackStart(w.status.dotHolder)
	menuBtn := newPrimaryMenuButton()
	sidebarHeader.PackEnd(menuBtn)
	sidebarHeader.PackEnd(w.chatBtn)

	sidebarToolbar := adw.NewToolbarView()
	sidebarToolbar.AddTopBar(sidebarHeader)
	sidebarToolbar.SetContent(w.sidebar.holder)
	sidebarToolbar.AddBottomBar(w.status.root)

	w.sidebarPage = adw.NewNavigationPage(sidebarToolbar, i18n.Tr("Murmel"))
	w.sidebarPage.SetTag("sidebar")

	// --- Content page: empty state ↔ chat. The chat brings its own
	// headers (one per conversation), the empty state gets a plain one.
	emptyHeader := adw.NewHeaderBar()
	emptyHeader.SetTitleWidget(adw.NewWindowTitle(i18n.Tr("Murmel"), ""))
	emptyToolbar := adw.NewToolbarView()
	emptyToolbar.AddTopBar(emptyHeader)
	emptyToolbar.SetContent(newEmptyState(app))

	w.stack = gtk.NewStack()
	w.stack.SetTransitionType(gtk.StackTransitionTypeCrossfade)
	w.stack.AddNamed(emptyToolbar, "empty")
	w.stack.AddNamed(w.chat.holder, "session")

	// Narrow layout: a mute toggle for the chat header, mirroring the
	// sidebar's microphone button.
	w.narrowMute = gtk.NewToggleButton()
	w.narrowMute.SetIconName("audio-input-microphone-symbolic")
	w.narrowMute.SetTooltipText(i18n.Tr("Mute microphone"))
	w.narrowMute.SetVisible(false)
	syncMute := func(from, to *gtk.ToggleButton) {
		if to.Active() != from.Active() {
			to.SetActive(from.Active())
		}
		to.SetIconName(from.IconName())
	}
	w.status.selfMute.ConnectToggled(func() { syncMute(w.status.selfMute, w.narrowMute) })
	w.narrowMute.ConnectToggled(func() { syncMute(w.narrowMute, w.status.selfMute) })

	w.contentPage = adw.NewNavigationPage(w.stack, i18n.Tr("Murmel"))
	w.contentPage.SetTag("content")

	// --- Split view.
	w.split = adw.NewNavigationSplitView()
	w.split.SetSidebar(w.sidebarPage)
	w.split.SetContent(w.contentPage)
	w.split.SetMinSidebarWidth(minSidebarWidth)
	w.split.SetMaxSidebarWidth(maxSidebarWidth)
	w.split.SetSidebarWidthFraction(sidebarWidthFraction)
	// While disconnected the content page carries the Connect button, so
	// that is what a collapsed window shows first.
	w.split.SetShowContent(true)
	w.split.AddCSSClass("split-wide")
	w.toasts = adw.NewToastOverlay()
	w.toasts.SetChild(w.split)
	w.SetContent(w.toasts)

	w.AddBreakpoint(w.newNarrowBreakpoint())
	w.registerActions()
	w.restoreGeometry()
	w.subscribe()
	w.installPlaceholders()
	w.ConnectCloseRequest(func() bool {
		if w.connected && !w.quitConfirmed {
			w.confirmQuit()
			return true // keep the window until the user decides
		}
		w.saveGeometry()
		cancel()
		return false
	})
	return w
}

// newPrimaryMenuButton builds the GNOME primary ("hamburger") menu in three
// sections: the connection, server administration and the app. Entries
// whose action is disabled are hidden: Disconnect while there is no
// connection, the administration entries without a connection or the
// right for them (an empty section shows no separator).
func newPrimaryMenuButton() *gtk.MenuButton {
	// Quit is no menu entry (GNOME HIG); Ctrl+Q and closing the window
	// remain.
	connection := gio.NewMenu()
	connection.Append(i18n.Tr("Connect to Server…"), "app.connect")
	disconnect := gio.NewMenuItem(i18n.Tr("Disconnect"), "app.disconnect")
	disconnect.SetAttributeValue("hidden-when", glib.NewVariantString("action-disabled"))
	connection.AppendItem(disconnect)

	admin := gio.NewMenu()
	for _, e := range []struct{ label, action string }{
		{i18n.Tr("Ban List"), "app.banlist"},
		{i18n.Tr("Registered Users"), "app.userlist"},
	} {
		item := gio.NewMenuItem(e.label, e.action)
		item.SetAttributeValue("hidden-when", glib.NewVariantString("action-disabled"))
		admin.AppendItem(item)
	}

	general := gio.NewMenu()
	general.Append(i18n.Tr("Preferences"), "app.preferences")
	general.Append(i18n.Tr("Keyboard Shortcuts"), "app.shortcuts")
	general.Append(i18n.Tr("About Murmel"), "app.about")

	menu := gio.NewMenu()
	menu.AppendSection("", connection)
	menu.AppendSection("", admin)
	menu.AppendSection("", general)

	btn := gtk.NewMenuButton()
	btn.SetIconName("open-menu-symbolic")
	btn.SetTooltipText(i18n.Tr("Main Menu"))
	btn.SetMenuModel(menu)
	btn.SetPrimary(true)
	return btn
}

// quit closes the window and with it the application (app.quit, from
// Ctrl+Q or D-Bus). While connected it asks first, above any open dialog.
// Open dialogs are closed first: closing the window while a dialog is open
// would only close the topmost dialog.
func (w *Window) quit() {
	if w.connected && !w.quitConfirmed {
		if !w.quitAsking {
			w.confirmQuit()
		}
		return
	}
	w.closeDialogs()
	w.Close()
}

// closeDialogs closes the dialogs open on the window, topmost first.
func (w *Window) closeDialogs() {
	for range w.Dialogs().NItems() {
		d := w.VisibleDialog()
		if d == nil {
			return
		}
		d.ForceClose()
	}
}

// confirmQuit asks before quitting while connected; agreeing leaves the
// server and closes the window.
func (w *Window) confirmQuit() {
	alert := adw.NewAlertDialog(i18n.Tr("Quit Murmel?"),
		i18n.Tr("You are still connected to a server. Quitting ends the connection."))
	alert.AddResponse("cancel", i18n.Tr("Cancel"))
	alert.AddResponse("quit", i18n.Tr("Quit"))
	alert.SetResponseAppearance("quit", adw.ResponseDestructive)
	alert.SetDefaultResponse("cancel")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(response string) {
		w.quitAsking = false
		if response != "quit" {
			return
		}
		w.quitConfirmed = true
		w.app.bus.Emit(bus.DisconnectRequested{})
		w.closeDialogs()
		w.Close()
	})
	w.quitAsking = true
	alert.Present(w)
}

// Sidebar returns the sidebar region slot (channel tree).
func (w *Window) Sidebar() *Slot { return w.sidebar }

// Chat returns the chat region slot (conversation view).
func (w *Window) Chat() *Slot { return w.chat }

func (w *Window) newNarrowBreakpoint() *adw.Breakpoint {
	bp := adw.NewBreakpoint(adw.NewBreakpointConditionLength(
		adw.BreakpointConditionMaxWidth, narrowBreakpointPx, adw.LengthUnitPx))
	bp.ConnectApply(func() { w.setCollapsed(true) })
	bp.ConnectUnapply(func() { w.setCollapsed(false) })
	return bp
}

func (w *Window) setCollapsed(collapsed bool) {
	if w.split.Collapsed() == collapsed {
		return
	}
	w.split.SetCollapsed(collapsed)
	// The column separator (style.css) only belongs to the wide layout.
	if collapsed {
		w.split.RemoveCSSClass("split-wide")
	} else {
		w.split.AddCSSClass("split-wide")
	}
	w.chatBtn.SetVisible(collapsed && w.connected)
	w.narrowMute.SetVisible(collapsed)
}

// NarrowMuteButton is the microphone toggle the chat shows in its header
// while the window is narrow (sidebar voice controls out of sight).
func (w *Window) NarrowMuteButton() gtk.Widgetter { return w.narrowMute }

// setServerTitle shows the server in the sidebar header ("Murmel" while
// disconnected).
func (w *Window) setServerTitle(address string) {
	title := i18n.Tr("Murmel")
	if address != "" {
		title = address
		if host, _, err := net.SplitHostPort(address); err == nil {
			title = host
		}
	}
	w.serverTitle.SetTitle(title)
	w.sidebarPage.SetTitle(title)
}

// showPrimary picks the page a collapsed split view shows: the channel list
// while connected, the empty state (with its Connect button) otherwise. In
// the wide layout both pages are visible and this only records the choice.
func (w *Window) showPrimary() {
	w.split.SetShowContent(!w.connected)
	w.chatBtn.SetVisible(w.split.Collapsed() && w.connected)
}

func newEmptyState(app *App) gtk.Widgetter {
	connect := gtk.NewButtonWithLabel(i18n.Tr("Connect…"))
	connect.AddCSSClass("suggested-action")
	connect.AddCSSClass("pill")
	connect.SetActionName("app.connect")

	page := adw.NewStatusPage()
	page.SetIconName(settings.SchemaID)
	page.SetTitle(i18n.Tr("Not connected"))
	page.SetDescription(i18n.Tr("Join a Mumble server to start talking."))
	page.SetChild(connect)
	return page
}

func (w *Window) installPlaceholders() {
	channels := adw.NewStatusPage()
	channels.SetTitle(i18n.Tr("Channels"))
	channels.SetDescription(i18n.Tr("Connect to a server to browse its channels."))
	w.sidebar.Set(channels)
}

// subscribe wires every shell state change to a bus subscription; the
// window context cancellation tears them all down (SubscribeCtx).
func (w *Window) subscribe() {
	context.AfterFunc(w.ctx, func() {
		glib.IdleAdd(w.status.stopIdleMic)
	})
	// The first ConnectionInfo of a connection can overtake its Connected;
	// it is kept until then (a new attempt or its end drops it).
	var earlyInfo *bus.ConnectionInfo
	showInfo := func(ev bus.ConnectionInfo) {
		w.status.setInfo(ev)
		w.setServerTitle(ev.Address)
	}
	bus.SubscribeCtx(w.ctx, w.app.bus, func(bus.Connected) {
		w.stack.SetVisibleChildName("session")
		w.status.setConnected()
		w.connected = true
		w.showPrimary()
		if earlyInfo != nil {
			showInfo(*earlyInfo)
			earlyInfo = nil
		}
	})
	bus.SubscribeCtx(w.ctx, w.app.bus, func(bus.ConnectionStarting) { earlyInfo = nil })
	bus.SubscribeCtx(w.ctx, w.app.bus, func(bus.Disconnected) {
		earlyInfo = nil
		w.stack.SetVisibleChildName("empty")
		w.status.setDisconnected()
		w.connected = false
		w.setServerTitle("")
		w.showPrimary()
		w.status.SetSelfMuted(false)
		w.status.SetSelfDeafened(false)
	})
	bus.SubscribeCtx(w.ctx, w.app.bus, func(ev bus.ConnectError) {
		earlyInfo = nil
		w.stack.SetVisibleChildName("empty")
		w.status.setError(ev.Text)
		w.connected = false
		w.setServerTitle("")
		w.showPrimary()
	})
	// Picking a chat target in the channel tree reveals the conversation
	// when the split view is collapsed (no-op in the wide layout).
	bus.SubscribeCtx(w.ctx, w.app.bus, func(bus.ChatTargetChanged) {
		if w.split.Collapsed() {
			w.split.SetShowContent(true)
		}
	})
	bus.SubscribeCtx(w.ctx, w.app.bus, func(ev bus.AudioDeviceProblem) {
		msg := i18n.Tr("The microphone stopped delivering audio. Choose another input device.")
		if ev.Output {
			msg = i18n.Tr("The output device is not playing audio. Choose another output device.")
		}
		toast := adw.NewToast(msg)
		toast.SetTimeout(8)
		w.toasts.AddToast(toast)
	})
	bus.SubscribeCtx(w.ctx, w.app.bus, func(ev bus.InputLevel) {
		w.status.pushLevel(ev)
	})
	bus.SubscribeCtx(w.ctx, w.app.bus, func(ev bus.ConnectionInfo) {
		if w.connected {
			showInfo(ev)
		} else {
			earlyInfo = &ev
		}
	})
	// Reflect server-confirmed self-mute/deafen state.
	bus.SubscribeCtx(w.ctx, w.app.bus, func(ev bus.UserUpdated) {
		if ev.Changed.Has(bus.UserChangeAudio) {
			state := w.app.bus.State()
			if ev.User.Session == state.SelfSession {
				w.status.SetSelfMuted(ev.User.SelfMuted)
				w.status.SetSelfDeafened(ev.User.SelfDeafened)
			}
		}
	})
	// Status bar mute/deafen toggles emit bus events so the controller can
	// translate them into protocol calls.
	w.status.selfMute.ConnectToggled(func() {
		w.app.bus.Emit(bus.SelfMuteToggle{Muted: w.status.SelfMuted()})
	})
	w.status.selfDeafen.ConnectToggled(func() {
		w.app.bus.Emit(bus.SelfDeafenToggle{Deafened: w.status.SelfDeafened()})
	})
}

func (w *Window) registerActions() {
	fullscreen := gio.NewSimpleAction("fullscreen", nil)
	fullscreen.ConnectActivate(func(*glib.Variant) {
		if w.IsFullscreen() {
			w.Unfullscreen()
		} else {
			w.Fullscreen()
		}
	})
	w.AddAction(fullscreen)

	pane := gio.NewSimpleAction("switch-pane", nil)
	pane.ConnectActivate(func(*glib.Variant) { w.switchPane() })
	w.AddAction(pane)
}

// switchPane moves the keyboard focus between the channel list and the
// message field (F6, Shift+F6): from the chat to the list, from anywhere
// else to the chat. A narrow window shows the page it moves to. Nothing
// happens while not connected or while a dialog is open.
func (w *Window) switchPane() {
	if !w.connected || w.VisibleDialog() != nil {
		return
	}
	target := w.chat
	toChat := !w.chat.contains(w.Focus())
	if !toChat {
		target = w.sidebar
	}
	if target.focus == nil {
		return
	}
	if w.split.Collapsed() {
		w.split.SetShowContent(toChat)
	}
	target.focus()
}

func (w *Window) restoreGeometry() {
	width := w.app.settings.WindowWidth()
	height := w.app.settings.WindowHeight()
	if width < minWindowWidth || height < minWindowHeight {
		width, height = defaultWindowWidth, defaultWindowHeight
	}
	w.SetDefaultSize(width, height)
	if w.app.settings.WindowMaximized() {
		w.Maximize()
	}
}

// saveGeometry persists the session geometry. When maximized, only the flag
// is written so the last normal size survives for the unmaximized restore.
func (w *Window) saveGeometry() {
	if w.IsMaximized() {
		w.app.settings.SetWindowMaximized(true)
		return
	}
	w.app.settings.SetWindowWidth(w.Width())
	w.app.settings.SetWindowHeight(w.Height())
	w.app.settings.SetWindowMaximized(false)
}

// GlobalShortcuts is the window's global shortcuts, for the preferences.
func (w *Window) GlobalShortcuts() *globalShortcuts { return w.shortcuts }
