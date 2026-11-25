package connection

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"weak"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/audio/engine"
	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/certs"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/protocol/gumble"
	"github.com/one337/murmel/internal/settings"
	"github.com/one337/murmel/internal/ui"
	"github.com/one337/murmel/internal/ui/avatarpick"
	"github.com/one337/murmel/internal/ui/clock"
	"github.com/one337/murmel/internal/ui/identities"
	"github.com/one337/murmel/internal/ui/secrets"
	"github.com/one337/murmel/internal/ui/weakref"
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
	lastJoin  joinAttempt // for the channel password prompt

	// gen numbers the connection attempts (see attempt); it is read by
	// the dial goroutines, changed only on the main loop under mu.
	gen           atomic.Uint64
	attemptCancel context.CancelFunc // ends the current attempt
	shownGen      uint64             // attempt whose connection the UI shows (main loop)

	certPromptOpen bool // the missing-certificate prompt is showing

	voiceEngine *engine.Engine
	voiceCancel context.CancelFunc

	audio localAudio // remembered local volumes (main loop only)
}

// dialParams are the parameters for one dial attempt, saved so a reconnect
// can replay the same call without user interaction.
type dialParams struct {
	addr     string
	username string
	password string
	identity *certs.Identity
	// channelPath is the channel a mumble:// link points to (joined after
	// connecting; nil for a plain connect).
	channelPath []string
	// rememberPassword saves password to the keyring (and the connection
	// to the saved list) once the server accepted it.
	rememberPassword bool
	// texture is the avatar image sent after connecting (nil = none).
	texture []byte
	// droppedPassword: this attempt left out the form's password after
	// the server refused a registered name with it (see onDialError).
	droppedPassword bool
	// tokens are access tokens (channel passwords) given this session;
	// a reconnect sends them again.
	tokens []string
}

// joinAttempt is the user's latest channel join request.
type joinAttempt struct {
	id uint32
	at time.Time
}

