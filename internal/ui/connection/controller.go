package connection

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/audio/engine"
	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/certs"
	"io.github/murmel/internal/protocol/gumble"
	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui"
	"io.github/murmel/internal/ui/secrets"
)

// Controller manages the gumble connection lifecycle:
//   - shows the connect dialog and wires the per-connection gumble client
//   - handles the TOFU certificate trust dialog on first connect / cert change
//   - shows actionable error dialogs classified by errorClass
//   - auto-reconnects on drop (exponential backoff, settings-gated)
//   - translates bus.ChannelJoinRequested into client.Self.Move(channel)
//
// All GTK work is main-loop-only; dial work happens on a goroutine and
// marshals back through glib.IdleAdd.
type Controller struct {
	win      gtk.Widgetter
	appBus   *bus.Bus
	settings settings.Settings
	certs    *certs.Manager
	appCtx   context.Context

	mu      sync.Mutex
	client  *gumble.Client
	connBus *bus.Bus
	stopFwd func()

	boff      backoff
	retryStop context.CancelFunc
	lastDial  *dialParams // saved for auto-reconnect

	voiceEngine *engine.Engine
	voiceCancel context.CancelFunc
}

// dialParams are the parameters for one dial attempt, saved so a reconnect
// can replay the same call without user interaction.
type dialParams struct {
	addr     string
	username string
	password string
	identity *certs.Identity
}

// NewController creates a Controller. win is the application window used as
// the parent for modal dialogs. ctx is the application lifetime; cancelling
// it tears the auto-reconnect loop down.
func NewController(ctx context.Context, win gtk.Widgetter, appBus *bus.Bus, st settings.Settings, m *certs.Manager) *Controller {
	c := &Controller{
		win:      win,
		appBus:   appBus,
		settings: st,
		certs:    m,
		appCtx:   ctx,
	}
	// Channel-join and user actions flow through the app bus.
	bus.SubscribeCtx(ctx, appBus, c.onJoinRequested)
	bus.SubscribeCtx(ctx, appBus, c.onSelfMuteToggle)
	bus.SubscribeCtx(ctx, appBus, c.onSelfDeafenToggle)
	bus.SubscribeCtx(ctx, appBus, c.onUserMoveRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserMuteRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserDeafenRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserPriorityRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserRegisterRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserStatsRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserKickRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserBanRequested)
	bus.SubscribeCtx(ctx, appBus, c.onChannelCreateRequested)
	bus.SubscribeCtx(ctx, appBus, c.onChannelDeleteRequested)
	bus.SubscribeCtx(ctx, appBus, c.onChannelRenameRequested)
	bus.SubscribeCtx(ctx, appBus, c.onBanListRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserListRequested)
	bus.SubscribeCtx(ctx, appBus, c.onBanListWriteRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserUnregisterRequested)
	bus.SubscribeCtx(ctx, appBus, c.onACLRequested)
	bus.SubscribeCtx(ctx, appBus, c.onACLSaveRequested)

	// Audio settings apply live to the running voice engine.
	disconnect := st.ConnectChanged(c.onSettingChanged)
	go func() { <-ctx.Done(); glib.IdleAdd(func() bool { disconnect(); return false }) }()
	return c
}

// OnConnectRequest is the handler installed on app.OnConnect. It is the
// entry point for both the empty-state "Connect…" button and mumble:// URL
// activations. Called on the main loop.
func (c *Controller) OnConnectRequest(req ui.ConnectRequest) {
	c.showConnectDialog(req)
}

// --- Connect dialog --------------------------------------------------------

// connectDialogWidth is wide enough for the longest row titles; the height
// follows the natural height of the form so every field is visible at once.
const connectDialogWidth = 440

// savedListMaxHeightPx shows about four saved connections before the list
// scrolls, so the form below stays visible.
const savedListMaxHeightPx = 240

