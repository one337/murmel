package channeltree

import (
	"sort"

	"io.github/murmel/internal/bus"
)

// channelNode is one channel with its ordered child channels and users.
type channelNode struct {
	info     bus.ChannelInfo
	children []uint32
	users    []uint32
	expanded bool
}

// entryKind discriminates flat-walk entries.
type entryKind uint8

const (
	entryChannel entryKind = iota
	entryUser
)

// entry is one row position in the flattened visible tree.
type entry struct {
	kind  entryKind
	id    uint32
	depth int
}

// treeModel is the widget's ordered view over the bus world state. It is
// maintained incrementally from bus events (never rebuilt from a full
// snapshot except on connect/disconnect) and walked to produce the flat row
// order for the wide list. Pure data — no GTK — so the ordering and
// incremental-apply logic is unit-testable without a display.
type treeModel struct {
	channels map[uint32]*channelNode
	users    map[uint32]bus.UserInfo
	self     uint32
	speaking map[uint32]bool
	root     uint32
	hasRoot  bool
}

func newTreeModel() *treeModel {
	return &treeModel{
		channels: map[uint32]*channelNode{},
		users:    map[uint32]bus.UserInfo{},
		speaking: map[uint32]bool{},
	}
}

func (m *treeModel) reset() {
	m.channels = map[uint32]*channelNode{}
	m.users = map[uint32]bus.UserInfo{}
	m.self = 0
	m.speaking = map[uint32]bool{}
	m.root = 0
	m.hasRoot = false
}

// apply folds one bus event into the model, mirroring the bus store's
// semantics but keeping child/user ordering. Returns true when the change
// requires structural row work (insert/remove/move) rather than an in-place
// row update.
func (m *treeModel) apply(ev bus.Event) bool {
	switch e := ev.(type) {
	case bus.Connected:
		m.reset()
		m.self = e.SelfSession
		for _, ch := range e.Channels {
			m.addChannel(ch)
		}
		for _, u := range e.Users {
			m.addUser(u)
		}
		return true
	case bus.Disconnected:
		m.reset()
		return true
	case bus.ChannelAdded:
		return m.addChannel(e.Channel)
	case bus.ChannelRemoved:
		if _, ok := m.channels[e.ID]; !ok {
			return false
		}
		parent := m.parentOf(e.ID)
		m.detachChannel(e.ID)
		m.removeChannelSubtree(e.ID)
		if !isRoot(e.ID) {
			m.sortChildren(parent)
		}
		return true
	case bus.ChannelUpdated:
		node, ok := m.channels[e.Channel.ID]
		if !ok {
			return m.addChannel(e.Channel)
		}
		moved := node.info.ParentID != e.Channel.ParentID
		node.info = e.Channel
		if moved {
			old := m.parentOf(e.Channel.ID)
			m.detachChannel(e.Channel.ID)
			m.attachChannel(e.Channel.ID)
			m.sortChildren(old)
			m.sortChildren(e.Channel.ParentID)
			return true
		}
		if e.Changed.Has(bus.ChannelChangePosition) || e.Changed.Has(bus.ChannelChangeName) {
			if !isRoot(e.Channel.ID) {
				m.sortChildren(e.Channel.ParentID)
				return true
			}
		}
		return false
	case bus.UserAdded:
		return m.addUser(e.User)
	case bus.UserRemoved:
		if _, ok := m.users[e.Session]; !ok {
			return false
		}
		delete(m.users, e.Session)
		delete(m.speaking, e.Session)
		for _, n := range m.channels {
			removeOrdered(&n.users, e.Session)
		}
		return true
	case bus.UserUpdated:
		old, ok := m.users[e.User.Session]
		if !ok {
			return m.addUser(e.User)
		}
		m.users[e.User.Session] = e.User
		if old.Channel != e.User.Channel {
			for _, n := range m.channels {
				removeOrdered(&n.users, e.User.Session)
			}
			if n, ok := m.channels[e.User.Channel]; ok {
				insertOrdered(&n.users, e.User.Session, m.lessUser)
			}
			return true
		}
		return false
	case bus.VoiceActivity:
		if _, ok := m.users[e.Session]; !ok {
			return false
		}
		m.speaking[e.Session] = e.Active
		return false
	}
	return false
}

// isRoot reports whether id is the server's root channel. Mumble's root
// channel always has ID 0; its direct children also carry ParentID 0
// (bus.ChannelInfo has no "no parent" marker), so the parent field alone
// cannot identify the root.
func isRoot(id uint32) bool { return id == 0 }

