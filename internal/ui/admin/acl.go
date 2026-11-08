package admin

import (
	"fmt"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/protocol/gumble"
)

// permDef names one Mumble permission for the ACL editor UI.
type permDef struct {
	perm gumble.Permission
	name string
}

var permDefs = []permDef{
	{gumble.PermissionWrite, "Write"},
	{gumble.PermissionTraverse, "Traverse"},
	{gumble.PermissionEnter, "Enter"},
	{gumble.PermissionSpeak, "Speak"},
	{gumble.PermissionMuteDeafen, "Mute/Deafen"},
	{gumble.PermissionMove, "Move"},
	{gumble.PermissionMakeChannel, "Make Channel"},
	{gumble.PermissionLinkChannel, "Link Channel"},
	{gumble.PermissionWhisper, "Whisper"},
	{gumble.PermissionTextMessage, "Text Message"},
	{gumble.PermissionMakeTemporaryChannel, "Make Temp Channel"},
	{gumble.PermissionKick, "Kick"},
	{gumble.PermissionBan, "Ban"},
	{gumble.PermissionRegister, "Register"},
	{gumble.PermissionRegisterSelf, "Register Self"},
}

// ruleWidget holds the live edit widgets for one ACL rule row.
type ruleWidget struct {
	info          bus.ACLRuleInfo
	expander      *adw.ExpanderRow
	applyCurrent  *adw.SwitchRow
	applyChildren *adw.SwitchRow
	allow         []*gtk.CheckButton // indexed parallel to permDefs
	deny          []*gtk.CheckButton
	deleted       bool
}

// ShowACLEditor opens the channel ACL editor dialog. It emits
// bus.ACLRequested; when bus.ACLFetched arrives the editor is populated.
// Save emits bus.ACLSaveRequested. Called on the main loop.
func ShowACLEditor(parent gtk.Widgetter, b *bus.Bus, channelID uint32, channelName string) {
	// --- inherit row ---
	inheritRow := adw.NewSwitchRow()
	inheritRow.SetTitle(tr("Inherit parent ACLs"))
	inheritRow.SetActive(true)

	inheritList := gtk.NewListBox()
	inheritList.SetSelectionMode(gtk.SelectionNone)
	inheritList.AddCSSClass("boxed-list")
	inheritList.Append(inheritRow)

	// --- rules list ---
	rulesList := gtk.NewListBox()
	rulesList.SetSelectionMode(gtk.SelectionNone)
	rulesList.AddCSSClass("boxed-list")

	loadingRow := gtk.NewListBoxRow()
	loadingRow.SetSelectable(false)
	loadingRow.SetActivatable(false)
	spinner := gtk.NewSpinner()
	spinner.SetSpinning(true)
	spinner.SetMarginTop(16)
	spinner.SetMarginBottom(16)
	spinner.SetHAlign(gtk.AlignCenter)
	loadingRow.SetChild(spinner)
	rulesList.Append(loadingRow)

	var ruleWidgets []*ruleWidget

	// --- layout ---
	contentBox := gtk.NewBox(gtk.OrientationVertical, 12)
	contentBox.SetMarginTop(12)
	contentBox.SetMarginBottom(12)
	contentBox.SetMarginStart(12)
	contentBox.SetMarginEnd(12)
	contentBox.Append(inheritList)
	contentBox.Append(rulesList)

	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(contentBox)
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetVExpand(true)

	// --- header ---
	saveBtn := gtk.NewButtonWithLabel(tr("Save"))
	saveBtn.AddCSSClass("suggested-action")
	saveBtn.SetSensitive(false)
	cancelBtn := gtk.NewButtonWithLabel(tr("Cancel"))

	hdr := adw.NewHeaderBar()
	hdr.SetShowEndTitleButtons(false)
	hdr.SetShowStartTitleButtons(false)
	hdr.PackStart(cancelBtn)
	hdr.PackEnd(saveBtn)
	hdr.SetTitleWidget(adw.NewWindowTitle(tr("Channel ACL"), channelName))

	toolbar := adw.NewToolbarView()
	toolbar.AddTopBar(hdr)
	toolbar.SetContent(scroll)

	dialog := adw.NewDialog()
	dialog.SetTitle(tr("Channel ACL"))
	dialog.SetContentWidth(580)
	dialog.SetContentHeight(500)
	dialog.SetChild(toolbar)

	// --- one-shot subscription ---
	var unsub func()
	unsub = bus.Subscribe(b, func(ev bus.ACLFetched) {
		if ev.ChannelID != channelID {
			return
		}
		if unsub != nil {
			f := unsub
			unsub = nil
			f()
		}

		inheritRow.SetActive(ev.Inherits)
		rulesList.Remove(loadingRow)

		ruleWidgets = make([]*ruleWidget, 0, len(ev.Rules))
		for _, rule := range ev.Rules {
			rw := buildRuleWidget(rule)
			ruleWidgets = append(ruleWidgets, rw)
			rulesList.Append(rw.expander)
		}

		if len(ev.Rules) == 0 {
			emptyRow := gtk.NewListBoxRow()
			emptyRow.SetSelectable(false)
			emptyRow.SetActivatable(false)
			lbl := gtk.NewLabel(tr("No rules"))
			lbl.AddCSSClass("dim-label")
			lbl.SetMarginTop(16)
			lbl.SetMarginBottom(16)
			emptyRow.SetChild(lbl)
			rulesList.Append(emptyRow)
		}

		saveBtn.SetSensitive(true)
	})

	saveBtn.ConnectClicked(func() {
		if unsub != nil {
			unsub()
			unsub = nil
		}
		rules := make([]bus.ACLRuleInfo, 0, len(ruleWidgets))
		for _, rw := range ruleWidgets {
			if rw.deleted {
				continue
			}
			r := rw.info
			r.AppliesCurrent = rw.applyCurrent.Active()
			r.AppliesChildren = rw.applyChildren.Active()
			r.Granted = 0
			r.Denied = 0
			for i, pd := range permDefs {
				if rw.allow[i].Active() {
					r.Granted |= int(pd.perm)
				}
				if rw.deny[i].Active() {
					r.Denied |= int(pd.perm)
				}
			}
			rules = append(rules, r)
		}
		b.Emit(bus.ACLSaveRequested{
			ChannelID: channelID,
			Inherits:  inheritRow.Active(),
			Rules:     rules,
		})
		dialog.Close()
	})

	cancelBtn.ConnectClicked(func() {
		if unsub != nil {
			unsub()
			unsub = nil
		}
		dialog.Close()
	})

	b.Emit(bus.ACLRequested{ChannelID: channelID})
	dialog.Present(parent)
}