func (c *Controller) showConnectDialog(req ui.ConnectRequest) {
	port := req.Port
	if port == 0 {
		port = ui.DefaultPort
	}

	dialog := adw.NewDialog()
	dialog.SetTitle(tr("Connect to Server"))
	dialog.SetContentWidth(connectDialogWidth)

	// --- Server group
	hostEntry := adw.NewEntryRow()
	hostEntry.SetTitle(tr("Address"))
	hostEntry.SetText(req.Host)

	portEntry := adw.NewEntryRow()
	portEntry.SetTitle(tr("Port"))
	portEntry.SetText(strconv.Itoa(port))
	portEntry.SetInputPurpose(gtk.InputPurposeDigits)

	passwordEntry := adw.NewPasswordEntryRow()
	passwordEntry.SetTitle(tr("Server Password"))
	passwordEntry.SetText(req.Password)

	group := adw.NewPreferencesGroup()
	group.SetTitle(tr("Server"))
	group.Add(hostEntry)
	group.Add(portEntry)
	group.Add(passwordEntry)

	// --- Account group
	userEntry := adw.NewEntryRow()
	userEntry.SetTitle(tr("Username"))
	userEntry.SetText(req.Username)

	// Certificate picker: "None" + stored identities. The import row below
	// adds a certificate file to the store and selects it.
	identities, _ := c.certs.List()
	identityStore := gtk.NewStringList([]string{tr("None (anonymous)")})
	for _, id := range identities {
		identityStore.Append(id.Name)
	}
	identityRow := adw.NewComboRow()
	identityRow.SetTitle(tr("User Certificate"))
	identityRow.SetModel(identityStore)
	selectIdentity := func(id string) {
		identityRow.SetSelected(0)
		for i, m := range identities {
			if m.ID == id {
				identityRow.SetSelected(uint(i + 1))
				return
			}
		}
	}
	selectIdentity(c.settings.LastIdentity())

	// Import button inside the certificate row keeps the form compact.
	importBtn := gtk.NewButtonFromIconName("document-open-symbolic")
	importBtn.AddCSSClass("flat")
	importBtn.SetVAlign(gtk.AlignCenter)
	importBtn.SetTooltipText(tr("Import certificate (PEM file with certificate and private key)"))
	importBtn.ConnectClicked(func() {
		c.pickCertificateFile(dialog, func(meta certs.IdentityMeta) {
			identities = append(identities, meta)
			identityStore.Append(meta.Name)
			identityRow.SetSelected(uint(len(identities)))
		})
	})
	identityRow.AddSuffix(importBtn)

	// --- Save switch: the name preview follows the form.
	saveRow := adw.NewSwitchRow()
	saveRow.SetTitle(tr("Save Connection"))
	saveRow.SetActive(true)
	formPort := func() int {
		p, err := strconv.Atoi(portEntry.Text())
		if err != nil || p <= 0 || p > 65535 {
			return ui.DefaultPort
		}
		return p
	}
	updateSaveName := func() {
		if strings.TrimSpace(hostEntry.Text()) == "" {
			saveRow.SetSubtitle(tr("Saved as username@address"))
			return
		}
		saveRow.SetSubtitle(fmt.Sprintf(tr("Saved as %s"),
			savedName(strings.TrimSpace(userEntry.Text()), strings.TrimSpace(hostEntry.Text()), formPort())))
	}
	hostEntry.ConnectChanged(updateSaveName)
	portEntry.ConnectChanged(updateSaveName)
	userEntry.ConnectChanged(updateSaveName)
	updateSaveName()

	userGroup := adw.NewPreferencesGroup()
	userGroup.SetTitle(tr("Account"))
	userGroup.Add(userEntry)
	userGroup.Add(identityRow)
	userGroup.Add(saveRow)

	// --- Saved connections: activating one fills the form (the password
	// comes from the keyring).
	fill := func(e settings.ServerEntry) {
		hostEntry.SetText(e.Host)
		portEntry.SetText(strconv.Itoa(e.Port))
		userEntry.SetText(e.Username)
		selectIdentity(e.IdentityID)
		passwordEntry.SetText("")
		saveRow.SetActive(true)
		key := secrets.Key{Host: e.Host, Port: e.Port, Username: e.Username}
		go func() {
			pw, err := secrets.Lookup(key)
			if err != nil {
				slog.Info("keyring lookup failed", "err", err)
				return
			}
			glib.IdleAdd(func() bool {
				// Only if the form still shows this account.
				if hostEntry.Text() == e.Host && userEntry.Text() == e.Username {
					passwordEntry.SetText(pw)
				}
				return false
			})
		}()
	}
	savedGroup := c.savedServersGroup(fill)

	box := gtk.NewBox(gtk.OrientationVertical, 18)
	box.SetMarginTop(12)
	box.SetMarginBottom(18)
	box.SetMarginStart(18)
	box.SetMarginEnd(18)
	if savedGroup != nil {
		box.Append(savedGroup)
	}
	box.Append(group)
	box.Append(userGroup)

	// The scrolled window only kicks in when the screen is too small; it
	// otherwise propagates the form's natural height to the dialog so the
	// whole form is visible without scrolling.
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(box)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetPropagateNaturalHeight(true)

	connectBtn := gtk.NewButtonWithLabel(tr("Connect"))
	connectBtn.AddCSSClass("suggested-action")
	cancelBtn := gtk.NewButtonWithLabel(tr("Cancel"))

	hdr := adw.NewHeaderBar()
	hdr.PackStart(cancelBtn)
	hdr.PackEnd(connectBtn)
	hdr.SetShowEndTitleButtons(false)
	hdr.SetShowStartTitleButtons(false)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(hdr)
	toolbar.SetContent(scroll)
	dialog.SetChild(toolbar)
	dialog.SetDefaultWidget(connectBtn)

	cancelBtn.ConnectClicked(func() { dialog.Close() })

	connectBtn.ConnectClicked(func() {
		h := strings.TrimSpace(hostEntry.Text())
		if h == "" {
			hostEntry.AddCSSClass("error")
			hostEntry.GrabFocus()
			return
		}
		p := formPort()
		user := strings.TrimSpace(userEntry.Text())
		if user == "" {
			user = "Murmel"
		}
		password := passwordEntry.Text()

		var identity *certs.Identity
		identityID := ""
		sel := int(identityRow.Selected())
		if sel > 0 && sel-1 < len(identities) {
			if id, lerr := c.certs.Load(identities[sel-1].ID); lerr == nil {
				identity = id
				identityID = identities[sel-1].ID
			}
		}
		c.settings.SetLastIdentity(identityID)

		if saveRow.Active() {
			c.saveServer(settings.ServerEntry{
				Name:       savedName(user, h, p),
				Host:       h,
				Port:       p,
				Username:   user,
				IdentityID: identityID,
			}, password)
		}

		dialog.Close()
		c.startDial(&dialParams{
			addr:     net.JoinHostPort(h, strconv.Itoa(p)),
			username: user,
			password: password,
			identity: identity,
		})
	})
	hostEntry.ConnectChanged(func() { hostEntry.RemoveCSSClass("error") })

	dialog.Present(c.win)
}

