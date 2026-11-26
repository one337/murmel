package channeltree

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
)

// natives maps every row to its GObject.
func natives(tree *Tree) map[rowID]uintptr {
	out := map[rowID]uintptr{}
	onMain(func() {
		for id, r := range tree.channelRows {
			out[rowID{id: id}] = coreglib.BaseObject(r.root).Native()
		}
		for s, r := range tree.userRows {
			out[rowID{user: true, id: s}] = coreglib.BaseObject(r.root).Native()
		}
	})
	return out
}

// settle waits until no rebuild is pending or queued.
func settle(t *testing.T, tree *Tree) {
	t.Helper()
	waitFor(t, "the rebuild", func() bool { return !tree.rebuildPending && !tree.rebuildScheduled })
}

// checkRows fails unless every row has the indent of its depth (a user
// row that of its channel), channel rows show their channel's name and
// number of users, and only one's own channel is marked as such.
func checkRows(t *testing.T, tree *Tree) {
	t.Helper()
	onMain(func() {
		self := tree.model.selfChannel()
		for _, e := range tree.model.walk() {
			var box *gtk.Box
			depth := e.depth
			if e.kind == entryChannel {
				r := tree.channelRows[e.id]
				box = r.box
				current := e.id == self && tree.model.hasSelf()
				if r.root.HasCSSClass("current-channel") != current || r.here.Visible() != current {
					t.Errorf("channel %d marked as one's own %v, want %v", e.id, r.root.HasCSSClass("current-channel"), current)
				}
				n := tree.model.channels[e.id]
				if got := r.name.Text(); got != n.info.Name {
					t.Errorf("channel %d named %q, want %q", e.id, got, n.info.Name)
				}
				count := ""
				if len(n.users) > 0 {
					count = fmt.Sprint(len(n.users))
				}
				if got := r.count.Text(); r.count.Visible() != (count != "") || (count != "" && got != count) {
					t.Errorf("channel %d shows %q users (shown %v), want %q", e.id, got, r.count.Visible(), count)
				}
				if want := channelDescription(len(n.users), current, r.text.shownUnread); r.text.desc != want {
					t.Errorf("channel %d described as %q, want %q", e.id, r.text.desc, want)
				}
			} else {
				r := tree.userRows[e.id]
				box = r.box
				depth--
				// A kept row shows the user as the model has them, also
				// after changes that came with a move.
				u := tree.model.users[e.id]
				muted := u.Muted || u.SelfMuted || u.Suppressed
				if r.name.Text() != u.Name || r.text.name != u.Name || r.icons[iconMuted].Visible() != muted {
					t.Errorf("user %d shown as %q (accessible %q, muted icon %v), want %q (muted %v)",
						e.id, r.name.Text(), r.text.name, r.icons[iconMuted].Visible(), u.Name, muted)
				}
				if want := userDescription(u, r.local, r.text.shownUnread); r.text.desc != want {
					t.Errorf("user %d described as %q, want %q", e.id, r.text.desc, want)
				}
			}
			if got := box.MarginStart(); got != indentPerDepthPx*depth {
				t.Errorf("%+v indented by %d, want %d", e, got, indentPerDepthPx*depth)
			}
		}
	})
}

