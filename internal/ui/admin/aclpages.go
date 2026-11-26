package admin

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"weak"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/ui/weakref"
)

// aclWork is the editor's working copy of the channel's ACL, shared by the
// General (password), Groups and Permissions pages.
type aclWork struct {
	inherits       bool
	groups         []bus.ACLGroupInfo
	ownRules       []bus.ACLRuleInfo // including the password pair (hidden)
	inheritedRules []bus.ACLRuleInfo
	users          map[uint32]string
	dirty          bool
}

func newACLWork(acl *bus.ACLFetched, users map[uint32]string) *aclWork {
	w := &aclWork{inherits: acl.Inherits, users: users}
	for _, g := range acl.Groups {
		g.UsersAdd = slices.Clone(g.UsersAdd)
		g.UsersRemove = slices.Clone(g.UsersRemove)
		w.groups = append(w.groups, g)
	}
	for _, r := range acl.Rules {
		if r.Inherited {
			w.inheritedRules = append(w.inheritedRules, r)
		} else {
			w.ownRules = append(w.ownRules, r)
		}
	}
	return w
}

func hiddenRule(r bus.ACLRuleInfo) bool { return isPasswordRule(r) || isPasswordDeny(r) }

// --- shared widgets --------------------------------------------------------

// userPicker is a button whose popover lists known registered users to
// pick from (with a filter).
func userPicker(icon, tooltip string, users func() map[uint32]string, skip func(uint32) bool, pick func(uint32, string)) *gtk.MenuButton {
	search := gtk.NewSearchEntry()
	list := gtk.NewListBox()
	list.AddCSSClass("boxed-list")
	list.SetSelectionMode(gtk.SelectionNone)
	scroll := gtk.NewScrolledWindow()
	scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroll.SetPropagateNaturalHeight(true)
	scroll.SetMaxContentHeight(280)
	scroll.SetChild(list)
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.SetSizeRequest(260, -1)
	box.Append(search)
	box.Append(scroll)
	pop := gtk.NewPopover()
	pop.SetChild(box)

	btn := gtk.NewMenuButton()
	btn.SetIconName(icon)
	btn.SetTooltipText(tooltip)
	btn.AddCSSClass("flat")
	btn.SetVAlign(gtk.AlignCenter)
	btn.SetPopover(pop)

	// The closures hold the search field and the popover only weakly:
	// they are attached to them or to widgets inside them.
	searchRef, popRef := weakref.New(search), weakref.New(pop)
	fill := func() {
		search, ok := searchRef.Get()
		if !ok {
			return
		}
		for c := list.FirstChild(); c != nil; c = list.FirstChild() {
			list.Remove(c)
		}
		type entry struct {
			id   uint32
			name string
		}
		var all []entry
		q := strings.ToLower(search.Text())
		for id, n := range users() {
			if (skip == nil || !skip(id)) && strings.Contains(strings.ToLower(n), q) {
				all = append(all, entry{id, n})
			}
		}
		slices.SortFunc(all, func(a, b entry) int {
			return cmp.Or(
				strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)),
				strings.Compare(a.name, b.name),
				cmp.Compare(a.id, b.id),
			)
		})
		if len(all) == 0 {
			list.Append(messageRow(i18n.Tr("No matching registered users. Users must be registered on the server; online ones and those from the user list are shown.")))
			return
		}
		for _, u := range all {
			row := adw.NewActionRow()
			row.SetUseMarkup(false) // user and group names come from the server
			row.SetTitle(u.name)
			row.SetActivatable(true)
			row.ConnectActivated(func() {
				popRef.Do(func(pop *gtk.Popover) { pop.Popdown() })
				pick(u.id, u.name)
			})
			list.Append(row)
		}
	}
	search.ConnectSearchChanged(fill)
	pop.ConnectShow(func() {
		search.SetText("")
		fill()
		search.GrabFocus()
	})
	return btn
}

// activeGetter is a widget with an "active" boolean property.
type activeGetter interface {
	coreglib.Objector
	Active() bool
}

