package admin

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// Pages of the channel editor (ShowChannelEditor's page argument).
const (
	PageGeneral     = "general"
	PageDescription = "description"
	PageGroups      = "groups"
	PagePermissions = "permissions"
)

const (
	editorWidth  = 640
	editorHeight = 620
	maxPosition  = 10000
	maxUsersCap  = 10000
)

// channelEditor is the "Edit Channel" dialog: one page per aspect of the
// channel, saved together. The channel's ACL (password, groups,
// permissions) is fetched when the dialog opens.
type channelEditor struct {
	b    *bus.Bus
	info bus.ChannelInfo

	dialog  *adw.Dialog
	stack   *adw.ViewStack
	saveBtn *gtk.Button

	// General page
	name        *adw.EntryRow
	position    *adw.SpinRow
	maxUsers    *adw.SpinRow
	password    *adw.PasswordEntryRow
	accessGroup *adw.PreferencesGroup

	// acl is nil until the server sent it (or refused: aclDenied).
	acl       *bus.ACLFetched
	aclDenied bool
	origPW    string
}

// ShowChannelEditor opens the editor for info's channel on page (one of the
// Page constants; "" = general). Called on the main loop.
func ShowChannelEditor(parent gtk.Widgetter, b *bus.Bus, info bus.ChannelInfo, page string) {
	e := &channelEditor{b: b, info: info}

	e.stack = adw.NewViewStack()
	e.addPage(PageGeneral, tr("General"), "document-properties-symbolic", e.generalPage())

	e.saveBtn = gtk.NewButtonWithLabel(tr("Save"))
	e.saveBtn.AddCSSClass("suggested-action")
	cancelBtn := gtk.NewButtonWithLabel(tr("Cancel"))

	switcher := adw.NewViewSwitcher()
	switcher.SetStack(e.stack)
	switcher.SetPolicy(adw.ViewSwitcherPolicyWide)

	hdr := adw.NewHeaderBar()
	hdr.SetShowEndTitleButtons(false)
	hdr.SetShowStartTitleButtons(false)
	hdr.PackStart(cancelBtn)
	hdr.PackEnd(e.saveBtn)
	hdr.SetTitleWidget(switcher)

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(hdr)
	toolbar.SetContent(e.stack)

	e.dialog = adw.NewDialog()
	e.dialog.SetTitle(fmt.Sprintf(tr("Edit Channel %s"), info.Name))
	e.dialog.SetContentWidth(editorWidth)
	e.dialog.SetContentHeight(editorHeight)
	e.dialog.SetChild(toolbar)

	if page != "" && e.stack.ChildByName(page) != nil {
		e.stack.SetVisibleChildName(page)
	}

	subscribeWhileOpen(e.dialog, b, func(ev bus.ACLFetched) {
		if ev.ChannelID != info.ID || e.acl != nil {
			return
		}
		e.acl = &ev
		e.aclLoaded()
	})
	subscribeWhileOpen(e.dialog, b, func(ev bus.PermissionDenied) {
		if e.acl == nil && !e.aclDenied {
			e.aclDenied = true
			e.aclLoaded()
		}
	})

	cancelBtn.ConnectClicked(func() { e.dialog.Close() })
	e.saveBtn.ConnectClicked(e.save)

	b.Emit(bus.ACLRequested{ChannelID: info.ID})
	e.dialog.Present(parent)
	e.name.GrabFocus()
}

// addPage puts content (scrollable, with the usual margins) on the stack.
func (e *channelEditor) addPage(name, title, icon string, content gtk.Widgetter) {
	clamp := adw.NewClamp()
	clamp.SetMaximumSize(editorWidth - 48)
	clamp.SetChild(content)
	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)
	scroll.SetChild(clamp)
	e.stack.AddTitledWithIcon(scroll, name, title, icon)
}

func pageBox() *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 18)
	box.SetMarginTop(18)
	box.SetMarginBottom(24)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)
	return box
}

// --- General ---------------------------------------------------------------