// buildRuleWidget creates an ExpanderRow for one editable ACL rule.
func buildRuleWidget(rule bus.ACLRuleInfo) *ruleWidget {
	rw := &ruleWidget{
		info:  rule,
		allow: make([]*gtk.CheckButton, len(permDefs)),
		deny:  make([]*gtk.CheckButton, len(permDefs)),
	}

	rw.expander = adw.NewExpanderRow()
	rw.expander.SetTitle(aclRuleTarget(rule))
	if rule.Inherited {
		rw.expander.SetSubtitle(tr("Inherited"))
	}

	// Apply scope switches
	rw.applyCurrent = adw.NewSwitchRow()
	rw.applyCurrent.SetTitle(tr("Apply to this channel"))
	rw.applyCurrent.SetActive(rule.AppliesCurrent)
	rw.expander.AddRow(rw.applyCurrent)

	rw.applyChildren = adw.NewSwitchRow()
	rw.applyChildren.SetTitle(tr("Apply to sub-channels"))
	rw.applyChildren.SetActive(rule.AppliesChildren)
	rw.expander.AddRow(rw.applyChildren)

	// Per-permission allow / deny checkboxes
	for i, pd := range permDefs {
		idx := i
		prow := adw.NewActionRow()
		prow.SetTitle(tr(pd.name))

		allowCb := gtk.NewCheckButton()
		allowCb.SetActive(gumble.Permission(rule.Granted).Has(pd.perm))
		allowCb.SetTooltipText(tr("Allow"))

		denyCb := gtk.NewCheckButton()
		denyCb.SetActive(gumble.Permission(rule.Denied).Has(pd.perm))
		denyCb.SetTooltipText(tr("Deny"))

		// Allow and Deny are mutually exclusive per permission.
		allowCb.ConnectToggled(func() {
			if allowCb.Active() {
				rw.deny[idx].SetActive(false)
			}
		})
		denyCb.ConnectToggled(func() {
			if denyCb.Active() {
				rw.allow[idx].SetActive(false)
			}
		})

		rw.allow[i] = allowCb
		rw.deny[i] = denyCb

		allowLbl := gtk.NewLabel(tr("Allow"))
		allowLbl.AddCSSClass("dim-label")
		denyLbl := gtk.NewLabel(tr("Deny"))
		denyLbl.AddCSSClass("dim-label")

		allowBox := gtk.NewBox(gtk.OrientationHorizontal, 4)
		allowBox.SetVAlign(gtk.AlignCenter)
		allowBox.Append(allowLbl)
		allowBox.Append(allowCb)

		denyBox := gtk.NewBox(gtk.OrientationHorizontal, 4)
		denyBox.SetVAlign(gtk.AlignCenter)
		denyBox.Append(denyLbl)
		denyBox.Append(denyCb)

		prow.AddSuffix(allowBox)
		prow.AddSuffix(denyBox)
		rw.expander.AddRow(prow)
	}

	// Delete button for non-inherited rules.
	if !rule.Inherited {
		deleteBtn := gtk.NewButtonFromIconName("list-remove-symbolic")
		deleteBtn.AddCSSClass("flat")
		deleteBtn.SetTooltipText(tr("Remove rule"))
		deleteBtn.ConnectClicked(func() {
			rw.deleted = true
			rw.expander.SetSensitive(false)
		})
		rw.expander.AddSuffix(deleteBtn)
	}

	return rw
}

// aclRuleTarget returns a display string for the target of an ACL rule.
func aclRuleTarget(r bus.ACLRuleInfo) string {
	if r.GroupName != "" {
		return fmt.Sprintf(tr("Group: %s"), r.GroupName)
	}
	if r.UserName != "" {
		return fmt.Sprintf(tr("User: %s"), r.UserName)
	}
	if r.UserID != 0 {
		return fmt.Sprintf(tr("User #%d"), r.UserID)
	}
	return tr("(unknown)")
}