// savedServersGroup lists the saved connections (nil when there are none).
// Activating a row calls fill; the trash button forgets the connection and
// its keyring password.
func (c *Controller) savedServersGroup(fill func(settings.ServerEntry)) *adw.PreferencesGroup {
	servers := c.settings.Servers()
	if len(servers) == 0 {
		return nil
	}
	group := adw.NewPreferencesGroup()
	group.SetTitle(tr("Saved Connections"))
	list := gtk.NewListBox()
	list.AddCSSClass("boxed-list")
	list.SetSelectionMode(gtk.SelectionNone)
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(list)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetPropagateNaturalHeight(true)
	scroll.SetMaxContentHeight(savedListMaxHeightPx)
	scroll.AddCSSClass("saved-connections")
	scroll.SetOverflow(gtk.OverflowHidden) // clip rows to the rounded card
	group.Add(scroll)
	for _, e := range servers {
		e := e
		row := adw.NewActionRow()
		row.SetTitle(e.Name)
		row.SetTitleLines(1)
		row.SetSubtitle(net.JoinHostPort(e.Host, strconv.Itoa(e.Port)))
		row.AddPrefix(gtk.NewImageFromIconName("network-server-symbolic"))
		row.SetActivatable(true)
		row.ConnectActivated(func() { fill(e) })

		del := gtk.NewButtonFromIconName("user-trash-symbolic")
		del.AddCSSClass("flat")
		del.SetVAlign(gtk.AlignCenter)
		del.SetTooltipText(tr("Forget connection"))
		del.ConnectClicked(func() {
			c.settings.SetServers(removeServer(c.settings.Servers(), e.Host, e.Port, e.Username))
			list.Remove(row)
			if list.FirstChild() == nil {
				group.SetVisible(false)
			}
			key := secrets.Key{Host: e.Host, Port: e.Port, Username: e.Username}
			go func() {
				if err := secrets.Delete(key); err != nil {
					slog.Info("keyring delete failed", "err", err)
				}
			}()
		})
		row.AddSuffix(del)
		list.Append(row)
	}
	return group
}

