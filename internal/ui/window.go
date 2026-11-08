package ui

import (
	"context"
	"net"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/settings"
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

// Slot is a mount point where a later task installs its region widget:
// the sidebar slot is filled by the channel tree (T16), the chat slot by
// the conversation view (T17). The shell ships placeholder content in each
// slot; Set replaces it. Main loop only.
type Slot struct {
	holder *gtk.Box
	child  gtk.Widgetter
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
	// adminMenu holds the server-admin entries; filled while connected.
	adminMenu *gio.Menu
	// quitConfirmed skips the quit confirmation once the user agreed.
	quitConfirmed bool
}

func newWindow(app *App) *Window {
	ctx, cancel := context.WithCancel(app.ctx)
	w := &Window{
		app:    app,
		ctx:    ctx,
		cancel: cancel,
	}
	w.ApplicationWindow = adw.NewApplicationWindow(&app.Application.Application)
	w.SetTitle(tr("Murmel"))
	w.SetIconName(settings.SchemaID)
	w.SetSizeRequest(minWindowWidth, minWindowHeight)

	w.status = newStatusBar(app.settings, app.bus)
	w.sidebar = newSlot()
	w.chat = newSlot()

	// --- Sidebar page: channel tree with the primary menu in its header.
	w.chatBtn = gtk.NewButtonFromIconName("go-next-symbolic")
	w.chatBtn.SetTooltipText(tr("Show chat"))
	w.chatBtn.SetVisible(false)
	w.chatBtn.ConnectClicked(func() { w.split.SetShowContent(true) })

	// The header shows the server name as its title and the connection
	// dot in the top-left corner: as far from the left edge as from the
	// top, so it lines up vertically with the title (status-dot-corner).
	w.serverTitle = adw.NewWindowTitle(tr("Murmel"), "")
	sidebarHeader := adw.NewHeaderBar()
	sidebarHeader.SetTitleWidget(w.serverTitle)
	w.status.dotHolder.AddCSSClass("status-dot-corner")
	w.status.dotHolder.SetVAlign(gtk.AlignCenter)
	sidebarHeader.PackStart(w.status.dotHolder)
	menuBtn, adminMenu := newPrimaryMenuButton()
	w.adminMenu = adminMenu
	sidebarHeader.PackEnd(menuBtn)
	sidebarHeader.PackEnd(w.chatBtn)

	sidebarToolbar := adw.NewToolbarView()
	sidebarToolbar.AddTopBar(sidebarHeader)
	sidebarToolbar.SetContent(w.sidebar.holder)
	sidebarToolbar.AddBottomBar(w.status.root)

	w.sidebarPage = adw.NewNavigationPage(sidebarToolbar, tr("Murmel"))
	w.sidebarPage.SetTag("sidebar")

	// --- Content page: empty state ↔ chat. The chat brings its own
	// headers (one per conversation), the empty state gets a plain one.
	emptyHeader := adw.NewHeaderBar()
	emptyHeader.SetTitleWidget(adw.NewWindowTitle(tr("Murmel"), ""))
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
	w.narrowMute.SetTooltipText(tr("Mute microphone"))
	w.narrowMute.SetVisible(false)
	syncMute := func(from, to *gtk.ToggleButton) {
		if to.Active() != from.Active() {
			to.SetActive(from.Active())
		}
		to.SetIconName(from.IconName())
	}
	w.status.selfMute.ConnectToggled(func() { syncMute(w.status.selfMute, w.narrowMute) })
	w.narrowMute.ConnectToggled(func() { syncMute(w.narrowMute, w.status.selfMute) })

	w.contentPage = adw.NewNavigationPage(w.stack, tr("Murmel"))
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

// newPrimaryMenuButton builds the GNOME primary ("hamburger") menu. The
// returned admin section starts empty: setAdminMenu fills it while
// connected. Disconnect is hidden while there is no connection.
func newPrimaryMenuButton() (*gtk.MenuButton, *gio.Menu) {
	admin := gio.NewMenu()

	general := gio.NewMenu()
	general.Append(tr("Preferences"), "app.preferences")
	general.Append(tr("About Murmel"), "app.about")
	disconnect := gio.NewMenuItem(tr("Disconnect"), "app.disconnect")
	disconnect.SetAttributeValue("hidden-when", glib.NewVariantString("action-disabled"))
	general.AppendItem(disconnect)
	general.Append(tr("Quit"), "app.quit")

	menu := gio.NewMenu()
	menu.AppendSection("", admin)
	menu.AppendSection("", general)

	btn := gtk.NewMenuButton()
	btn.SetIconName("open-menu-symbolic")
	btn.SetTooltipText(tr("Main Menu"))
	btn.SetMenuModel(menu)
	btn.SetPrimary(true)
	return btn, admin
}

// setAdminMenu shows the server-admin entries only while connected; the
// app actions grey them out when the server does not grant the rights.
func (w *Window) setAdminMenu(connected bool) {
	if !connected {
		w.adminMenu.RemoveAll()
		return
	}
	if w.adminMenu.NItems() == 0 {
		w.adminMenu.Append(tr("Ban List"), "app.banlist")
		w.adminMenu.Append(tr("Registered Users"), "app.userlist")
	}
}

// confirmQuit asks before quitting while connected; agreeing leaves the
// server and closes the window.
func (w *Window) confirmQuit() {
	alert := adw.NewAlertDialog(tr("Quit Murmel?"),
		tr("You are still connected to a server. Quitting ends the connection."))
	alert.AddResponse("cancel", tr("Cancel"))
	alert.AddResponse("quit", tr("Quit"))
	alert.SetResponseAppearance("quit", adw.ResponseDestructive)
	alert.SetDefaultResponse("cancel")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(response string) {
		if response != "quit" {
			return
		}
		w.quitConfirmed = true
		w.app.bus.Emit(bus.DisconnectRequested{})
		w.Close()
	})
	alert.Present(w)
}

// Sidebar returns the sidebar region slot (channel tree, T16).
func (w *Window) Sidebar() *Slot { return w.sidebar }

// Chat returns the chat region slot (conversation view, T17).
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
	title := tr("Murmel")
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
	connect := gtk.NewButtonWithLabel(tr("Connect…"))
	connect.AddCSSClass("suggested-action")
	connect.AddCSSClass("pill")
	connect.ConnectClicked(func() { app.RequestConnect(ConnectRequest{}) })

	page := adw.NewStatusPage()
	page.SetIconName(settings.SchemaID)
	page.SetTitle(tr("Not connected"))
	page.SetDescription(tr("Join a Mumble server to start talking."))
	page.SetChild(connect)
	return page
}

