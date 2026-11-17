package channeltree

import (
	"math"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"

	"io.github/murmel/internal/bus"
)

// The local user is session 1 (connectedEvent), in channel 2.
var self = rowID{user: true, id: 1}

// ownRow reports where one's own row is in the view.
func ownRow(t *testing.T, tree *Tree) (top, bottom, page float64, ok bool) {
	t.Helper()
	onMain(func() {
		var h float64
		top, h, ok = viewTop(tree, self)
		bottom = top + h
		page = tree.root.VAdjustment().PageSize()
	})
	return top, bottom, page, ok
}

// fullyShown checks that one's own row is fully in view.
func fullyShown(t *testing.T, tree *Tree, what string) (top, bottom, page float64) {
	t.Helper()
	top, bottom, page, ok := ownRow(t, tree)
	if !ok || top < -tolerance || bottom > page+tolerance {
		t.Errorf("%s: own row at %.0f..%.0f (there %v), not fully in the view 0..%.0f", what, top, bottom, ok, page)
	}
	return top, bottom, page
}

func scrollTo(t *testing.T, tree *Tree, end bool) {
	t.Helper()
	onMain(func() {
		adj := tree.root.VAdjustment()
		v := 0.0
		if end {
			v = adj.Upper() - adj.PageSize()
		}
		adj.SetValue(v)
	})
	time.Sleep(3 * rebuildInterval)
}

// Moved into a channel far below the view: the view follows just far
// enough to show one's own row fully, at its bottom edge.
func TestGTKOwnRowShownAfterMoveFarBelow(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 30, 300)
	sidebarMargin(tree)
	scrollTo(t, tree, false)
	moveUser(t, tree, b, self.id, channelFarBelow(t, tree, listOrder(tree)))
	_, bottom, page := fullyShown(t, tree, "moved far below")
	if math.Abs(bottom-page) > tolerance {
		t.Errorf("the view scrolled more than needed: own row ends at %.0f, the view at %.0f", bottom, page)
	}
}

// Moved into a channel far above the view: own row at the top edge.
func TestGTKOwnRowShownAfterMoveFarAbove(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 30, 300)
	scrollTo(t, tree, true)
	moveUser(t, tree, b, self.id, 1)
	top, _, _ := fullyShown(t, tree, "moved far above")
	if math.Abs(top) > tolerance {
		t.Errorf("the view scrolled more than needed: own row starts at %.0f", top)
	}
}

// Moved within the view: the view stays as it is.
func TestGTKOwnRowMovedInViewViewStays(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 30, 60) // two users a channel: several channels in view
	scrollRowToTop(t, tree, rowID{id: 2})
	top := rowID{id: 2}
	var target uint32
	onMain(func() {
		page := tree.root.VAdjustment().PageSize()
		for _, id := range listOrder2(tree) {
			if t, _, ok := viewTop(tree, id); ok && !id.user && id.id != 2 && t > 40 && t < page-80 {
				target = id.id
				return
			}
		}
	})
	if target == 0 {
		t.Fatal("no channel in the middle of the view")
	}
	moveUser(t, tree, b, self.id, target)
	keepsPlace(t, tree, top, 0, "the top row")
	fullyShown(t, tree, "moved within the view")
}

// Moved into a closed channel: it opens and one's own row is shown.
func TestGTKOwnRowOpensClosedChannel(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 30, 300)
	target := channelFarBelow(t, tree, listOrder(tree))
	onMain(func() { tree.toggleExpand(target) })
	moveUser(t, tree, b, self.id, target)
	onMain(func() {
		if !tree.model.channels[target].expanded {
			t.Error("the closed target channel stayed closed")
		}
	})
	fullyShown(t, tree, "moved into a closed channel")
	checkListMatchesModel(t, tree)
}