// saveServer stores the connection (most recent first) and its password in
// the keyring; an empty password removes a previously stored one.
func (c *Controller) saveServer(e settings.ServerEntry, password string) {
	c.settings.SetServers(upsertServer(c.settings.Servers(), e))
	key := secrets.Key{Host: e.Host, Port: e.Port, Username: e.Username}
	go func() {
		var err error
		if password != "" {
			err = secrets.Store(key, password)
		} else {
			err = secrets.Delete(key)
		}
		if err != nil {
			slog.Info("keyring: password not saved", "err", err)
		}
	}()
}

// pickCertificateFile opens a file chooser for a PEM certificate bundle
// (certificate chain + private key, the Mumble export format), imports it
// into the identity store and reports the new identity via onImported.
// Import failures are shown as an alert on top of parent.
func (c *Controller) pickCertificateFile(parent *adw.Dialog, onImported func(certs.IdentityMeta)) {
	pem := gtk.NewFileFilter()
	pem.SetName(tr("PEM Certificates"))
	pem.AddSuffix("pem")
	pem.AddSuffix("crt")
	pem.AddSuffix("key")
	pem.AddMIMEType("application/x-pem-file")
	all := gtk.NewFileFilter()
	all.SetName(tr("All Files"))
	all.AddPattern("*")

	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(pem.Object)
	filters.Append(all.Object)

	fd := gtk.NewFileDialog()
	fd.SetTitle(tr("Select User Certificate"))
	fd.SetModal(true)
	fd.SetFilters(filters)
	fd.SetDefaultFilter(pem)

	var parentWin *gtk.Window
	if w, ok := c.win.(*ui.Window); ok {
		parentWin = &w.ApplicationWindow.ApplicationWindow.Window
	}

	fd.Open(c.appCtx, parentWin, func(res gio.AsyncResulter) {
		file, err := fd.OpenFinish(res)
		if err != nil || file == nil {
			return // cancelled
		}
		path := file.Path()
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		ident, err := c.certs.ImportFiles(name, path, "")
		if err != nil {
			slog.Info("certificate import failed", "path", path, "err", err)
			alert := adw.NewAlertDialog(tr("Certificate Import Failed"),
				fmt.Sprintf(tr("The file could not be imported: %v"), err))
			alert.AddResponse("ok", tr("OK"))
			alert.Present(parent)
			return
		}
		onImported(ident.Meta)
	})
}

// --- Dial ------------------------------------------------------------------

func (c *Controller) startDial(p *dialParams) {
	// Cancel any running reconnect.
	c.mu.Lock()
	if c.retryStop != nil {
		c.retryStop()
		c.retryStop = nil
	}
	// Disconnect any live connection.
	if c.client != nil {
		c.disconnectLocked()
	}
	c.boff.reset()
	c.lastDial = p
	c.mu.Unlock()

	go c.dial(p)
}

