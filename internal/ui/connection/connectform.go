package connection

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/certs"
	"github.com/one337/murmel/internal/settings"
	"github.com/one337/murmel/internal/ui"
	"github.com/one337/murmel/internal/ui/avatarpick"
	"github.com/one337/murmel/internal/ui/identities"
	"github.com/one337/murmel/internal/ui/secrets"
	"github.com/one337/murmel/internal/ui/weakref"
)

// connectForm is the form of the connect dialog: the server and account
// groups. It lives as long as the dialog (weakref.Owned); the closures of
// its widgets reach it through a weak pointer. Main loop only.
type connectForm struct {
	ctx     context.Context // ends when the dialog closes
	req     ui.ConnectRequest
	reqPort int // the request's port, or the default port

	host     *adw.EntryRow
	port     *adw.SpinRow
	password *adw.PasswordEntryRow
	user     *adw.EntryRow

	// pwField says whether the field may update the keyring; text set by
	// the form itself (setPassword) does not count as the user's edit.
	pwField         passwordField
	settingPassword bool

	idList        []certs.IdentityMeta
	listed        bool // the stored certificates could be read
	identityStore *gtk.StringList
	identityRow   *adw.ComboRow

	saveRow *adw.SwitchRow

	avatars       *avatarpick.Store
	defaultAvatar []byte
	avatarRow     *avatarpick.Row
	avatarChanged bool

	server  *adw.PreferencesGroup
	account *adw.PreferencesGroup
}

// newConnectForm builds the form for req inside dialog (the parent of the
// certificate dialogs it opens); ctx ends when the dialog closes.
func (c *Controller) newConnectForm(dialog *adw.Dialog, ctx context.Context, req ui.ConnectRequest) *connectForm {
	f := &connectForm{ctx: ctx, req: req, reqPort: req.Port}
	if f.reqPort == 0 {
		f.reqPort = ui.DefaultPort
	}
	// The widgets' closures hold the form and the dialog only weakly.
	wf := weakref.Owned(dialog, f)
	dialogRef := weakref.New(dialog)
	with := func(fn func(f *connectForm)) func() {
		return func() {
			if f := wf.Value(); f != nil {
				fn(f)
			}
		}
	}

	// --- Server group
	f.host = adw.NewEntryRow()
	f.host.SetTitle(tr("Address"))
	f.host.SetText(req.Host)

	f.port = adw.NewSpinRow(gtk.NewAdjustment(float64(f.reqPort), 1, 65535, 1, 100, 0), 1, 0)
	f.port.SetTitle(tr("Port"))
	f.port.SetNumeric(true)

	f.password = adw.NewPasswordEntryRow()
	f.password.SetTitle(tr("Server Password"))
	f.password.SetText(req.Password)
	f.password.ConnectChanged(with(func(f *connectForm) {
		if !f.settingPassword {
			f.pwField.edited()
		}
	}))

	f.server = adw.NewPreferencesGroup()
	f.server.SetTitle(tr("Server"))
	f.server.Add(f.host)
	f.server.Add(f.port)
	f.server.Add(f.password)

	// --- Account group
	f.user = adw.NewEntryRow()
	f.user.SetTitle(tr("Username"))
	f.user.SetText(req.Username)

	// Certificate picker: "None" + stored idList. The import row below
	// adds a certificate file to the store and selects it.
	var err error
	f.idList, err = c.certs.List()
	f.listed = err == nil
	if err != nil {
		slog.Warn("connect: certificates not listed", "err", err)
	}
	f.identityStore = gtk.NewStringList([]string{tr("None (anonymous)")})
	for _, id := range f.idList {
		f.identityStore.Append(id.Name)
	}
	f.identityRow = adw.NewComboRow()
	f.identityRow.SetTitle(tr("User Certificate"))
	f.identityRow.SetModel(f.identityStore)
	f.identityNote("")
	coreglib.InternObject(f.identityRow).NotifyProperty("selected", with(func(f *connectForm) { f.identityNote("") }))
	f.selectIdentity(c.settings.LastIdentity())

	// Import button inside the certificate row keeps the form compact.
	importBtn := gtk.NewButtonFromIconName("document-open-symbolic")
	importBtn.AddCSSClass("flat")
	importBtn.SetVAlign(gtk.AlignCenter)
	importBtn.SetTooltipText(tr("Import certificate (PEM file with certificate and private key)"))
	importBtn.ConnectClicked(with(func(f *connectForm) {
		dialogRef.Do(func(dialog *adw.Dialog) { c.pickCertificateFile(dialog, f.addIdentity) })
	}))
	f.identityRow.AddSuffix(importBtn)

	createBtn := gtk.NewButtonFromIconName("list-add-symbolic")
	createBtn.AddCSSClass("flat")
	createBtn.SetVAlign(gtk.AlignCenter)
	createBtn.SetTooltipText(tr("Create a new certificate"))
	createBtn.ConnectClicked(with(func(f *connectForm) {
		dialogRef.Do(func(dialog *adw.Dialog) {
			identities.ShowCreate(dialog, c.certs, strings.TrimSpace(f.user.Text()), f.addIdentity)
		})
	}))
	f.identityRow.AddSuffix(createBtn)

	// --- Save switch: the name preview follows the form.
	f.saveRow = adw.NewSwitchRow()
	f.saveRow.SetTitle(tr("Save Connection"))
	f.saveRow.SetActive(true)
	updateSaveName := with((*connectForm).updateSaveName)
	f.host.ConnectChanged(updateSaveName)
	f.port.ConnectChanged(updateSaveName)
	f.user.ConnectChanged(updateSaveName)
	f.updateSaveName()

	// Optional avatar for this account; without one the default avatar
	// (Preferences) is sent.
	f.avatars = c.avatarStore()
	f.defaultAvatar = f.avatars.Load(avatarpick.DefaultKey)
	unset := tr("Optional")
	if f.defaultAvatar != nil {
		unset = tr("Optional — otherwise your default avatar is used")
	}
	f.avatarRow = avatarpick.NewRow(tr("Avatar"), req.Username, unset, func([]byte) {
		with(func(f *connectForm) { f.avatarChanged = true })()
	})
	f.avatarRow.SetFallback(f.defaultAvatar)
	if req.Host != "" {
		f.avatarRow.SetData(f.avatars.Load(avatarpick.AccountKey(req.Host, f.reqPort, req.Username)))
	}
	f.user.ConnectChanged(with(func(f *connectForm) { f.avatarRow.SetName(f.user.Text()) }))

	f.account = adw.NewPreferencesGroup()
	f.account.SetTitle(tr("Account"))
	f.account.Add(f.user)
	f.account.Add(f.avatarRow)
	f.account.Add(f.identityRow)
	f.account.Add(f.saveRow)

	f.host.ConnectChanged(with(func(f *connectForm) { f.host.RemoveCSSClass("error") }))
	return f
}