// Users leaving, joining and moving and a new sub-channel leave the rows
// that stay as they are: the same widgets, moved ones included.
func TestGTKRowsThatStayAreKept(t *testing.T) {
	needGTK(t)
	const channels, users = 30, 100
	tree, b, _ := batchTree(t, channels, users)
	settle(t, tree)
	before := natives(tree)

	var evs []bus.Event
	for s := 1; s <= 10; s++ {
		evs = append(evs, bus.UserRemoved{Session: uint32(s * 7)})
	}
	evs = append(evs, bus.ChannelAdded{Channel: bus.ChannelInfo{ID: 100, ParentID: 3, Name: "Sub"}})
	onMain(func() {
		for _, s := range []uint32{2, 3, 4, 5} {
			u := tree.model.users[s]
			u.Channel = u.Channel%channels + 1
			evs = append(evs, bus.UserUpdated{User: u})
		}
		u := tree.model.users[6]
		u.Channel = 100 // one level deeper
		evs = append(evs, bus.UserUpdated{User: u})
	})
	for s := 1001; s <= 1005; s++ {
		evs = append(evs, bus.UserAdded{User: bus.UserInfo{Session: uint32(s), Name: fmt.Sprintf("new-%d", s), Channel: uint32(s % channels)}})
	}
	inOnePass(b, evs, func() {})
	settle(t, tree)

	after := natives(tree)
	for id, n := range after {
		if old, ok := before[id]; ok && old != n {
			t.Errorf("row %+v was replaced", id)
		}
	}
	if len(after) != len(before)-10+1+5 {
		t.Errorf("%d rows, want %d", len(after), len(before)-10+1+5)
	}
	checkListMatchesModel(t, tree)
	checkRows(t, tree)
}

// A user coming or going inserts or removes just that row.
func TestGTKOneJoinInsertsOneRow(t *testing.T) {
	needGTK(t)
	tree, b, _ := batchTree(t, 30, 100)
	settle(t, tree)
	stop := countInserted(tree)
	b.Emit(bus.UserAdded{User: bus.UserInfo{Session: 500, Name: "guest", Channel: 4}})
	waitFor(t, "the new row", func() bool { _, ok := tree.userRows[500]; return ok })
	settle(t, tree)
	if n := stop(); n != 1 {
		t.Errorf("a user joining inserted %d rows, want 1", n)
	}
	stop = countInserted(tree)
	b.Emit(bus.UserRemoved{Session: 500})
	waitFor(t, "the row to go", func() bool { _, ok := tree.userRows[500]; return !ok })
	settle(t, tree)
	if n := stop(); n != 0 {
		t.Errorf("a user leaving inserted %d rows, want 0", n)
	}
	checkListMatchesModel(t, tree)
}

// within reports whether channel id is top or lies below it, with parent
// giving each channel's parent.
func within(parent func(uint32) uint32, id, top uint32) bool {
	for {
		if id == top {
			return true
		}
		if isRoot(id) {
			return false
		}
		id = parent(id)
	}
}

// Many random changes (channels moved elsewhere among them), each pass
// checked: the list shows the model's walk order, with the right indents
// and one's own channel marked.
func TestGTKRandomChangesKeepTheList(t *testing.T) {
	needGTK(t)
	const channels, users = 12, 40
	tree, b, _ := batchTree(t, channels, users)
	settle(t, tree)
	rng := rand.New(rand.NewPCG(1, 2))
	nextSession, nextChannel := uint32(1000), uint32(1000)

	for pass := range 40 {
		var evs []bus.Event
		onMain(func() {
			var sessions, chans []uint32
			for s := range tree.model.users {
				sessions = append(sessions, s)
			}
			for id := range tree.model.channels {
				chans = append(chans, id)
			}
			pick := func(l []uint32) uint32 { return l[rng.IntN(len(l))] }
			// The moves of this pass count already: two moves checked only
			// against the model could put channels below each other.
			moved := map[uint32]uint32{}
			parent := func(id uint32) uint32 {
				if p, ok := moved[id]; ok {
					return p
				}
				return tree.model.parentOf(id)
			}
			for range 1 + rng.IntN(6) {
				switch rng.IntN(8) {
				case 0:
					nextSession++
					evs = append(evs, bus.UserAdded{User: bus.UserInfo{Session: nextSession, Name: fmt.Sprintf("u%d", nextSession), Channel: pick(chans)}})
				case 1:
					if s := pick(sessions); s != tree.model.self {
						evs = append(evs, bus.UserRemoved{Session: s})
					}
				case 2, 3:
					// A move can bring other changes with it (suppressed
					// in the new channel, a new name).
					u := tree.model.users[pick(sessions)]
					u.Channel = pick(chans)
					changed := bus.UserChangeChannel
					if rng.IntN(2) == 0 {
						u.Suppressed = !u.Suppressed
						changed |= bus.UserChangeAudio
					}
					if rng.IntN(4) == 0 {
						u.Name = fmt.Sprintf("n%d", rng.IntN(100))
						changed |= bus.UserChangeName
					}
					evs = append(evs, bus.UserUpdated{User: u, Changed: changed})
				case 4:
					nextChannel++
					evs = append(evs, bus.ChannelAdded{Channel: bus.ChannelInfo{ID: nextChannel, ParentID: pick(chans), Name: fmt.Sprintf("c%d", rng.IntN(100))}})
				case 5:
					if id := pick(chans); !isRoot(id) && len(tree.model.channels[id].users) == 0 && len(tree.model.channels[id].children) == 0 {
						evs = append(evs, bus.ChannelRemoved{ID: id})
					}
				case 6:
					n := tree.model.channels[pick(chans)]
					info := n.info
					info.Name = fmt.Sprintf("r%d", rng.IntN(100))
					evs = append(evs, bus.ChannelUpdated{Channel: info})
				case 7:
					id, to := pick(chans), pick(chans)
					if !isRoot(id) && !within(parent, to, id) {
						info := tree.model.channels[id].info
						info.ParentID = to
						evs = append(evs, bus.ChannelUpdated{Channel: info})
						moved[id] = to
					}
				}
			}
		})
		inOnePass(b, evs, func() {})
		if pass%5 == 4 {
			onMain(func() {
				for id, n := range tree.model.channels {
					if !isRoot(id) && len(n.users)+len(n.children) > 0 {
						tree.setExpanded(id, !n.expanded)
						break
					}
				}
			})
		}
		settle(t, tree)
		checkListMatchesModel(t, tree)
		checkRows(t, tree)
		if t.Failed() {
			t.Fatalf("after pass %d", pass)
		}
	}
}