func (c *Controller) dial(p *dialParams) {
	cfg := gumble.NewConfig()
	cfg.Username = p.username
	cfg.Password = p.password
	cfg.UDPVoice = true

	// The per-connection bus and its forwarding into the app bus must exist
	// before dialing: gumble emits OnConnect (with the full channel/user
	// snapshot) from inside DialWithDialer once ServerSync arrives.
	connBus := bus.NewForConfig(cfg)
	stopFwd := forwardBus(connBus, c.appBus, func() {
		// Runs inside connBus's Disconnected handler; tearing the
		// forwarders down synchronously from there would deadlock on the
		// subscription's own lock, so defer to the next main-loop turn.
		glib.IdleAdd(func() bool {
			c.onConnectionDropped(connBus)
			return false
		})
	})

	// TOFU callback runs in this goroutine (the dial goroutine); it marshals
	// the trust dialog onto the main loop and blocks waiting for the response.
	verifyFn := c.makeVerifyCallback()
	tlsCfg := c.certs.TLSConfig(p.identity, p.addr, verifyFn)

	slog.Info("dialing", "addr", p.addr, "user", p.username,
		"password", p.password != "", "certificate", p.identity != nil)
	client, err := gumble.DialWithDialer(new(net.Dialer), p.addr, cfg, tlsCfg)
	if err != nil {
		slog.Info("dial error", "err", err)
		stopFwd()
		connBus.Close()
		glib.IdleAdd(func() bool {
			c.onDialError(err)
			return false
		})
		return
	}
	self := "?"
	if client.Self != nil {
		self = fmt.Sprintf("session=%d channel=%q mute=%v suppressed=%v selfmute=%v",
			client.Self.Session, channelName(client.Self.Channel), client.Self.Muted,
			client.Self.Suppressed, client.Self.SelfMuted)
	}
	slog.Info("connected", "addr", p.addr, "server", client.ServerVersion().String(),
		"max_bandwidth", client.MaxBandwidth(), "self", self)

	glib.IdleAdd(func() bool {
		c.onDialSuccess(client, connBus, stopFwd, p.addr)
		return false
	})
}

func (c *Controller) onDialSuccess(client *gumble.Client, connBus *bus.Bus, stopFwd func(), addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.client = client
	c.connBus = connBus
	c.stopFwd = stopFwd
	c.boff.reset()

	// Start voice engine on a per-connection context.
	var voiceCtx context.Context
	voiceCtx, c.voiceCancel = context.WithCancel(c.appCtx)
	var selfSession uint32
	if client.Self != nil {
		selfSession = client.Self.Session
	}
	opts := engine.Options{
		CaptureDevice:  c.settings.AudioInputDevice(),
		PlaybackDevice: c.settings.AudioOutputDevice(),
		Threshold:      c.settings.VADThreshold(),
		JitterFrames:   c.settings.JitterTargetFrames(),
		PacketFrames:   c.settings.AudioPacketFrames(),
	}
	go c.reportConnectionInfo(voiceCtx, client, addr)
	if eng, err := engine.New(client, c.appBus, selfSession, opts); err != nil {
		slog.Info("voice engine init error", "err", err)
	} else {
		c.voiceEngine = eng
		eng.Start(voiceCtx)
	}
}

func (c *Controller) onDialError(err error) {
	class := classifyDialError(err)
	if class == classNone {
		return // TOFU declined — silent, the dialog explained it
	}

	// Emit a ConnectError so the status bar and window state machine update.
	if ev, ok := bus.TranslateReject(err); ok {
		c.appBus.Emit(ev)
	} else {
		c.appBus.Emit(bus.ConnectError{Text: err.Error()})
	}

	c.showErrorDialog(class, err)
}

// connectionInfoInterval is how often the status indicator is refreshed.
const connectionInfoInterval = 5 * time.Second