// setPassword sets the password field without counting as the user's edit.
func (f *connectForm) setPassword(pw string) {
	f.settingPassword = true
	f.password.SetText(pw)
	f.settingPassword = false
}

// identityNote shows problem under the certificate row, or (problem "")
// that the stored certificates could not be read, if so.
func (f *connectForm) identityNote(problem string) {
	if problem == "" && !f.listed {
		problem = tr("Your certificates could not be read.")
	}
	f.identityRow.SetSubtitle(problem)
	if problem != "" {
		f.identityRow.AddCSSClass("error")
	} else {
		f.identityRow.RemoveCSSClass("error")
	}
}

// rememberIdentity says whether connecting stores id as the last used
// certificate: not when the stored certificates could not be read and
// none is selected, so a read error does not make Murmel forget it.
func rememberIdentity(listed bool, id string) bool {
	return listed || id != ""
}

// selectIdentity selects the certificate with id, or "None".
func (f *connectForm) selectIdentity(id string) {
	f.identityRow.SetSelected(0)
	for i, m := range f.idList {
		if m.ID == id {
			f.identityRow.SetSelected(uint(i + 1))
			return
		}
	}
}

// addIdentity adds an imported or created certificate and selects it.
func (f *connectForm) addIdentity(meta certs.IdentityMeta) {
	f.idList = append(f.idList, meta)
	f.identityStore.Append(meta.Name)
	f.identityRow.SetSelected(uint(len(f.idList)))
}