// Moved into a channel inside a closed one: both open.
func TestGTKOwnRowOpensClosedParents(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 30, 300)
	b.Emit(bus.ChannelAdded{Channel: bus.ChannelInfo{ID: 31, ParentID: 25, Name: "Channel 25a"}})
	waitFor(t, "the sub-channel", func() bool { _, ok := tree.channelRows[31]; return ok && !tree.rebuildPending })
	onMain(func() {
		tree.toggleExpand(31)
		tree.toggleExpand(25)
	})
	moveUser(t, tree, b, self.id, 31)
	onMain(func() {
		if !tree.model.channels[25].expanded || !tree.model.channels[31].expanded {
			t.Errorf("open: parent %v, target %v; want both", tree.model.channels[25].expanded, tree.model.channels[31].expanded)
		}
	})
	fullyShown(t, tree, "moved into a channel inside a closed one")
	checkListMatchesModel(t, tree)
}

// While a menu is open the list waits; once it closes, one's own row is
// shown.
func TestGTKOwnRowShownAfterMenuCloses(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 30, 300)
	scrollTo(t, tree, false)
	pop := openUserMenu(t, tree, firstUser(tree))
	time.Sleep(2 * rebuildInterval)
	var before float64
	onMain(func() { before = tree.root.VAdjustment().Value() })
	target := channelFarBelow(t, tree, listOrder(tree))

	var u bus.UserInfo
	onMain(func() { u = tree.model.users[self.id] })
	u.Channel = target
	b.Emit(u2updated(u))
	time.Sleep(4 * rebuildInterval)
	var after float64
	var mapped bool
	onMain(func() { after, mapped = tree.root.VAdjustment().Value(), pop.Mapped() })
	if !mapped || after != before {
		t.Errorf("while the menu was open: menu on screen %v, view moved from %.0f to %.0f", mapped, before, after)
	}
	onMain(func() { pop.Popdown() })
	time.Sleep(3 * rebuildInterval)
	fullyShown(t, tree, "after the menu closed")
}

// While a drag handle is pressed the list waits; on release, one's own row
// is shown.
func TestGTKOwnRowShownAfterHandleRelease(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 30, 300)
	scrollTo(t, tree, false)
	src := dragSource(t, tree, firstUser(tree))
	onMain(func() { coreglib.InternObject(src).Emit("begin", nil) })
	var before float64
	onMain(func() { before = tree.root.VAdjustment().Value() })

	var u bus.UserInfo
	onMain(func() { u = tree.model.users[self.id] })
	u.Channel = channelFarBelow(t, tree, listOrder(tree))
	b.Emit(u2updated(u))
	time.Sleep(4 * rebuildInterval)
	var after float64
	onMain(func() { after = tree.root.VAdjustment().Value() })
	if after != before {
		t.Errorf("while the handle was pressed the view moved from %.0f to %.0f", before, after)
	}
	onMain(func() { coreglib.InternObject(src).Emit("end", nil) })
	time.Sleep(3 * rebuildInterval)
	fullyShown(t, tree, "after the handle was released")
}

// Another user moving into a closed channel opens nothing and moves
// nothing.
func TestGTKOtherUserMovedNoReveal(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 30, 300)
	scrollTo(t, tree, false)
	order := listOrder(tree)
	target := channelFarBelow(t, tree, order)
	onMain(func() { tree.toggleExpand(target) })
	var top float64
	onMain(func() { top, _, _ = viewTop(tree, order[0]) })
	moveUser(t, tree, b, usersOf(tree, 2)[1], target)
	onMain(func() {
		if tree.model.channels[target].expanded {
			t.Error("another user's move opened the closed channel")
		}
	})
	keepsPlace(t, tree, order[0], top, "the top row")
}

func u2updated(u bus.UserInfo) bus.Event { return bus.UserUpdated{User: u} }

// firstUser is the first user row of the list (in view at the top).
func firstUser(tree *Tree) uint32 {
	for _, id := range listOrder(tree) {
		if id.user && id != self {
			return id.id
		}
	}
	return 0
}

// listOrder2 is listOrder for the main thread.
func listOrder2(tree *Tree) []rowID {
	var out []rowID
	for _, e := range tree.model.walk() {
		out = append(out, rowID{user: e.kind != entryChannel, id: e.id})
	}
	return out
}