// onActive calls f with the new value whenever w's "active" changes.
func onActive(w activeGetter, f func(bool)) {
	self := weakref.New(w)
	coreglib.BaseObject(w).NotifyProperty("active", func() {
		self.Do(func(w activeGetter) { f(w.Active()) })
	})
}

func iconButton(icon, tooltip string, onClick func()) *gtk.Button {
	b := gtk.NewButtonFromIconName(icon)
	b.AddCSSClass("flat")
	b.SetVAlign(gtk.AlignCenter)
	b.SetTooltipText(tooltip)
	b.ConnectClicked(onClick)
	return b
}

func clearBox(b *gtk.Box) {
	for c := b.FirstChild(); c != nil; c = b.FirstChild() {
		b.Remove(c)
	}
}

// --- Groups page -------------------------------------------------------------

// groupsPage is held by the channel editor; the closures of its widgets
// reach it through a weak pointer (see internal/ui/weakref).
type groupsPage struct {
	self     weak.Pointer[groupsPage]
	w        *aclWork
	box      *gtk.Box
	expanded map[string]bool
}

func newGroupsPage(w *aclWork, box *gtk.Box) *groupsPage {
	p := &groupsPage{w: w, box: box, expanded: map[string]bool{}}
	p.self = weak.Make(p)
	p.render()
	return p
}

// on returns a callback that runs fn with the page while it exists.
func (p *groupsPage) on(fn func(p *groupsPage)) func() {
	ws := p.self
	return func() {
		if p := ws.Value(); p != nil {
			fn(p)
		}
	}
}

// onBool is on for callbacks with a boolean.
func (p *groupsPage) onBool(fn func(p *groupsPage, on bool)) func(bool) {
	ws := p.self
	return func(on bool) {
		if p := ws.Value(); p != nil {
			fn(p, on)
		}
	}
}

func (p *groupsPage) changed() { p.w.dirty = true }

func (p *groupsPage) render() {
	clearBox(p.box)
	groups := adw.NewPreferencesGroup()
	groups.SetTitle(i18n.Tr("Groups"))
	groups.SetDescription(i18n.Tr("Groups collect registered users. Rules on the Permissions page can refer to them as @name."))
	if len(p.w.groups) == 0 {
		empty := adw.NewActionRow()
		empty.SetTitle(i18n.Tr("No groups in this channel"))
		empty.AddCSSClass("dim-label")
		groups.Add(empty)
	}
	for i := range p.w.groups {
		groups.Add(p.groupRow(i))
	}
	p.box.Append(groups)

	add := adw.NewEntryRow()
	add.SetTitle(i18n.Tr("New group name"))
	add.SetShowApplyButton(true)
	addRef := weakref.New(add)
	add.ConnectApply(p.on(func(p *groupsPage) {
		add, ok := addRef.Get()
		if !ok {
			return
		}
		name := strings.TrimSpace(add.Text())
		if name == "" || strings.ContainsAny(name, " @#~") || slices.Contains(builtinGroups, name) ||
			slices.ContainsFunc(p.w.groups, func(g bus.ACLGroupInfo) bool { return g.Name == name }) {
			add.AddCSSClass("error")
			return
		}
		p.w.groups = append(p.w.groups, bus.ACLGroupInfo{Name: name, InheritUsers: true, Inheritable: true})
		p.expanded[name] = true
		p.changed()
		p.render()
	}))
	add.ConnectChanged(func() { addRef.Do(func(add *adw.EntryRow) { add.RemoveCSSClass("error") }) })
	newGroup := adw.NewPreferencesGroup()
	newGroup.Add(add)
	p.box.Append(newGroup)
}