// typedPort is the port as typed so far, for the name preview: the field
// takes typed text only when it is confirmed or left.
func (f *connectForm) typedPort() int {
	if p, err := strconv.Atoi(strings.TrimSpace(f.port.Text())); err == nil && p >= 1 && p <= 65535 {
		return p
	}
	return int(f.port.Value())
}

// formPort takes over typed text and returns the port (1–65535).
func (f *connectForm) formPort() int {
	f.port.Update()
	return int(f.port.Value())
}

func (f *connectForm) updateSaveName() {
	if strings.TrimSpace(f.host.Text()) == "" {
		f.saveRow.SetSubtitle(tr("Saved as username@address"))
		return
	}
	f.saveRow.SetSubtitle(fmt.Sprintf(tr("Saved as %s"),
		savedName(strings.TrimSpace(f.user.Text()), strings.TrimSpace(f.host.Text()), f.typedPort())))
}

// fill shows a saved connection in the form; the password comes from the
// keyring.
func (f *connectForm) fill(e settings.ServerEntry) {
	f.host.SetText(e.Host)
	f.port.SetValue(float64(e.Port))
	f.user.SetText(e.Username)
	f.selectIdentity(e.IdentityID)
	f.setPassword("")
	f.saveRow.SetActive(true)
	f.avatarRow.SetData(f.avatars.Load(avatarpick.AccountKey(e.Host, e.Port, e.Username)))
	f.avatarChanged = false
	key := secrets.Key{Host: e.Host, Port: e.Port, Username: e.Username}
	gen := f.pwField.startLookup()
	go func() {
		pw, err := secrets.Lookup(key)
		if err != nil {
			slog.Info("keyring lookup failed", "err", err)
		}
		glib.IdleAdd(func() bool {
			// Only if the dialog is open and still shows this account.
			if f.ctx.Err() != nil || f.host.Text() != e.Host || f.user.Text() != e.Username {
				return false
			}
			if f.pwField.lookupDone(gen, err == nil) {
				f.setPassword(pw)
			}
			return false
		})
	}()
}

// connectFromForm saves what the form asks for and connects; it closes
// dialog unless the form is incomplete or the selected certificate cannot
// be loaded.
func (c *Controller) connectFromForm(dialog *adw.Dialog, f *connectForm) {
	h := strings.TrimSpace(f.host.Text())
	if h == "" {
		f.host.AddCSSClass("error")
		f.host.GrabFocus()
		return
	}
	p := f.formPort()
	user := strings.TrimSpace(f.user.Text())
	if user == "" {
		user = "Murmel"
	}
	password := f.password.Text()

	var identity *certs.Identity
	identityID := ""
	sel := int(f.identityRow.Selected())
	if sel > 0 && sel-1 < len(f.idList) {
		id, err := c.certs.Load(f.idList[sel-1].ID)
		if err != nil {
			// Never connect anonymously instead of with the chosen certificate.
			slog.Warn("connect: certificate not loaded", "id", f.idList[sel-1].ID, "err", err)
			f.identityNote(tr("This certificate could not be loaded."))
			return
		}
		identity = id
		identityID = f.idList[sel-1].ID
	}
	if rememberIdentity(f.listed, identityID) {
		c.settings.SetLastIdentity(identityID)
	}

	texture := f.avatarRow.Data()
	if f.saveRow.Active() && (f.avatarChanged || texture != nil) {
		if err := f.avatars.Save(avatarpick.AccountKey(h, p, user), texture); err != nil {
			slog.Info("avatar not saved", "err", err)
		}
	}
	if texture == nil {
		texture = f.defaultAvatar
	}
	if f.saveRow.Active() {
		c.saveServerEntry(settings.ServerEntry{
			Name:       savedName(user, h, p),
			Host:       h,
			Port:       p,
			Username:   user,
			IdentityID: identityID,
		})
	}

	dialog.Close()
	params := &dialParams{
		addr:     net.JoinHostPort(h, strconv.Itoa(p)),
		username: user,
		password: password,
		identity: identity,
		texture:  texture,
		// The password reaches the keyring only once the server
		// accepted it, and not while the saved one is still loading.
		rememberPassword: f.saveRow.Active() && f.pwField.known(),
	}
	// A link's channel applies only to the server the link named.
	if strings.EqualFold(h, f.req.Host) && p == f.reqPort {
		params.channelPath = f.req.ChannelPath
	}
	c.startDial(params)
}