// reportConnectionInfo emits bus.ConnectionInfo now and periodically until
// ctx (the connection's lifetime) ends. Runs on its own goroutine.
func (c *Controller) reportConnectionInfo(ctx context.Context, client *gumble.Client, addr string) {
	tick := time.NewTicker(connectionInfoInterval)
	defer tick.Stop()
	for {
		info := bus.ConnectionInfo{
			Address:       addr,
			ServerVersion: client.ServerVersion().String(),
			Transport:     client.VoiceTransport(),
			Ping:          client.TCPPing(),
			MaxBandwidth:  client.MaxBandwidth(),
		}
		if self := client.Self; self != nil {
			info.Username = self.Name
			info.Channel = channelName(self.Channel)
		}
		c.appBus.Emit(info)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// onSettingChanged forwards audio settings to the live voice engine.
// Main loop (GSettings change signal).
func (c *Controller) onSettingChanged(key string) {
	c.mu.Lock()
	eng := c.voiceEngine
	c.mu.Unlock()
	if eng == nil {
		return
	}
	switch key {
	case "vad-threshold":
		eng.SetThreshold(c.settings.VADThreshold())
	case "jitter-target-frames":
		eng.SetJitterFrames(c.settings.JitterTargetFrames())
	case "audio-packet-frames":
		eng.SetPacketFrames(c.settings.AudioPacketFrames())
	case "audio-input-device", "audio-output-device":
		capture, playback := c.settings.AudioInputDevice(), c.settings.AudioOutputDevice()
		// Reopening the stream blocks briefly; keep it off the main loop.
		go eng.SetDevices(capture, playback)
	}
}

// onConnectionDropped is called on the main loop after the gumble connection
// is lost (not by the user). It starts the auto-reconnect loop if the setting
// is enabled.
func (c *Controller) onConnectionDropped(connBus *bus.Bus) {
	c.mu.Lock()
	if c.connBus != connBus {
		// A newer connection (or an explicit disconnect) already replaced
		// this one; nothing left to tear down.
		c.mu.Unlock()
		return
	}
	p := c.lastDial
	enabled := c.settings.AutoReconnect()
	c.disconnectLocked()
	c.mu.Unlock()

	if !enabled || p == nil {
		return
	}
	c.scheduleReconnect(p)
}

func (c *Controller) scheduleReconnect(p *dialParams) {
	c.mu.Lock()
	delay := c.boff.next()
	ctx, cancel := context.WithCancel(c.appCtx)
	c.retryStop = cancel
	c.mu.Unlock()

	slog.Info("reconnect scheduled", "delay", delay)

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		glib.IdleAdd(func() bool {
			c.appBus.Emit(bus.ConnectError{Text: tr("Reconnecting…")})
			go c.dial(p)
			return false
		})
	}()
}

// disconnectLocked tears down the live connection. c.mu must be held.
func (c *Controller) disconnectLocked() {
	if c.voiceCancel != nil {
		c.voiceCancel()
		c.voiceCancel = nil
	}
	if c.voiceEngine != nil {
		c.voiceEngine.Stop()
		c.voiceEngine = nil
	}
	if c.stopFwd != nil {
		c.stopFwd()
		c.stopFwd = nil
	}
	if c.connBus != nil {
		c.connBus.Close()
		c.connBus = nil
	}
	if c.client != nil {
		c.client.Disconnect()
		c.client = nil
	}
}

// --- Channel join ----------------------------------------------------------

func (c *Controller) onJoinRequested(ev bus.ChannelJoinRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()

	if client == nil || client.Self == nil {
		return
	}
	ch := client.Channels[ev.ChannelID]
	if ch == nil {
		return
	}
	client.Self.Move(ch)
}

// --- User actions (T18) ----------------------------------------------------

func (c *Controller) onSelfMuteToggle(ev bus.SelfMuteToggle) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil || client.Self == nil {
		return
	}
	client.Self.SetSelfMuted(ev.Muted)
}

func (c *Controller) onSelfDeafenToggle(ev bus.SelfDeafenToggle) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil || client.Self == nil {
		return
	}
	client.Self.SetSelfDeafened(ev.Deafened)
}

func (c *Controller) onUserMoveRequested(ev bus.UserMoveRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil || client.Self == nil {
		return
	}
	target := client.Users[ev.Session]
	if target == nil || target.Channel == nil {
		return
	}
	target.Move(client.Self.Channel)
}

func (c *Controller) onUserMuteRequested(ev bus.UserMuteRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	if u := client.Users[ev.Session]; u != nil {
		u.SetMuted(ev.Muted)
	}
}

func (c *Controller) onUserDeafenRequested(ev bus.UserDeafenRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	if u := client.Users[ev.Session]; u != nil {
		u.SetDeafened(ev.Deafened)
	}
}

func (c *Controller) onUserPriorityRequested(ev bus.UserPriorityRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	if u := client.Users[ev.Session]; u != nil {
		u.SetPrioritySpeaker(ev.Priority)
	}
}

func (c *Controller) onUserRegisterRequested(ev bus.UserRegisterRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	if u := client.Users[ev.Session]; u != nil {
		u.Register()
	}
}

