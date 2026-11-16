package connection

import (
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/certs"
	"io.github/murmel/internal/settings"
	"io.github/murmel/internal/ui"
	"io.github/murmel/internal/ui/avatarpick"
	"io.github/murmel/internal/ui/identities"
	"io.github/murmel/internal/ui/secrets"
)

// connectForm is the form of the connect dialog: the server and account
// groups. Main loop only.
type connectForm struct {
	req     ui.ConnectRequest
	reqPort int // the request's port, or the default port

	host     *adw.EntryRow
	port     *adw.EntryRow
	password *adw.PasswordEntryRow
	user     *adw.EntryRow

	// pwField says whether the field may update the keyring; text set by
	// the form itself (setPassword) does not count as the user's edit.
	pwField         passwordField
	settingPassword bool

	idList        []certs.IdentityMeta
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
// certificate dialogs it opens).
func (c *Controller) newConnectForm(dialog *adw.Dialog, req ui.ConnectRequest) *connectForm {
	f := &connectForm{req: req, reqPort: req.Port}
	if f.reqPort == 0 {
		f.reqPort = ui.DefaultPort
	}

	// --- Server group
	f.host = adw.NewEntryRow()
	f.host.SetTitle(tr("Address"))
	f.host.SetText(req.Host)

	f.port = adw.NewEntryRow()
	f.port.SetTitle(tr("Port"))
	f.port.SetText(strconv.Itoa(f.reqPort))
	f.port.SetInputPurpose(gtk.InputPurposeDigits)

	f.password = adw.NewPasswordEntryRow()
	f.password.SetTitle(tr("Server Password"))
	f.password.SetText(req.Password)
	f.password.ConnectChanged(func() {
		if !f.settingPassword {
			f.pwField.edited()
		}
	})

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
	f.idList, _ = c.certs.List()
	f.identityStore = gtk.NewStringList([]string{tr("None (anonymous)")})
	for _, id := range f.idList {
		f.identityStore.Append(id.Name)
	}
	f.identityRow = adw.NewComboRow()
	f.identityRow.SetTitle(tr("User Certificate"))
	f.identityRow.SetModel(f.identityStore)
	f.selectIdentity(c.settings.LastIdentity())

	// Import button inside the certificate row keeps the form compact.
	importBtn := gtk.NewButtonFromIconName("document-open-symbolic")
	importBtn.AddCSSClass("flat")
	importBtn.SetVAlign(gtk.AlignCenter)
	importBtn.SetTooltipText(tr("Import certificate (PEM file with certificate and private key)"))
	importBtn.ConnectClicked(func() {
		c.pickCertificateFile(dialog, f.addIdentity)
	})
	f.identityRow.AddSuffix(importBtn)

	createBtn := gtk.NewButtonFromIconName("list-add-symbolic")
	createBtn.AddCSSClass("flat")
	createBtn.SetVAlign(gtk.AlignCenter)
	createBtn.SetTooltipText(tr("Create a new certificate"))
	createBtn.ConnectClicked(func() {
		identities.ShowCreate(dialog, c.certs, strings.TrimSpace(f.user.Text()), f.addIdentity)
	})
	f.identityRow.AddSuffix(createBtn)

	// --- Save switch: the name preview follows the form.
	f.saveRow = adw.NewSwitchRow()
	f.saveRow.SetTitle(tr("Save Connection"))
	f.saveRow.SetActive(true)
	f.host.ConnectChanged(f.updateSaveName)
	f.port.ConnectChanged(f.updateSaveName)
	f.user.ConnectChanged(f.updateSaveName)
	f.updateSaveName()

	// Optional avatar for this account; without one the default avatar
	// (Preferences) is sent.
	f.avatars = c.avatarStore()
	f.defaultAvatar = f.avatars.Load(avatarpick.DefaultKey)
	unset := tr("Optional")
	if f.defaultAvatar != nil {
		unset = tr("Optional — otherwise your default avatar is used")
	}
	f.avatarRow = avatarpick.NewRow(tr("Avatar"), req.Username, unset, func([]byte) { f.avatarChanged = true })
	f.avatarRow.SetFallback(f.defaultAvatar)
	if req.Host != "" {
		f.avatarRow.SetData(f.avatars.Load(avatarpick.AccountKey(req.Host, f.reqPort, req.Username)))
	}
	f.user.ConnectChanged(func() { f.avatarRow.SetName(f.user.Text()) })

	f.account = adw.NewPreferencesGroup()
	f.account.SetTitle(tr("Account"))
	f.account.Add(f.user)
	f.account.Add(f.avatarRow)
	f.account.Add(f.identityRow)
	f.account.Add(f.saveRow)

	f.host.ConnectChanged(func() { f.host.RemoveCSSClass("error") })
	return f
}

// setPassword sets the password field without counting as the user's edit.
func (f *connectForm) setPassword(pw string) {
	f.settingPassword = true
	f.password.SetText(pw)
	f.settingPassword = false
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

// formPort is the port in the form, or the default port.
func (f *connectForm) formPort() int {
	p, err := strconv.Atoi(f.port.Text())
	if err != nil || p <= 0 || p > 65535 {
		return ui.DefaultPort
	}
	return p
}

func (f *connectForm) updateSaveName() {
	if strings.TrimSpace(f.host.Text()) == "" {
		f.saveRow.SetSubtitle(tr("Saved as username@address"))
		return
	}
	f.saveRow.SetSubtitle(fmt.Sprintf(tr("Saved as %s"),
		savedName(strings.TrimSpace(f.user.Text()), strings.TrimSpace(f.host.Text()), f.formPort())))
}

// fill shows a saved connection in the form; the password comes from the
// keyring.
func (f *connectForm) fill(e settings.ServerEntry) {
	f.host.SetText(e.Host)
	f.port.SetText(strconv.Itoa(e.Port))
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
			// Only if the form still shows this account.
			if f.host.Text() != e.Host || f.user.Text() != e.Username {
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
// dialog unless the form is incomplete.
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
		if id, lerr := c.certs.Load(f.idList[sel-1].ID); lerr == nil {
			identity = id
			identityID = f.idList[sel-1].ID
		}
	}
	c.settings.SetLastIdentity(identityID)

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