func (p *groupsPage) groupRow(i int) *adw.ExpanderRow {
	g := &p.w.groups[i]
	exp := adw.NewExpanderRow()
	exp.SetUseMarkup(false)
	exp.SetTitle(g.Name)
	members := len(g.UsersAdd)
	if g.InheritUsers {
		for _, u := range g.UsersInherited {
			if !hasUser(g.UsersRemove, u.UserID) {
				members++
			}
		}
	}
	sub := fmt.Sprintf(i18n.TrN("%d member", "%d members", members), members)
	if g.Inherited {
		sub = i18n.Tr("Inherited from the parent channel") + " · " + sub
	}
	exp.SetSubtitle(sub)
	exp.SetExpanded(p.expanded[g.Name])
	name := g.Name
	expRef := weakref.New(exp)
	coreglib.BaseObject(exp).NotifyProperty("expanded", p.on(func(p *groupsPage) {
		expRef.Do(func(exp *adw.ExpanderRow) { p.expanded[name] = exp.Expanded() })
	}))

	if !g.Inherited {
		exp.AddSuffix(iconButton("user-trash-symbolic", i18n.Tr("Delete group"), p.on(func(p *groupsPage) {
			p.w.groups = slices.Delete(p.w.groups, i, i+1)
			p.changed()
			p.render()
		})))
	}

	inherit := adw.NewSwitchRow()
	inherit.SetTitle(i18n.Tr("Inherit members"))
	inherit.SetSubtitle(i18n.Tr("Members of the same group in the parent channel belong to it too"))
	inherit.SetActive(g.InheritUsers)
	onActive(inherit, p.onBool(func(p *groupsPage, on bool) {
		p.w.groups[i].InheritUsers = on
		p.changed()
		p.render()
	}))
	exp.AddRow(inherit)

	inheritable := adw.NewSwitchRow()
	inheritable.SetTitle(i18n.Tr("Inheritable"))
	inheritable.SetSubtitle(i18n.Tr("Sub-channels can use this group"))
	inheritable.SetActive(g.Inheritable)
	onActive(inheritable, p.onBool(func(p *groupsPage, on bool) {
		p.w.groups[i].Inheritable = on
		p.changed()
	}))
	exp.AddRow(inheritable)

	for _, u := range g.UsersAdd {
		row := adw.NewActionRow()
		row.SetUseMarkup(false)
		row.SetTitle(p.userName(u))
		row.AddPrefix(gtk.NewImageFromIconName("avatar-default-symbolic"))
		id := u.UserID
		row.AddSuffix(iconButton("list-remove-symbolic", i18n.Tr("Remove from group"), p.on(func(p *groupsPage) {
			p.w.groups[i].UsersAdd = withoutUser(p.w.groups[i].UsersAdd, id)
			p.changed()
			p.render()
		})))
		exp.AddRow(row)
	}
	if g.InheritUsers {
		for _, u := range g.UsersInherited {
			row := adw.NewActionRow()
			row.SetUseMarkup(false)
			row.SetTitle(p.userName(u))
			row.AddPrefix(gtk.NewImageFromIconName("avatar-default-symbolic"))
			id := u.UserID
			excluded := hasUser(g.UsersRemove, id)
			if excluded {
				row.SetSubtitle(i18n.Tr("Inherited, excluded here"))
				row.AddCSSClass("dim-label")
			} else {
				row.SetSubtitle(i18n.Tr("Inherited from the parent channel"))
			}
			sw := gtk.NewSwitch()
			sw.SetActive(!excluded)
			sw.SetVAlign(gtk.AlignCenter)
			sw.SetTooltipText(i18n.Tr("Member in this channel"))
			onActive(sw, p.onBool(func(p *groupsPage, on bool) {
				if on {
					p.w.groups[i].UsersRemove = withoutUser(p.w.groups[i].UsersRemove, id)
				} else if !hasUser(p.w.groups[i].UsersRemove, id) {
					p.w.groups[i].UsersRemove = append(p.w.groups[i].UsersRemove, u)
				}
				p.changed()
				p.render()
			}))
			row.AddSuffix(sw)
			exp.AddRow(row)
		}
	}

	addRow := adw.NewActionRow()
	addRow.SetTitle(i18n.Tr("Add member…"))
	ws := p.self
	addRow.AddSuffix(userPicker("list-add-symbolic", i18n.Tr("Add member"),
		func() map[uint32]string {
			if p := ws.Value(); p != nil {
				return p.w.users
			}
			return nil
		},
		func(id uint32) bool {
			p := ws.Value()
			return p == nil || hasUser(p.w.groups[i].UsersAdd, id)
		},
		func(id uint32, name string) {
			p := ws.Value()
			if p == nil {
				return
			}
			p.w.groups[i].UsersAdd = append(p.w.groups[i].UsersAdd, bus.ACLUserInfo{UserID: id, Name: name})
			p.w.groups[i].UsersRemove = withoutUser(p.w.groups[i].UsersRemove, id)
			p.changed()
			p.render()
		}))
	exp.AddRow(addRow)
	return exp
}