// Users coming, going and moving elsewhere do not touch the focused row:
// it keeps the focus all along (assistive technologies are not told of
// a new focus).
func TestGTKForeignChangesLeaveTheFocusAlone(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 5, 40)
	id := rowID{user: true, id: usersOf(tree, 2)[1]}
	focusRow(t, tree, id)
	before := natives(tree)[id]
	leaves := 0
	onMain(func() {
		c := gtk.NewEventControllerFocus()
		c.ConnectLeave(func() { leaves++ })
		tree.rowFor(id).AddController(c)
	})

	other := usersOf(tree, 4)
	b.Emit(bus.UserAdded{User: bus.UserInfo{Session: 900, Name: "guest", Channel: 3}})
	time.Sleep(2 * rebuildInterval)
	b.Emit(bus.UserRemoved{Session: other[0]})
	time.Sleep(2 * rebuildInterval)
	var u bus.UserInfo
	onMain(func() { u = tree.model.users[other[1]] })
	u.Channel = 5
	b.Emit(bus.UserUpdated{User: u})
	settle(t, tree)
	time.Sleep(2 * rebuildInterval)

	if got, ok := focused(tree); !ok || got != id {
		t.Errorf("focus on %+v (%v), want %+v", got, ok, id)
	}
	if natives(tree)[id] != before {
		t.Error("the focused row was replaced")
	}
	onMain(func() {
		if leaves != 0 {
			t.Errorf("the focused row lost the focus %d times", leaves)
		}
	})
}

// One's own channel changing moves the mark to the new channel's row; the
// rows stay.
func TestGTKOwnChannelMarkFollows(t *testing.T) {
	needGTK(t)
	tree, b, _ := batchTree(t, 10, 30)
	settle(t, tree)
	before := natives(tree)
	var self bus.UserInfo
	onMain(func() { self = tree.model.users[tree.model.self] })
	self.Channel = self.Channel%10 + 1
	b.Emit(bus.UserUpdated{User: self})
	waitFor(t, "the move", func() bool { return tree.model.selfChannel() == self.Channel })
	settle(t, tree)
	checkRows(t, tree)
	for id, n := range natives(tree) {
		if before[id] != n {
			t.Errorf("row %+v was replaced", id)
		}
	}
}