func (e *channelEditor) generalPage() gtk.Widgetter {
	e.name = adw.NewEntryRow()
	e.name.SetTitle(tr("Name"))
	e.name.SetText(e.info.Name)
	e.name.ConnectChanged(func() { e.name.RemoveCSSClass("error") })

	e.position = adw.NewSpinRowWithRange(-maxPosition, maxPosition, 1)
	e.position.SetTitle(tr("Position"))
	e.position.SetSubtitle(tr("Order among sibling channels: lower numbers come first"))
	e.position.SetValue(float64(e.info.Position))

	e.maxUsers = adw.NewSpinRowWithRange(0, maxUsersCap, 1)
	e.maxUsers.SetTitle(tr("Maximum Users"))
	e.maxUsers.SetSubtitle(tr("0 = no limit beyond the server's own"))
	e.maxUsers.SetValue(float64(e.info.MaxUsers))

	props := adw.NewPreferencesGroup()
	props.Add(e.name)
	props.Add(e.position)
	props.Add(e.maxUsers)
	if e.info.Temporary {
		tmp := adw.NewActionRow()
		tmp.SetTitle(tr("Temporary"))
		tmp.SetSubtitle(tr("Removed automatically when the last user leaves"))
		props.Add(tmp)
	}

	e.password = adw.NewPasswordEntryRow()
	e.password.SetTitle(tr("Password"))
	e.password.SetSensitive(false) // until the ACL is known

	access := adw.NewPreferencesGroup()
	access.SetTitle(tr("Access"))
	access.SetDescription(tr("Loading the channel's permissions…"))
	access.Add(e.password)
	e.accessGroup = access

	box := pageBox()
	box.Append(props)
	box.Append(access)
	return box
}

// aclLoaded enables what depends on the ACL (or explains why not).
func (e *channelEditor) aclLoaded() {
	if e.acl == nil {
		e.accessGroup.SetDescription(tr("You may not view this channel's permissions, so its password cannot be changed here."))
		return
	}
	e.origPW = channelPassword(e.acl.Rules)
	e.password.SetText(e.origPW)
	e.password.SetSensitive(true)
	e.accessGroup.SetDescription(tr("Users need this password to enter the channel. Leave it empty for an open channel."))
}

// --- Save ------------------------------------------------------------------

func (e *channelEditor) save() {
	name := e.name.Text()
	if name == "" {
		e.name.AddCSSClass("error")
		e.stack.SetVisibleChildName(PageGeneral)
		e.name.GrabFocus()
		return
	}
	edit := bus.ChannelEditRequested{ChannelID: e.info.ID}
	changed := false
	if name != e.info.Name {
		edit.Name, changed = &name, true
	}
	if pos := int32(e.position.Value()); pos != e.info.Position {
		edit.Position, changed = &pos, true
	}
	if mu := uint32(e.maxUsers.Value()); mu != e.info.MaxUsers {
		edit.MaxUsers, changed = &mu, true
	}
	if changed {
		e.b.Emit(edit)
	}

	if e.acl != nil {
		if pw := e.password.Text(); pw != e.origPW {
			e.b.Emit(bus.ACLSaveRequested{
				ChannelID: e.info.ID,
				Inherits:  e.acl.Inherits,
				Groups:    ownGroups(e.acl.Groups),
				Rules:     withPassword(e.acl.Rules, pw),
			})
		}
	}
	e.dialog.Close()
}

// ownGroups are the groups an ACL write must repeat: the server replaces
// all of a channel's group definitions with the ones sent. Those are the
// channel's own groups and inherited groups it modifies locally.
func ownGroups(groups []bus.ACLGroupInfo) []bus.ACLGroupInfo {
	var out []bus.ACLGroupInfo
	for _, g := range groups {
		if !g.Inherited || len(g.UsersAdd) > 0 || len(g.UsersRemove) > 0 || !g.InheritUsers || !g.Inheritable {
			out = append(out, g)
		}
	}
	return out
}