func (p *groupsPage) userName(u bus.ACLUserInfo) string {
	if u.Name != "" {
		return u.Name
	}
	if n := p.w.users[u.UserID]; n != "" {
		return n
	}
	return "#" + itoa(u.UserID)
}

// --- Permissions page --------------------------------------------------------

// permsPage is held by the channel editor; the closures of its widgets
// reach it through a weak pointer (see internal/ui/weakref).
type permsPage struct {
	self     weak.Pointer[permsPage]
	w        *aclWork
	box      *gtk.Box
	root     bool // the root channel: server-wide permissions apply
	expanded map[int]bool
}

func newPermsPage(w *aclWork, box *gtk.Box, root bool) *permsPage {
	p := &permsPage{w: w, box: box, root: root, expanded: map[int]bool{}}
	p.self = weak.Make(p)
	p.render()
	return p
}

// on returns a callback that runs fn with the page while it exists.
func (p *permsPage) on(fn func(p *permsPage)) func() {
	ws := p.self
	return func() {
		if p := ws.Value(); p != nil {
			fn(p)
		}
	}
}

// onBool is on for callbacks with a boolean.
func (p *permsPage) onBool(fn func(p *permsPage, on bool)) func(bool) {
	ws := p.self
	return func(on bool) {
		if p := ws.Value(); p != nil {
			fn(p, on)
		}
	}
}

func (p *permsPage) render() {
	clearBox(p.box)

	inherit := adw.NewSwitchRow()
	inherit.SetTitle(i18n.Tr("Inherit permissions from the parent channel"))
	inherit.SetActive(p.w.inherits)
	onActive(inherit, p.onBool(func(p *permsPage, on bool) {
		p.w.inherits = on
		p.w.dirty = true
		p.render()
	}))
	top := adw.NewPreferencesGroup()
	top.Add(inherit)
	p.box.Append(top)

	if p.w.inherits && len(p.w.inheritedRules) > 0 {
		inh := adw.NewPreferencesGroup()
		inh.SetTitle(i18n.Tr("Inherited Rules"))
		inh.SetDescription(i18n.Tr("Defined in parent channels; they apply before the rules below."))
		for _, r := range p.w.inheritedRules {
			row := adw.NewActionRow()
			row.SetUseMarkup(false)
			row.SetTitle(ruleTarget(r, p.w.users))
			row.SetSubtitle(ruleSummary(r))
			row.SetSubtitleLines(2)
			row.AddCSSClass("dim-label")
			inh.Add(row)
		}
		p.box.Append(inh)
	}

	own := adw.NewPreferencesGroup()
	own.SetTitle(i18n.Tr("Rules of This Channel"))
	own.SetDescription(i18n.Tr("Applied from top to bottom: a later rule overrides an earlier one. The password on the General page adds its own rules, which are not listed here."))
	own.SetHeaderSuffix(p.addRuleButton())
	visible := 0
	for i, r := range p.w.ownRules {
		if hiddenRule(r) {
			continue
		}
		visible++
		own.Add(p.ruleRow(i))
	}
	if visible == 0 {
		empty := adw.NewActionRow()
		empty.SetTitle(i18n.Tr("No rules in this channel"))
		empty.AddCSSClass("dim-label")
		own.Add(empty)
	}
	p.box.Append(own)
}

// neighbour is the index of the next visible rule before (dir -1) or after
// (dir 1) i, or -1.
func (p *permsPage) neighbour(i, dir int) int {
	for j := i + dir; j >= 0 && j < len(p.w.ownRules); j += dir {
		if !hiddenRule(p.w.ownRules[j]) {
			return j
		}
	}
	return -1
}