func (c *Controller) onUserStatsRequested(ev bus.UserStatsRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	u := client.Users[ev.Session]
	if u == nil {
		return
	}
	u.RequestStats()
}

// --- Admin actions (T21) ---------------------------------------------------

func (c *Controller) onUserKickRequested(ev bus.UserKickRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	if u := client.Users[ev.Session]; u != nil {
		u.Kick(ev.Reason)
	}
}

func (c *Controller) onUserBanRequested(ev bus.UserBanRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	if u := client.Users[ev.Session]; u != nil {
		u.Ban(ev.Reason)
	}
}

func (c *Controller) onChannelCreateRequested(ev bus.ChannelCreateRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	parent := client.Channels[ev.ParentID]
	if parent == nil {
		return
	}
	parent.Add(ev.Name, ev.Temporary)
}

func (c *Controller) onChannelDeleteRequested(ev bus.ChannelDeleteRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	if ch := client.Channels[ev.ChannelID]; ch != nil {
		ch.Remove()
	}
}

func (c *Controller) onChannelRenameRequested(ev bus.ChannelRenameRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	if ch := client.Channels[ev.ChannelID]; ch != nil {
		ch.SetName(ev.Name)
	}
}

func (c *Controller) onBanListRequested(_ bus.BanListRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client != nil {
		client.RequestBanList()
	}
}

func (c *Controller) onUserListRequested(_ bus.UserListRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client != nil {
		client.RequestUserList()
	}
}

func (c *Controller) onBanListWriteRequested(ev bus.BanListWriteRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	var list gumble.BanList
	for _, e := range ev.Entries {
		list.Add(e.Address, e.Mask, e.Reason, e.Duration)
	}
	client.Send(list)
}

func (c *Controller) onUserUnregisterRequested(ev bus.UserUnregisterRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	u := &gumble.RegisteredUser{UserID: ev.UserID}
	u.Deregister()
	client.Send(gumble.RegisteredUsers{u})
}

func (c *Controller) onACLRequested(ev bus.ACLRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	if ch := client.Channels[ev.ChannelID]; ch != nil {
		ch.RequestACL()
	}
}

func (c *Controller) onACLSaveRequested(ev bus.ACLSaveRequested) {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return
	}
	ch := client.Channels[ev.ChannelID]
	if ch == nil {
		return
	}
	acl := &gumble.ACL{
		Channel:  ch,
		Inherits: ev.Inherits,
	}
	for _, gi := range ev.Groups {
		g := &gumble.ACLGroup{
			Name:         gi.Name,
			InheritUsers: gi.InheritUsers,
			Inheritable:  gi.Inheritable,
			UsersAdd:     make(map[uint32]*gumble.ACLUser, len(gi.UsersAdd)),
			UsersRemove:  make(map[uint32]*gumble.ACLUser, len(gi.UsersRemove)),
		}
		for _, u := range gi.UsersAdd {
			g.UsersAdd[u.UserID] = &gumble.ACLUser{UserID: u.UserID, Name: u.Name}
		}
		for _, u := range gi.UsersRemove {
			g.UsersRemove[u.UserID] = &gumble.ACLUser{UserID: u.UserID, Name: u.Name}
		}
		acl.Groups = append(acl.Groups, g)
	}
	for _, ri := range ev.Rules {
		r := &gumble.ACLRule{
			AppliesCurrent:  ri.AppliesCurrent,
			AppliesChildren: ri.AppliesChildren,
			Granted:         gumble.Permission(ri.Granted),
			Denied:          gumble.Permission(ri.Denied),
		}
		if ri.GroupName != "" {
			r.Group = &gumble.ACLGroup{Name: ri.GroupName}
		} else if ri.UserID != 0 {
			r.User = &gumble.ACLUser{UserID: ri.UserID, Name: ri.UserName}
		}
		acl.Rules = append(acl.Rules, r)
	}
	client.Send(acl)
}

// --- chat.Sender -----------------------------------------------------------

// SendText implements chat.Sender. Called on the main loop.
func (c *Controller) SendText(channelID, session uint32, message string) error {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return fmt.Errorf("not connected")
	}
	if session != 0 {
		if u := client.Users[session]; u != nil {
			u.Send(message)
		}
		return nil
	}
	if ch := client.Channels[channelID]; ch != nil {
		ch.Send(message, false)
	}
	return nil
}