// avatarStore holds the user's own avatars next to the certificates.
func (c *Controller) avatarStore() *avatarpick.Store {
	return avatarpick.NewStore(filepath.Join(c.certs.DataDir(), "avatars"))
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
	c.subscribeLocalAudio()
	bus.SubscribeCtx(ctx, appBus, c.onSelfMuteToggle)
	bus.SubscribeCtx(ctx, appBus, c.onSelfDeafenToggle)
	bus.SubscribeCtx(ctx, appBus, c.onUserMoveRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserMuteRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserUnsuppressRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserDeafenRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserPriorityRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserRegisterRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserStatsRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserKickRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserBanRequested)
	bus.SubscribeCtx(ctx, appBus, c.onChannelCreateRequested)
	bus.SubscribeCtx(ctx, appBus, c.onChannelDeleteRequested)
	bus.SubscribeCtx(ctx, appBus, c.onChannelEditRequested)
	bus.SubscribeCtx(ctx, appBus, c.onAccessTokenAdded)
	bus.SubscribeCtx(ctx, appBus, c.onPermissionDenied)
	bus.SubscribeCtx(ctx, appBus, c.onDisconnectRequested)
	bus.SubscribeCtx(ctx, appBus, c.onChannelPermissionRequested)
	bus.SubscribeCtx(ctx, appBus, c.onChannelDescriptionRequested)
	bus.SubscribeCtx(ctx, appBus, c.onUserTextureRequested)
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

// savedListMaxHeightPx shows five and a half saved connections before the
// list scrolls: the cut-off row (plus a fade and a permanent scrollbar)
// tells that there is more.
const savedListMaxHeightPx = 320

// savedRowPx is the height of one saved-connection row (title and
// subtitle); savedListMinRows of them stay visible even when the dialog has
// to shrink to a small window (the whole form scrolls then).
const (
	savedRowPx       = 57
	savedListMinRows = 3.5
)

func (c *Controller) showConnectDialog(req ui.ConnectRequest) {
	dialog, _, _ := c.newConnectDialog(req)
	dialog.Present(c.win)
}

// newConnectDialog builds the connect dialog for req. It also returns its
// form and its saved list (nil without saved connections), which live as
// long as the dialog.
func (c *Controller) newConnectDialog(req ui.ConnectRequest) (*adw.Dialog, *connectForm, *savedList) {
	dialog := adw.NewDialog()
	dialog.SetTitle(i18n.Tr("Connect to Server"))
	dialog.SetContentWidth(connectDialogWidth)

	dialogCtx, closeDialogCtx := context.WithCancel(c.appCtx)
	dialog.ConnectClosed(closeDialogCtx)

	// The form and the saved list live as long as the dialog; the closures
	// below hold them and the dialog only weakly (see internal/ui/weakref).
	form := c.newConnectForm(dialog, dialogCtx, req)
	formRef := weak.Make(form)
	dialogRef := weakref.New(dialog)
	// Saved connections: activating one fills the form (the password comes
	// from the keyring).
	saved := c.savedServersGroup(dialog, func(e settings.ServerEntry) {
		if f := formRef.Value(); f != nil {
			f.fill(e)
		}
	})

	box := gtk.NewBox(gtk.OrientationVertical, 18)
	box.SetMarginTop(12)
	box.SetMarginBottom(18)
	box.SetMarginStart(18)
	box.SetMarginEnd(18)
	if saved != nil {
		box.Append(saved.group)
	}
	box.Append(form.server)
	box.Append(form.account)

	// The scrolled window only kicks in when the screen is too small; it
	// otherwise propagates the form's natural height to the dialog so the
	// whole form is visible without scrolling.
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(box)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetPropagateNaturalHeight(true)

	connectBtn := gtk.NewButtonWithLabel(i18n.Tr("Connect"))
	connectBtn.AddCSSClass("suggested-action")
	cancelBtn := gtk.NewButtonWithLabel(i18n.Tr("Cancel"))

	hdr := adw.NewHeaderBar()
	hdr.PackStart(cancelBtn)
	hdr.PackEnd(connectBtn)
	hdr.SetShowEndTitleButtons(false)
	hdr.SetShowStartTitleButtons(false)

	// While connected, a banner says that connecting here ends that
	// connection, and the saved list marks its account. Both follow the
	// connection while the dialog is open.
	banner := adw.NewBanner("")
	banner.SetUseMarkup(false) // the host is the user's text
	showConnection := func() {
		host, port, user, ok := c.shownConnection()
		banner.SetRevealed(ok)
		if ok {
			shown := host
			if port != ui.DefaultPort {
				shown = net.JoinHostPort(host, strconv.Itoa(port))
			}
			banner.SetTitle(fmt.Sprintf(i18n.Tr("Connected to %s. Connecting to another server ends this connection."), shown))
		}
		if saved != nil {
			saved.markConnected(host, port, user, ok)
		}
	}
	showConnection()
	bus.SubscribeCtx(dialogCtx, c.appBus, func(bus.Connected) { showConnection() })
	bus.SubscribeCtx(dialogCtx, c.appBus, func(bus.Disconnected) { showConnection() })
	bus.SubscribeCtx(dialogCtx, c.appBus, func(bus.ConnectError) { showConnection() })

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(hdr)
	toolbar.AddTopBar(banner)
	toolbar.SetContent(scroll)
	dialog.SetChild(toolbar)
	dialog.SetDefaultWidget(connectBtn)

	cancelBtn.ConnectClicked(func() { dialogRef.Do(func(d *adw.Dialog) { d.Close() }) })
	connectBtn.ConnectClicked(func() {
		f := formRef.Value()
		if f == nil {
			return
		}
		dialogRef.Do(func(d *adw.Dialog) { c.connectFromForm(d, f) })
	})
	return dialog, form, saved
}

// shownConnection is the server and account of the connection the user
// interface shows (ok false: none). Main loop only.
func (c *Controller) shownConnection() (host string, port int, username string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.lastDial
	if p == nil || c.shownGen == 0 || c.shownGen != c.gen.Load() {
		return "", 0, "", false
	}
	host, portStr, err := net.SplitHostPort(p.addr)
	if err != nil {
		return "", 0, "", false
	}
	port, _ = strconv.Atoi(portStr)
	return host, port, p.username, true
}

// saveServerEntry stores the connection in the saved list (in place, or at
// the end when it is new).
func (c *Controller) saveServerEntry(e settings.ServerEntry) {
	c.settings.SetServers(upsertServer(c.settings.Servers(), e))
}

// saveServer stores the connection and its password in the keyring; an
// empty password removes a previously stored one.
func (c *Controller) saveServer(e settings.ServerEntry, password string) {
	c.saveServerEntry(e)
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

// pickCertificateFile imports a certificate file over parent (see
// identities.Import): a new identity is added to the form, one already
// stored is selected there.
func (c *Controller) pickCertificateFile(parent *adw.Dialog, onImported func(meta certs.IdentityMeta, existing bool)) {
	identities.Import(parent, c.certs, onImported)
}

// --- Dial ------------------------------------------------------------------

func (c *Controller) startDial(p *dialParams) {
	a := c.switchAttempt(p)
	go c.dial(p, a)
}

// switchAttempt ends whatever runs (a reconnect wait, a dial with its
// prompts, a connection) and announces the new attempt: Disconnected for
// a connection the user interface shows, then ConnectionStarting — in
// this order, so clearing away the old server cannot wipe out the start
// of the new one.
func (c *Controller) switchAttempt(p *dialParams) attempt {
	c.mu.Lock()
	if c.retryStop != nil {
		c.retryStop()
		c.retryStop = nil
	}
	a, shown := c.beginAttemptLocked()
	if c.client != nil {
		shown = true
		c.disconnectLocked()
	}
	c.boff.reset()
	c.lastDial = p
	c.mu.Unlock()

	if shown {
		slog.Info("leaving the server for another connection")
		c.appBus.Emit(bus.Disconnected{Reason: bus.DisconnectUser})
	}
	c.appBus.Emit(bus.ConnectionStarting{Reconnect: false})
	return a
}

func (c *Controller) dial(p *dialParams, a attempt) {
	cfg := gumble.NewConfig()
	cfg.Username = p.username
	cfg.Password = p.password
	cfg.Tokens = gumble.AccessTokens(p.tokens)
	cfg.UDPVoice = true

	// The per-connection bus and its forwarding into the app bus must exist
	// before dialing: gumble emits OnConnect (with the full channel/user
	// snapshot) from inside DialWithDialer once ServerSync arrives.
	connBus := bus.NewForConfig(cfg)
	stopFwd := forwardBus(connBus, c.appBus, c.passFor(a), func() {
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
	verifyFn := c.makeVerifyCallback(a)
	tlsCfg := c.certs.TLSConfig(p.identity, p.addr, verifyFn)

	slog.Info("dialing", "addr", p.addr, "user", p.username,
		"password", p.password != "", "certificate", p.identity != nil)
	// A superseded attempt gives up before it logs in: logging in later
	// would make the server drop the newer session of the same account.
	client, err := gumble.DialContext(a.ctx, new(net.Dialer), p.addr, cfg, tlsCfg)
	if err != nil {
		slog.Info("dial error", "err", err)
		stopFwd()
		connBus.Close()
		glib.IdleAdd(func() bool {
			c.onDialError(err, p, a)
			return false
		})
		return
	}
	self := "?"
	client.Do(func() {
		if client.Self != nil {
			self = fmt.Sprintf("session=%d channel=%q mute=%v suppressed=%v selfmute=%v",
				client.Self.Session, channelName(client.Self.Channel), client.Self.Muted,
				client.Self.Suppressed, client.Self.SelfMuted)
		}
	})
	slog.Info("connected", "addr", p.addr, "server", client.ServerVersion().String(),
		"max_bandwidth", client.MaxBandwidth(), "self", self)

	glib.IdleAdd(func() bool {
		c.onDialSuccess(a, client, connBus, stopFwd, p.addr)
		return false
	})
}

func (c *Controller) onDialSuccess(a attempt, client *gumble.Client, connBus *bus.Bus, stopFwd func(), addr string) {
	if !c.current(a) {
		// Superseded while dialing: leave again without a trace — no
		// password or saved connection is stored, no voice starts.
		slog.Info("superseded connection closed", "addr", addr)
		stopFwd()
		connBus.Close()
		client.Disconnect()
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.client = client
	c.connBus = connBus
	c.stopFwd = stopFwd
	c.boff.reset()

	if p := c.lastDial; p != nil && p.rememberPassword {
		p.rememberPassword = false // once; reconnects reuse the dial params
		accepted := *p
		// Not under c.mu: saving changes settings, and the settings change
		// handler (onSettingChanged) takes c.mu itself.
		glib.IdleAdd(func() bool {
			c.rememberAccepted(&accepted)
			return false
		})
	}

	// Join the channel a mumble:// link pointed to; read the session for
	// the voice engine. gumble's read loop is already running (withClient).
	var selfSession uint32
	var texture []byte
	if c.lastDial != nil {
		texture = c.lastDial.texture
	}
	client.Do(func() {
		if client.Self == nil {
			return
		}
		selfSession = client.Self.Session
		if len(texture) > 0 {
			client.Self.SetTexture(texture)
		}
		if p := c.lastDial; p != nil && len(p.channelPath) > 0 {
			if ch := client.Channels.Find(p.channelPath...); ch != nil {
				client.Self.Move(ch)
			} else {
				slog.Info("link channel not found", "path", strings.Join(p.channelPath, "/"))
			}
		}
	})

	// Start voice engine on a per-connection context.
	var voiceCtx context.Context
	voiceCtx, c.voiceCancel = context.WithCancel(c.appCtx)
	opts := engine.Options{
		CaptureDevice:    c.settings.AudioInputDevice(),
		PlaybackDevice:   c.settings.AudioOutputDevice(),
		Threshold:        c.settings.VADThreshold(),
		JitterFrames:     c.settings.JitterTargetFrames(),
		PacketFrames:     c.settings.AudioPacketFrames(),
		Bitrate:          c.settings.AudioQuality() * 1000,
		NoiseSuppression: c.settings.NoiseSuppression(),
		EchoCancellation: c.settings.EchoCancellation(),
		Mode:             transmitMode(c.settings.TransmissionMode()),
	}
	go c.reportConnectionInfo(voiceCtx, client, addr)
	if eng, err := engine.New(client, c.appBus, selfSession, opts); err != nil {
		slog.Info("voice engine init error", "err", err)
	} else {
		c.voiceEngine = eng
		eng.Start(voiceCtx)
	}
	c.startLocalAudio(addr)
}

func (c *Controller) onDialError(err error, p *dialParams, a attempt) {
	if !c.current(a) {
		slog.Info("superseded attempt failed", "err", err)
		return
	}
	class := classifyDialError(err)
	if class == classNone {
		return // TOFU declined — silent, the dialog explained it
	}

	// Emit a ConnectError so the status bar and window state machine update.
	ev, isReject := bus.TranslateReject(err)
	if isReject {
		c.appBus.Emit(ev)
	} else {
		c.appBus.Emit(bus.ConnectError{Text: err.Error()})
	}

	// A missing or wrong password is asked for instead of reported. A
	// registered name the server does not recognize (another certificate,
	// no certificate, or the wrong account password) gets its own choice:
	// a password alone cannot help when the certificate is the problem.
	if isReject && p != nil {
		// Mumble 1.6 checks a password sent by a name registered with a
		// certificate as that account's password, so the server password
		// from the form makes the login fail although registered users do
		// not need it. Try once more without it. This is a server bug
		// (mumble-voip/mumble#7495); only 1.6 servers without its fix
		// need the second attempt.
		if ev.Reason == bus.RejectUserCredentials && p.identity != nil && p.password != "" && !p.droppedPassword {
			slog.Info("registered name refused with password: retrying without it")
			retry := *p
			retry.password = ""
			retry.droppedPassword = true
			retry.rememberPassword = false
			c.startDial(&retry)
			return
		}
		switch {
		case ev.Reason == bus.RejectServerPassword,
			ev.Reason == bus.RejectUserCredentials && strings.EqualFold(p.username, "SuperUser"):
			c.promptPassword(p, a, ev.Reason == bus.RejectUserCredentials)
			return
		case ev.Reason == bus.RejectUserCredentials:
			c.promptRegisteredName(p, a)
			return
		}
	}
	c.showErrorDialog(class, err)
}

// promptRegisteredName explains that the name is registered on the server
// with another certificate (or a password) and offers the ways out:
// change the name or certificate, or log in with the account's password.
func (c *Controller) promptRegisteredName(p *dialParams, a attempt) {
	body := fmt.Sprintf(i18n.Tr("The name %s is registered on this server, and the server does not recognize you as its owner. Connect with the certificate the name was registered with, choose another name, or enter the account's password if it has one."), p.username)
	if p.identity != nil {
		body = fmt.Sprintf(i18n.Tr("The name %s is registered on this server with a different certificate than %s. Connect with the certificate the name was registered with, choose another name, or enter the account's password if it has one."), p.username, p.identity.Meta.Name)
	}
	entry := adw.NewPasswordEntryRow()
	entry.SetTitle(i18n.Tr("Account password"))
	group := adw.NewPreferencesGroup()
	group.Add(entry)

	alert := adw.NewAlertDialog(i18n.Tr("Name Already Registered"), body)
	alert.SetExtraChild(group)
	alert.AddResponse("cancel", i18n.Tr("Cancel"))
	alert.AddResponse("change", i18n.Tr("Change Name or Certificate…"))
	alert.AddResponse("password", i18n.Tr("Log In with Password"))
	alert.SetResponseAppearance("password", adw.ResponseSuggested)
	alert.SetResponseEnabled("password", false)
	alert.SetDefaultResponse("change")
	alert.SetCloseResponse("cancel")
	entryRef, alertRef := weakref.New(entry), weakref.New(alert)
	entry.ConnectChanged(func() {
		entry, ok1 := entryRef.Get()
		alert, ok2 := alertRef.Get()
		if !ok1 || !ok2 {
			return
		}
		on := entry.Text() != ""
		alert.SetResponseEnabled("password", on)
		if on {
			alert.SetDefaultResponse("password")
		} else {
			alert.SetDefaultResponse("change")
		}
	})
	entry.SetActivatesDefault(true)
	alert.ConnectResponse(func(r string) {
		switch r {
		case "password":
			retry := *p
			retry.password = entry.Text()
			c.startDial(&retry)
		case "change":
			host, portStr, err := net.SplitHostPort(p.addr)
			if err != nil {
				host = p.addr
			}
			port, _ := strconv.Atoi(portStr)
			c.showConnectDialog(ui.ConnectRequest{Host: host, Port: port, Username: p.username, Password: p.password})
		}
	})
	closeWhenSuperseded(a, &alert.Dialog)
	alert.Present(c.win)
}

// promptPassword asks for the password the server wants — the user's
// password (e.g. SuperUser, or an account registered with a password
// instead of a certificate) or the server password — and retries. With
// "Remember password" the password goes to the keyring once the server
// accepts it.
func (c *Controller) promptPassword(p *dialParams, a attempt, userPassword bool) {
	heading := i18n.Tr("Password Required")
	body := i18n.Tr("This server is protected by a password.")
	if userPassword {
		body = fmt.Sprintf(i18n.Tr("The server needs the password for the user %s."), p.username)
	}
	if p.password != "" {
		heading = i18n.Tr("Wrong Password")
		body = i18n.Tr("The server did not accept the password. Please try again.")
	}

	entry := adw.NewPasswordEntryRow()
	entry.SetTitle(i18n.Tr("Password"))
	remember := adw.NewSwitchRow()
	remember.SetTitle(i18n.Tr("Remember Password"))
	remember.SetSubtitle(i18n.Tr("Stored in your keyring"))
	remember.SetActive(true)
	group := adw.NewPreferencesGroup()
	group.Add(entry)
	group.Add(remember)

	alert := adw.NewAlertDialog(heading, body)
	alert.SetExtraChild(group)
	alert.AddResponse("cancel", i18n.Tr("Cancel"))
	alert.AddResponse("connect", i18n.Tr("Connect"))
	alert.SetResponseAppearance("connect", adw.ResponseSuggested)
	alert.SetDefaultResponse("connect")
	alert.SetCloseResponse("cancel")
	alert.SetResponseEnabled("connect", false)
	enableWhenFilled(entry, alert, "connect")
	entry.SetActivatesDefault(true) // Enter connects
	alert.ConnectResponse(func(response string) {
		if response != "connect" {
			return
		}
		retry := *p
		retry.password = entry.Text()
		retry.rememberPassword = remember.Active()
		c.startDial(&retry)
	})
	closeWhenSuperseded(a, &alert.Dialog)
	alert.Present(c.win)
	entry.GrabFocus()
}

// enableWhenFilled enables alert's response only while entry is not empty.
func enableWhenFilled(entry *adw.PasswordEntryRow, alert *adw.AlertDialog, response string) {
	entryRef, alertRef := weakref.New(entry), weakref.New(alert)
	entry.ConnectChanged(func() {
		entry, ok1 := entryRef.Get()
		alert, ok2 := alertRef.Get()
		if ok1 && ok2 {
			alert.SetResponseEnabled(response, entry.Text() != "")
		}
	})
}

// rememberAccepted stores the password of a successful dial in the keyring
// and the connection in the saved list (keeping an existing entry's
// certificate choice).
func (c *Controller) rememberAccepted(p *dialParams) {
	host, portStr, err := net.SplitHostPort(p.addr)
	if err != nil {
		return
	}
	port, _ := strconv.Atoi(portStr)
	entry := settings.ServerEntry{
		Name:     savedName(p.username, host, port),
		Host:     host,
		Port:     port,
		Username: p.username,
	}
	if p.identity != nil {
		entry.IdentityID = p.identity.Meta.ID
	}
	for _, e := range c.settings.Servers() {
		if sameAccount(e, host, port, p.username) && entry.IdentityID == "" {
			entry.IdentityID = e.IdentityID
		}
	}
	c.saveServer(entry, p.password)
	slog.Info("password remembered", "server", entry.Name)
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
		client.Do(func() {
			if self := client.Self; self != nil {
				info.Username = self.Name
				info.Channel = channelName(self.Channel)
			}
		})
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
	case "noise-suppression":
		eng.SetNoiseSuppression(c.settings.NoiseSuppression())
	case "echo-cancellation":
		eng.SetEchoCancellation(c.settings.EchoCancellation())
	case "transmission-mode":
		eng.SetTransmitMode(transmitMode(c.settings.TransmissionMode()))
	case "audio-quality":
		eng.SetBitrate(c.settings.AudioQuality() * 1000)
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
			if ctx.Err() != nil {
				return false // cancelled while this was queued
			}
			c.mu.Lock()
			a, _ := c.beginAttemptLocked()
			c.mu.Unlock()
			c.appBus.Emit(bus.ConnectError{Text: i18n.Tr("Reconnecting…")})
			c.appBus.Emit(bus.ConnectionStarting{Reconnect: true})
			go c.dial(p, a)
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

// withClient runs f with the live client while gumble holds its state
// lock. gumble's read loop mutates Users, Channels and Self on its own
// goroutine; reading them unsynchronised from the main loop is a data race,
// and a concurrent map read and write aborts the process. gumble fires its
// listeners only after releasing the lock, so this cannot deadlock with
// the bus. Reports false when there is no connection.
func (c *Controller) withClient(f func(*gumble.Client)) bool {
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return false
	}
	client.Do(func() { f(client) })
	return true
}

// --- Channel join ----------------------------------------------------------

func (c *Controller) onJoinRequested(ev bus.ChannelJoinRequested) {
	c.mu.Lock()
	c.lastJoin = joinAttempt{id: ev.ChannelID, at: time.Now()}
	c.mu.Unlock()
	c.withClient(func(client *gumble.Client) {
		if client.Self == nil {
			return
		}
		if ch := client.Channels[ev.ChannelID]; ch != nil {
			client.Self.Move(ch)
		}
	})
}

// --- User actions ----------------------------------------------------------

func (c *Controller) onSelfMuteToggle(ev bus.SelfMuteToggle) {
	c.withClient(func(client *gumble.Client) {
		if client.Self == nil {
			return
		}
		client.Self.SetSelfMuted(ev.Muted)
	})
}

func (c *Controller) onSelfDeafenToggle(ev bus.SelfDeafenToggle) {
	c.withClient(func(client *gumble.Client) {
		if client.Self == nil {
			return
		}
		client.Self.SetSelfDeafened(ev.Deafened)
	})
}

func (c *Controller) onUserMoveRequested(ev bus.UserMoveRequested) {
	c.withClient(func(client *gumble.Client) {
		if client.Self == nil {
			return
		}
		target := client.Users[ev.Session]
		if target == nil || target.Channel == nil {
			return
		}
		to := client.Self.Channel
		if ev.ToChannel {
			to = client.Channels[ev.ChannelID]
		}
		if to == nil || to == target.Channel {
			return
		}
		target.Move(to)
	})
}

func (c *Controller) onUserMuteRequested(ev bus.UserMuteRequested) {
	c.withClient(func(client *gumble.Client) {
		if u := client.Users[ev.Session]; u != nil {
			u.SetMuted(ev.Muted)
		}
	})
}

func (c *Controller) onUserUnsuppressRequested(ev bus.UserUnsuppressRequested) {
	c.withClient(func(client *gumble.Client) {
		if u := client.Users[ev.Session]; u != nil {
			u.SetSuppressed(false)
		}
	})
}

func (c *Controller) onUserDeafenRequested(ev bus.UserDeafenRequested) {
	c.withClient(func(client *gumble.Client) {
		if u := client.Users[ev.Session]; u != nil {
			u.SetDeafened(ev.Deafened)
		}
	})
}

func (c *Controller) onUserPriorityRequested(ev bus.UserPriorityRequested) {
	c.withClient(func(client *gumble.Client) {
		if u := client.Users[ev.Session]; u != nil {
			u.SetPrioritySpeaker(ev.Priority)
		}
	})
}

func (c *Controller) onUserRegisterRequested(ev bus.UserRegisterRequested) {
	c.withClient(func(client *gumble.Client) {
		if u := client.Users[ev.Session]; u != nil {
			u.Register()
		}
	})
}

func (c *Controller) onUserStatsRequested(ev bus.UserStatsRequested) {
	c.withClient(func(client *gumble.Client) {
		u := client.Users[ev.Session]
		if u == nil {
			return
		}
		u.RequestStats()
	})
}

// --- Admin actions ---------------------------------------------------------

func (c *Controller) onUserKickRequested(ev bus.UserKickRequested) {
	c.withClient(func(client *gumble.Client) {
		if u := client.Users[ev.Session]; u != nil {
			u.Kick(ev.Reason)
		}
	})
}

func (c *Controller) onUserBanRequested(ev bus.UserBanRequested) {
	c.withClient(func(client *gumble.Client) {
		if u := client.Users[ev.Session]; u != nil {
			u.Ban(ev.Reason)
		}
	})
}

func (c *Controller) onChannelCreateRequested(ev bus.ChannelCreateRequested) {
	c.withClient(func(client *gumble.Client) {
		parent := client.Channels[ev.ParentID]
		if parent == nil {
			return
		}
		parent.Add(ev.Name, ev.Temporary)
	})
}

func (c *Controller) onChannelDeleteRequested(ev bus.ChannelDeleteRequested) {
	c.withClient(func(client *gumble.Client) {
		if ch := client.Channels[ev.ChannelID]; ch != nil {
			ch.Remove()
		}
	})
}

func (c *Controller) onChannelEditRequested(ev bus.ChannelEditRequested) {
	c.withClient(func(client *gumble.Client) {
		ch := client.Channels[ev.ChannelID]
		if ch == nil {
			return
		}
		if ev.Name != nil {
			ch.SetName(*ev.Name)
		}
		if ev.Description != nil {
			ch.SetDescription(*ev.Description)
		}
		if ev.Position != nil {
			ch.SetPosition(*ev.Position)
		}
		if ev.MaxUsers != nil {
			ch.SetMaxUsers(*ev.MaxUsers)
		}
	})
}

// joinPromptWindow is how long after a join request a refusal to enter
// counts as the answer to it (and asks for the channel password).
const joinPromptWindow = 10 * time.Second

// onPermissionDenied asks for a channel password when the server refused
// the user's own join request (password channels deny Enter to everyone
// without the password as an access token).
func (c *Controller) onPermissionDenied(ev bus.PermissionDenied) {
	if ev.Reason == bus.DenyMissingCertificate {
		c.promptMissingCertificate()
		return
	}
	if ev.Reason != bus.DenyPermission || ev.Permission&int(gumble.PermissionEnter) == 0 || ev.ChannelID == 0 {
		return
	}
	c.mu.Lock()
	recent := c.lastJoin.id == ev.ChannelID && time.Since(c.lastJoin.at) < joinPromptWindow
	c.lastJoin = joinAttempt{}
	c.mu.Unlock()
	if recent {
		c.promptChannelPassword(ev.ChannelID)
	}
}

// promptMissingCertificate offers to create a certificate when the server
// refused something (e.g. self-registration) because the user has none;
// the new certificate is attached to the connection, which reconnects.
func (c *Controller) promptMissingCertificate() {
	c.mu.Lock()
	p := c.lastDial
	busy := c.certPromptOpen
	if p != nil && p.identity == nil && !busy {
		c.certPromptOpen = true
	}
	c.mu.Unlock()
	if p == nil || p.identity != nil || busy {
		return
	}
	done := func() {
		c.mu.Lock()
		c.certPromptOpen = false
		c.mu.Unlock()
	}
	alert := adw.NewAlertDialog(i18n.Tr("Certificate Required"),
		i18n.Tr("The server needs a certificate for this, for example to register your account. Create one now? Murmel then reconnects with it."))
	alert.AddResponse("cancel", i18n.Tr("Cancel"))
	alert.AddResponse("create", i18n.Tr("Create Certificate…"))
	alert.SetResponseAppearance("create", adw.ResponseSuggested)
	alert.SetDefaultResponse("create")
	alert.SetCloseResponse("cancel")
	alert.ConnectResponse(func(r string) {
		done()
		if r != "create" {
			return
		}
		identities.ShowCreate(c.win, c.certs, p.username, func(meta certs.IdentityMeta) {
			c.useNewIdentity(p, meta)
		})
	})
	alert.Present(c.win)
}

// useNewIdentity makes meta the identity of p's connection (default for
// new connections, attached to the saved connection) and reconnects.
func (c *Controller) useNewIdentity(p *dialParams, meta certs.IdentityMeta) {
	ident, err := c.certs.Load(meta.ID)
	if err != nil {
		slog.Info("new identity not loadable", "err", err)
		return
	}
	c.settings.SetLastIdentity(meta.ID)
	if host, portStr, err := net.SplitHostPort(p.addr); err == nil {
		port, _ := strconv.Atoi(portStr)
		servers := c.settings.Servers()
		for i, e := range servers {
			if sameAccount(e, host, port, p.username) {
				servers[i].IdentityID = meta.ID
			}
		}
		c.settings.SetServers(servers)
	}
	retry := *p
	retry.identity = ident
	retry.rememberPassword = false
	c.startDial(&retry)
}

func (c *Controller) promptChannelPassword(channelID uint32) {
	name := ""
	if ch, ok := c.appBus.State().Channels[channelID]; ok {
		name = ch.Name
	}
	entry := adw.NewPasswordEntryRow()
	entry.SetTitle(i18n.Tr("Password"))
	group := adw.NewPreferencesGroup()
	group.Add(entry)

	alert := adw.NewAlertDialog(i18n.Tr("Channel Password"),
		fmt.Sprintf(i18n.Tr("The channel %s is protected by a password."), name))
	alert.SetExtraChild(group)
	alert.AddResponse("cancel", i18n.Tr("Cancel"))
	// TRANSLATORS: Button: join the channel with the password entered.
	alert.AddResponse("join", i18n.Tr("Join"))
	alert.SetResponseAppearance("join", adw.ResponseSuggested)
	alert.SetDefaultResponse("join")
	alert.SetCloseResponse("cancel")
	alert.SetResponseEnabled("join", false)
	enableWhenFilled(entry, alert, "join")
	entry.SetActivatesDefault(true)
	alert.ConnectResponse(func(response string) {
		if response == "join" {
			c.appBus.Emit(bus.AccessTokenAdded{Token: entry.Text(), ChannelID: channelID})
		}
	})
	alert.Present(c.win)
	entry.GrabFocus()
}

// onAccessTokenAdded hands the server the token (with all earlier ones of
// this connection; reconnects send them again) and retries the join.
func (c *Controller) onAccessTokenAdded(ev bus.AccessTokenAdded) {
	c.mu.Lock()
	var tokens []string
	if p := c.lastDial; p != nil {
		if !slices.Contains(p.tokens, ev.Token) {
			p.tokens = append(p.tokens, ev.Token)
		}
		tokens = slices.Clone(p.tokens)
	} else {
		tokens = []string{ev.Token}
	}
	if ev.ChannelID != 0 {
		c.lastJoin = joinAttempt{id: ev.ChannelID, at: time.Now()}
	}
	c.mu.Unlock()
	c.withClient(func(client *gumble.Client) {
		client.Send(gumble.AccessTokens(tokens))
		if ev.ChannelID == 0 || client.Self == nil {
			return
		}
		if ch := client.Channels[ev.ChannelID]; ch != nil {
			client.Self.Move(ch)
		}
	})
}

// onDisconnectRequested leaves the server on the user's request: no
// reconnect, and the UI returns to its start state via Disconnected
// (the connection's own bus is already detached, so it is emitted here).
func (c *Controller) onDisconnectRequested(bus.DisconnectRequested) {
	c.mu.Lock()
	if c.retryStop != nil {
		c.retryStop()
		c.retryStop = nil
	}
	// A dial still running is superseded: it neither connects later nor
	// leaves a prompt open.
	connected := c.endAttemptLocked() || c.client != nil
	c.disconnectLocked()
	c.boff.reset()
	c.lastDial = nil
	c.mu.Unlock()
	if connected {
		slog.Info("disconnected by the user")
		c.appBus.Emit(bus.Disconnected{Reason: bus.DisconnectUser})
	}
}

func (c *Controller) onChannelPermissionRequested(ev bus.ChannelPermissionRequested) {
	c.withClient(func(client *gumble.Client) {
		if ch := client.Channels[ev.ChannelID]; ch != nil && ch.Permission() == nil {
			ch.RequestPermission()
		}
	})
}

func (c *Controller) onChannelDescriptionRequested(ev bus.ChannelDescriptionRequested) {
	c.withClient(func(client *gumble.Client) {
		if ch := client.Channels[ev.ChannelID]; ch != nil && ch.Description == "" && len(ch.DescriptionHash) > 0 {
			ch.RequestDescription()
		}
	})
}

func (c *Controller) onUserTextureRequested(ev bus.UserTextureRequested) {
	c.withClient(func(client *gumble.Client) {
		if u := client.Users[ev.Session]; u != nil && len(u.Texture) == 0 && len(u.TextureHash) > 0 {
			u.RequestTexture()
		}
	})
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
	c.withClient(func(client *gumble.Client) {
		if ch := client.Channels[ev.ChannelID]; ch != nil {
			ch.RequestACL()
		}
	})
}

func (c *Controller) onACLSaveRequested(ev bus.ACLSaveRequested) {
	c.withClient(func(client *gumble.Client) {
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
	})
}

// --- chat.Sender -----------------------------------------------------------

// SendText implements chat.Sender. Called on the main loop.
func (c *Controller) SendText(channelID, session uint32, message string) error {
	connected := c.withClient(func(client *gumble.Client) {
		if session != 0 {
			if u := client.Users[session]; u != nil {
				u.Send(message)
			}
			return
		}
		if ch := client.Channels[channelID]; ch != nil {
			ch.Send(message, false)
		}
	})
	if !connected {
		return fmt.Errorf("not connected")
	}
	return nil
}

// --- TOFU dialogs ----------------------------------------------------------

// makeVerifyCallback returns a certs.VerifyCallback that blocks the dial
// goroutine while showing a GTK4 trust dialog on the main loop.
func (c *Controller) makeVerifyCallback(a attempt) certs.VerifyCallback {
	return func(result certs.VerifyResult, stored, presented certs.ServerEntry) error {
		switch result {
		case certs.VerifyMatch:
			return nil // already pinned — proceed silently
		case certs.VerifyNew:
			trusted := c.blockForTOFUDialog(a, nil, presented)
			if !trusted {
				return errDeclinedTOFU
			}
			return nil
		case certs.VerifyChanged:
			trusted := c.blockForTOFUDialog(a, &stored, presented)
			if !trusted {
				return errDeclinedTOFU
			}
			return nil
		}
		return errDeclinedTOFU
	}
}

// blockForTOFUDialog shows a TOFU dialog on the main loop and blocks the
// caller (which runs on a goroutine) until the user responds. stored is the
// previously trusted certificate when it changed, nil for a new server. A
// superseded attempt counts as declined (nothing is pinned) and closes
// the dialog.
func (c *Controller) blockForTOFUDialog(a attempt, stored *certs.ServerEntry, presented certs.ServerEntry) bool {
	ch := make(chan bool, 1)
	glib.IdleAdd(func() bool {
		c.showTOFUDialog(a, stored, presented, ch)
		return false
	})
	select {
	case trusted := <-ch:
		return trusted && c.current(a)
	case <-a.ctx.Done():
		return false
	}
}

func (c *Controller) showTOFUDialog(a attempt, stored *certs.ServerEntry, presented certs.ServerEntry, ch chan<- bool) {
	if !c.current(a) {
		ch <- false
		return
	}
	changed := stored != nil
	var heading, body string
	if changed {
		heading = i18n.Tr("Server Certificate Changed")
		body = fmt.Sprintf(
			i18n.Tr("The server's certificate has changed.\n\nPreviously trusted fingerprint (since %s):\n%s\n\nNew fingerprint:\n%s\n\nOnly continue if you trust this certificate."),
			clock.Date(stored.FirstSeen), fingerprintLines(stored.Fingerprint), fingerprintLines(presented.Fingerprint))
	} else {
		heading = i18n.Tr("New Server Certificate")
		body = fmt.Sprintf(
			i18n.Tr("This server's certificate has not been seen before.\n\nFingerprint:\n%s\n\nTrust and connect?"),
			fingerprintLines(presented.Fingerprint))
	}

	alert := adw.NewAlertDialog(heading, body)
	alert.AddResponse("abort", i18n.Tr("Cancel"))
	alert.AddResponse("trust", i18n.Tr("Trust and Connect"))
	alert.SetCloseResponse("abort")
	if changed {
		// Enter must not accept a changed certificate.
		alert.SetDefaultResponse("abort")
		alert.SetResponseAppearance("trust", adw.ResponseDestructive)
	} else {
		alert.SetDefaultResponse("trust")
		alert.SetResponseAppearance("trust", adw.ResponseSuggested)
	}
	alert.ConnectResponse(func(resp string) {
		select {
		case ch <- (resp == "trust"):
		default:
		}
	})
	closeWhenSuperseded(a, &alert.Dialog)
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
		heading = i18n.Tr("Authentication Failed")
		body = i18n.Tr("The server rejected the password or credentials.")
	case classBanned:
		heading = i18n.Tr("Banned")
		body = i18n.Tr("You are banned from this server.")
	case classServerFull:
		heading = i18n.Tr("Server Full")
		body = i18n.Tr("The server has reached its maximum number of users.")
	case classTLSFailure:
		heading = i18n.Tr("TLS Handshake Failed")
		body = fmt.Sprintf(i18n.Tr("Could not establish a secure connection:\n%s"), err.Error())
	case classUnreachable:
		heading = i18n.Tr("Server Unreachable")
		body = fmt.Sprintf(i18n.Tr("The server could not be reached:\n%s"), unreachableDetail(err))
	default:
		heading = i18n.Tr("Connection Failed")
		body = err.Error()
	}

	alert := adw.NewAlertDialog(heading, body)
	alert.AddResponse("ok", i18n.Tr("OK"))
	alert.SetDefaultResponse("ok")
	alert.Present(c.win)
}

// --- Helpers ---------------------------------------------------------------

// fingerprintLines breaks a colon-separated fingerprint ("aa:bb:…") into
// lines of 8 bytes, so the 32 bytes of a SHA-256 fingerprint read as four
// even lines in the narrow trust dialog.
func fingerprintLines(fp string) string {
	const perLine = 8
	parts := strings.Split(fp, ":")
	var lines []string
	for len(parts) > perLine {
		lines = append(lines, strings.Join(parts[:perLine], ":"))
		parts = parts[perLine:]
	}
	lines = append(lines, strings.Join(parts, ":"))
	return strings.Join(lines, "\n")
}

// transmitMode maps the transmission-mode setting to the engine's mode.
func transmitMode(v string) engine.TransmitMode {
	switch v {
	case "push-to-talk":
		return engine.TransmitPushToTalk
	case "continuous":
		return engine.TransmitContinuous
	}
	return engine.TransmitVoiceActivation
}