func (p *permsPage) ruleRow(i int) *adw.ExpanderRow {
	r := &p.w.ownRules[i]
	exp := adw.NewExpanderRow()
	exp.SetUseMarkup(false)
	exp.SetTitle(ruleTarget(*r, p.w.users))
	exp.SetSubtitle(ruleSummary(*r))
	exp.SetSubtitleLines(2)
	exp.SetExpanded(p.expanded[i])
	expRef := weakref.New(exp)
	coreglib.BaseObject(exp).NotifyProperty("expanded", p.on(func(p *permsPage) {
		expRef.Do(func(exp *adw.ExpanderRow) { p.expanded[i] = exp.Expanded() })
	}))

	move := func(p *permsPage, dir int) {
		j := p.neighbour(i, dir)
		if j < 0 {
			return
		}
		p.w.ownRules[i], p.w.ownRules[j] = p.w.ownRules[j], p.w.ownRules[i]
		p.expanded[i], p.expanded[j] = p.expanded[j], p.expanded[i]
		p.w.dirty = true
		p.render()
	}
	up := gtk.NewButtonWithLabel(i18n.Tr("Move Up"))
	up.SetSensitive(p.neighbour(i, -1) >= 0)
	up.ConnectClicked(p.on(func(p *permsPage) { move(p, -1) }))
	down := gtk.NewButtonWithLabel(i18n.Tr("Move Down"))
	down.SetSensitive(p.neighbour(i, 1) >= 0)
	down.ConnectClicked(p.on(func(p *permsPage) { move(p, 1) }))
	order := gtk.NewBox(gtk.OrientationHorizontal, 0)
	order.AddCSSClass("linked")
	order.SetVAlign(gtk.AlignCenter)
	order.Append(up)
	order.Append(down)
	del := gtk.NewButtonWithLabel(i18n.Tr("Delete"))
	del.AddCSSClass("destructive-action")
	del.SetVAlign(gtk.AlignCenter)
	del.ConnectClicked(p.on(func(p *permsPage) {
		p.w.ownRules = slices.Delete(p.w.ownRules, i, i+1)
		p.expanded = map[int]bool{}
		p.w.dirty = true
		p.render()
	}))
	manage := adw.NewActionRow()
	// TRANSLATORS: Position of an ACL rule in the rule list (rules are applied in order).
	manage.SetTitle(i18n.Tr("Order"))
	manage.SetSubtitle(i18n.Tr("Later rules override earlier ones"))
	manage.AddSuffix(order)
	manage.AddSuffix(del)
	exp.AddRow(manage)

	here := adw.NewSwitchRow()
	here.SetTitle(i18n.Tr("Applies to this channel"))
	here.SetActive(r.AppliesCurrent)
	onActive(here, p.onBool(func(p *permsPage, on bool) { p.w.ownRules[i].AppliesCurrent = on; p.w.dirty = true }))
	subs := adw.NewSwitchRow()
	subs.SetTitle(i18n.Tr("Applies to sub-channels"))
	subs.SetActive(r.AppliesChildren)
	onActive(subs, p.onBool(func(p *permsPage, on bool) { p.w.ownRules[i].AppliesChildren = on; p.w.dirty = true }))
	exp.AddRow(here)
	exp.AddRow(subs)

	for _, perm := range aclPerms {
		if perm.root && !p.root {
			continue
		}
		row := adw.NewActionRow()
		row.SetTitle(perm.name())
		bit := perm.bit
		ws := p.self
		row.AddSuffix(stateSwitch(ruleState(*r, bit), func(st permState) {
			p := ws.Value()
			if p == nil {
				return
			}
			setRuleState(&p.w.ownRules[i], bit, st)
			p.w.dirty = true
			expRef.Do(func(exp *adw.ExpanderRow) { exp.SetSubtitle(ruleSummary(p.w.ownRules[i])) })
		}))
		exp.AddRow(row)
	}
	return exp
}