func (m *treeModel) addChannel(ch bus.ChannelInfo) bool {
	if _, ok := m.channels[ch.ID]; ok {
		return false
	}
	m.channels[ch.ID] = &channelNode{info: ch, expanded: true}
	if isRoot(ch.ID) {
		m.root = ch.ID
		m.hasRoot = true
	}
	// Channels and users that arrived before this channel (parent IDs can
	// be higher than child IDs after moves) hang off it now.
	for id, n := range m.channels {
		if id != ch.ID && !isRoot(id) && n.info.ParentID == ch.ID {
			m.attachChannel(id)
		}
	}
	m.sortChildren(ch.ID)
	for session, u := range m.users {
		if u.Channel == ch.ID {
			insertOrdered(&m.channels[ch.ID].users, session, m.lessUser)
		}
	}
	if isRoot(ch.ID) {
		return true
	}
	if _, ok := m.channels[ch.ParentID]; !ok {
		// Parent not seen yet: attached when it arrives (see above).
		return false
	}
	m.attachChannel(ch.ID)
	m.sortChildren(ch.ParentID)
	return true
}

func (m *treeModel) attachChannel(id uint32) {
	ch := m.channels[id]
	if isRoot(id) {
		return
	}
	if p, ok := m.channels[ch.info.ParentID]; ok && !containsOrdered(p.children, id) {
		p.children = append(p.children, id)
	}
}

func (m *treeModel) detachChannel(id uint32) {
	if isRoot(id) {
		return
	}
	if p, ok := m.channels[m.channels[id].info.ParentID]; ok {
		removeOrdered(&p.children, id)
	}
}

// removeChannelSubtree drops a channel and everything below it.
func (m *treeModel) removeChannelSubtree(id uint32) {
	n, ok := m.channels[id]
	if !ok {
		return
	}
	for _, c := range n.children {
		m.removeChannelSubtree(c)
	}
	delete(m.channels, id)
}

func (m *treeModel) addUser(u bus.UserInfo) bool {
	if _, ok := m.users[u.Session]; ok {
		return false
	}
	m.users[u.Session] = u
	if n, ok := m.channels[u.Channel]; ok {
		insertOrdered(&n.users, u.Session, m.lessUser)
		return true
	}
	return false
}

func (m *treeModel) parentOf(id uint32) uint32 {
	if n, ok := m.channels[id]; ok {
		return n.info.ParentID
	}
	return 0
}

func (m *treeModel) sortChildren(parent uint32) {
	p, ok := m.channels[parent]
	if !ok {
		return
	}
	sort.SliceStable(p.children, func(i, j int) bool {
		return m.lessChannel(p.children[i], p.children[j])
	})
}

func (m *treeModel) lessChannel(a, b uint32) bool {
	ca, cb := m.channels[a].info, m.channels[b].info
	if ca.Position != cb.Position {
		return ca.Position < cb.Position
	}
	if ca.Name != cb.Name {
		return ca.Name < cb.Name
	}
	return ca.ID < cb.ID
}

func (m *treeModel) lessUser(a, b uint32) bool {
	ua, ub := m.users[a], m.users[b]
	if ua.Name != ub.Name {
		return ua.Name < ub.Name
	}
	return ua.Session < ub.Session
}

// hasSelf reports whether the local user is known (connected and synced).
func (m *treeModel) hasSelf() bool {
	_, ok := m.users[m.self]
	return ok
}

// selfChannel returns the local user's channel (0 while unknown).
func (m *treeModel) selfChannel() uint32 {
	if u, ok := m.users[m.self]; ok {
		return u.Channel
	}
	return 0
}

// walk flattens the visible (expanded) tree depth-first: each channel, then
// (if expanded) its users and its sub-channels.
func (m *treeModel) walk() []entry {
	var out []entry
	var rec func(id uint32, depth int)
	rec = func(id uint32, depth int) {
		n, ok := m.channels[id]
		if !ok {
			return
		}
		out = append(out, entry{kind: entryChannel, id: id, depth: depth})
		if !n.expanded {
			return // a collapsed channel hides its users and sub-channels
		}
		for _, s := range n.users {
			out = append(out, entry{kind: entryUser, id: s, depth: depth + 1})
		}
		for _, c := range n.children {
			rec(c, depth+1)
		}
	}
	if m.hasRoot {
		rec(m.root, 0)
	}
	return out
}

func insertOrdered(list *[]uint32, id uint32, less func(a, b uint32) bool) {
	i := sort.Search(len(*list), func(i int) bool { return less(id, (*list)[i]) })
	*list = append(*list, 0)
	copy((*list)[i+1:], (*list)[i:])
	(*list)[i] = id
}

func removeOrdered(list *[]uint32, id uint32) {
	for i, v := range *list {
		if v == id {
			*list = append((*list)[:i:i], (*list)[i+1:]...)
			return
		}
	}
}

func containsOrdered(list []uint32, id uint32) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}