func (w *Window) installPlaceholders() {
	channels := adw.NewStatusPage()
	channels.SetTitle(tr("Channels"))
	channels.SetDescription(tr("Connect to a server to browse its channels."))
	w.sidebar.Set(channels)
}

// subscribe wires every shell state change to a bus subscription; the
// window context cancellation tears them all down (SubscribeCtx).
func (w *Window) subscribe() {
	bus.SubscribeCtx(w.ctx, w.app.bus, func(bus.Connected) {
		w.stack.SetVisibleChildName("session")
		w.status.setConnected()
		w.connected = true
		w.setAdminMenu(true)
		w.showPrimary()
	})
	bus.SubscribeCtx(w.ctx, w.app.bus, func(bus.Disconnected) {
		w.stack.SetVisibleChildName("empty")
		w.status.setDisconnected()
		w.connected = false
		w.setAdminMenu(false)
		w.setServerTitle("")
		w.showPrimary()
		w.status.SetSelfMuted(false)
		w.status.SetSelfDeafened(false)
	})
	bus.SubscribeCtx(w.ctx, w.app.bus, func(ev bus.ConnectError) {
		w.stack.SetVisibleChildName("empty")
		w.status.setError(ev.Text)
		w.connected = false
		w.setAdminMenu(false)
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
		msg := tr("The microphone stopped delivering audio. Choose another input device.")
		if ev.Output {
			msg = tr("The output device is not playing audio. Choose another output device.")
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
			w.status.setInfo(ev)
			w.setServerTitle(ev.Address)
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

	// Quick switcher (T26): the action and accelerator exist so the
	// shortcut is reserved; the handler is filled in later.
	quickSwitcher := gio.NewSimpleAction("quick-switcher", nil)
	quickSwitcher.ConnectActivate(func(*glib.Variant) {})
	w.AddAction(quickSwitcher)
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