// stateSwitch is a linked Inherit / Allow / Deny toggle group.
func stateSwitch(st permState, set func(permState)) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.AddCSSClass("linked")
	box.SetVAlign(gtk.AlignCenter)
	var first *gtk.ToggleButton
	for _, o := range []struct {
		st    permState
		label string
		class string
	}{
		// TRANSLATORS: Inherit, Allow, Deny: the three states of one permission in an ACL rule.
		{permInherit, i18n.Tr("Inherit"), ""},
		{permAllow, i18n.Tr("Allow"), "success"},
		{permDeny, i18n.Tr("Deny"), "error"},
	} {
		b := gtk.NewToggleButtonWithLabel(o.label)
		b.AddCSSClass("small-toggle")
		if first == nil {
			first = b
		} else {
			b.SetGroup(first)
		}
		b.SetActive(o.st == st)
		if o.class != "" && o.st == st {
			b.AddCSSClass(o.class)
		}
		ost, class := o.st, o.class
		self := weakref.New(b)
		b.ConnectToggled(func() {
			b, ok := self.Get()
			if !ok {
				return
			}
			if class != "" {
				if b.Active() {
					b.AddCSSClass(class)
				} else {
					b.RemoveCSSClass(class)
				}
			}
			if b.Active() {
				set(ost)
			}
		})
		box.Append(b)
	}
	return box
}

// addRuleButton offers a new rule for a group or a registered user.
func (p *permsPage) addRuleButton() *gtk.Box {
	groups := gtk.NewDropDownFromStrings(nil)
	addGroup := gtk.NewButtonWithLabel(i18n.Tr("Add"))
	addGroup.AddCSSClass("suggested-action")
	groupRow := gtk.NewBox(gtk.OrientationHorizontal, 6)
	groupRow.Append(groups)
	groupRow.Append(addGroup)
	glabel := gtk.NewLabel(i18n.Tr("Rule for a group"))
	glabel.SetXAlign(0)
	glabel.AddCSSClass("heading")
	pbox := gtk.NewBox(gtk.OrientationVertical, 8)
	pbox.Append(glabel)
	pbox.Append(groupRow)
	pop := gtk.NewPopover()
	pop.SetChild(pbox)
	groupBtn := gtk.NewMenuButton()
	groupBtn.SetLabel(i18n.Tr("Add Group Rule"))
	groupBtn.AddCSSClass("flat")
	groupBtn.SetPopover(pop)
	var names []string
	ws, popRef := p.self, weakref.New(pop)
	pop.ConnectShow(p.on(func(p *permsPage) {
		names = groupNames(p.w.groups)
		groups.SetModel(gtk.NewStringList(names))
	}))
	addGroup.ConnectClicked(p.on(func(p *permsPage) {
		sel := int(groups.Selected())
		if sel < 0 || sel >= len(names) {
			return
		}
		popRef.Do(func(pop *gtk.Popover) { pop.Popdown() })
		p.addRule(bus.ACLRuleInfo{AppliesCurrent: true, AppliesChildren: true, GroupName: names[sel]})
	}))

	userBtn := userPicker("avatar-default-symbolic", i18n.Tr("Add a rule for a registered user"),
		func() map[uint32]string {
			if p := ws.Value(); p != nil {
				return p.w.users
			}
			return nil
		}, nil,
		func(id uint32, name string) {
			if p := ws.Value(); p != nil {
				p.addRule(bus.ACLRuleInfo{AppliesCurrent: true, AppliesChildren: true, UserID: id, UserName: name})
			}
		})

	box := gtk.NewBox(gtk.OrientationHorizontal, 4)
	box.Append(groupBtn)
	box.Append(userBtn)
	return box
}

func (p *permsPage) addRule(r bus.ACLRuleInfo) {
	// Before the password pair, so the password keeps the last word on
	// entering the channel.
	at := len(p.w.ownRules)
	for i, x := range p.w.ownRules {
		if isPasswordDeny(x) || isPasswordRule(x) {
			at = i
			break
		}
	}
	p.w.ownRules = slices.Insert(p.w.ownRules, at, r)
	p.expanded = map[int]bool{at: true}
	p.w.dirty = true
	p.render()
}