// --- TOFU dialogs ----------------------------------------------------------

// makeVerifyCallback returns a certs.VerifyCallback that blocks the dial
// goroutine while showing a GTK4 trust dialog on the main loop.
func (c *Controller) makeVerifyCallback() certs.VerifyCallback {
	return func(result certs.VerifyResult, stored, presented certs.ServerEntry) error {
		switch result {
		case certs.VerifyMatch:
			return nil // already pinned — proceed silently
		case certs.VerifyNew:
			trusted := c.blockForTOFUDialog(false, presented.Fingerprint)
			if !trusted {
				return errDeclinedTOFU
			}
			return nil
		case certs.VerifyChanged:
			trusted := c.blockForTOFUDialog(true, presented.Fingerprint)
			if !trusted {
				return errDeclinedTOFU
			}
			return nil
		}
		return errDeclinedTOFU
	}
}

// blockForTOFUDialog shows a TOFU dialog on the main loop and blocks the
// caller (which runs on a goroutine) until the user responds.
func (c *Controller) blockForTOFUDialog(changed bool, fingerprint string) bool {
	ch := make(chan bool, 1)
	glib.IdleAdd(func() bool {
		c.showTOFUDialog(changed, fingerprint, ch)
		return false
	})
	return <-ch
}

func (c *Controller) showTOFUDialog(changed bool, fingerprint string, ch chan<- bool) {
	var heading, body string
	if changed {
		heading = tr("Server Certificate Changed")
		body = fmt.Sprintf(
			tr("The server's certificate has changed.\n\nNew fingerprint:\n%s\n\nOnly continue if you trust this certificate."),
			groupedHex(fingerprint))
	} else {
		heading = tr("New Server Certificate")
		body = fmt.Sprintf(
			tr("This server's certificate has not been seen before.\n\nFingerprint:\n%s\n\nTrust and connect?"),
			groupedHex(fingerprint))
	}

	alert := adw.NewAlertDialog(heading, body)
	alert.AddResponse("abort", tr("Cancel"))
	alert.AddResponse("trust", tr("Trust and Connect"))
	alert.SetDefaultResponse("trust")
	alert.SetCloseResponse("abort")
	if changed {
		alert.SetResponseAppearance("trust", adw.ResponseDestructive)
	} else {
		alert.SetResponseAppearance("trust", adw.ResponseSuggested)
	}
	alert.ConnectResponse(func(resp string) {
		ch <- (resp == "trust")
	})
	alert.Present(c.win)
}

func channelName(ch *gumble.Channel) string {
	if ch == nil {
		return ""
	}
	return ch.Name
}

// --- Error dialogs ---------------------------------------------------------

func (c *Controller) showErrorDialog(class errorClass, err error) {
	var heading, body string
	switch class {
	case classWrongPassword:
		heading = tr("Authentication Failed")
		body = tr("The server rejected the password or credentials.")
	case classBanned:
		heading = tr("Banned")
		body = tr("You are banned from this server.")
	case classServerFull:
		heading = tr("Server Full")
		body = tr("The server has reached its maximum number of users.")
	case classTLSFailure:
		heading = tr("TLS Handshake Failed")
		body = fmt.Sprintf(tr("Could not establish a secure connection:\n%s"), err.Error())
	case classUnreachable:
		heading = tr("Server Unreachable")
		body = fmt.Sprintf(tr("The server could not be reached:\n%s"), unreachableDetail(err))
	default:
		heading = tr("Connection Failed")
		body = err.Error()
	}

	alert := adw.NewAlertDialog(heading, body)
	alert.AddResponse("ok", tr("OK"))
	alert.SetDefaultResponse("ok")
	alert.Present(c.win)
}

// --- Helpers ---------------------------------------------------------------

// groupedHex inserts a space every 2 hex characters for readability.
func groupedHex(fp string) string {
	const groupSize = 2
	var out []byte
	for i, b := range []byte(fp) {
		if i > 0 && i%groupSize == 0 {
			out = append(out, ' ')
		}
		out = append(out, b)
	}
	return string(out)
}
