package channeltree

import (
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"

	"github.com/one337/murmel/internal/bus"
)

// tolerance is how far (px) a row may move on screen and still count as
// having kept its place.
const tolerance = 2

// listOrder is the rows of the list in order.
func listOrder(tree *Tree) []rowID {
	var out []rowID
	onMain(func() {
		for _, e := range tree.model.walk() {
			out = append(out, rowID{user: e.kind != entryChannel, id: e.id})
		}
	})
	return out
}

// viewTop is how far below the top of the view id's row starts (call on
// the main thread).
func viewTop(tree *Tree, id rowID) (top, height float64, ok bool) {
	row := tree.rowFor(id)
	if row == nil {
		return 0, 0, false
	}
	b, ok := row.ComputeBounds(tree.root.Child())
	if !ok {
		return 0, 0, false
	}
	return float64(b.Y()), float64(b.Height()), true
}

// sidebarMargin gives the list the margin the sidebar style gives it in
// the app (the tests run without that style sheet).
func sidebarMargin(tree *Tree) {
	onMain(func() {
		tree.list.SetMarginTop(6)
		tree.list.SetMarginBottom(6)
	})
	time.Sleep(2 * rebuildInterval)
}

// rowAtView is the row y px below the top of the view (call on the main
// thread).
func rowAtView(tree *Tree, y float64) (rowID, bool) {
	for _, e := range tree.model.walk() {
		id := rowID{user: e.kind != entryChannel, id: e.id}
		if top, h, ok := viewTop(tree, id); ok && top <= y && y < top+h {
			return id, true
		}
	}
	return rowID{}, false
}

// scrollRowToTop scrolls so that id's row starts at the top of the view.
func scrollRowToTop(t *testing.T, tree *Tree, id rowID) {
	t.Helper()
	onMain(func() {
		top, _, _ := viewTop(tree, id)
		adj := tree.root.VAdjustment()
		adj.SetValue(adj.Value() + top)
	})
	time.Sleep(3 * rebuildInterval)
	var top float64
	onMain(func() { top, _, _ = viewTop(tree, id) })
	if math.Abs(top) > tolerance {
		t.Fatalf("could not scroll %+v to the top of the view (at %.0f)", id, top)
	}
}

// usersBetween returns up to n users, spread out, from order[from:to].
func usersBetween(order []rowID, from, to, n int) []uint32 {
	var all []uint32
	for _, id := range order[from:to] {
		if id.user {
			all = append(all, id.id)
		}
	}
	if len(all) <= n {
		return all
	}
	var out []uint32
	for i := range n {
		out = append(out, all[i*len(all)/n])
	}
	return out
}

// moveUser moves session into channel and waits for the rebuild.
func moveUser(t *testing.T, tree *Tree, b *bus.Bus, session, channel uint32) {
	t.Helper()
	var u bus.UserInfo
	onMain(func() { u = tree.model.users[session] })
	u.Channel = channel
	b.Emit(bus.UserUpdated{User: u})
	waitFor(t, "the move", func() bool {
		return tree.model.users[session].Channel == channel && !tree.rebuildPending && !tree.rebuildScheduled
	})
	time.Sleep(3 * rebuildInterval)
}

// keepsPlace checks that id's row is where it was on screen.
func keepsPlace(t *testing.T, tree *Tree, id rowID, before float64, what string) {
	t.Helper()
	var after float64
	var ok bool
	onMain(func() { after, _, ok = viewTop(tree, id) })
	if !ok || math.Abs(after-before) > tolerance {
		t.Errorf("%s: %+v moved on screen from %.0f to %.0f (row there %v)", what, id, before, after, ok)
	}
}

// Users above the view leave (mouse use, the focus not in the list): the
// top row of the view and the row under a fixed point stay where they are.
func TestGTKUsersLeavingAboveKeepTheView(t *testing.T) {
	needGTK(t)
	tree, b, _, entry := focusTree(t, 30, 300)
	onMain(func() { entry.GrabFocus() })
	order := listOrder(tree)
	at := len(order) * 6 / 10
	scrollRowToTop(t, tree, order[at])

	var under rowID
	onMain(func() { under, _ = rowAtView(tree, 300) })
	removeAndWait(t, tree, b, usersBetween(order, 0, at, 20)...)
	keepsPlace(t, tree, order[at], 0, "the top row")
	var now rowID
	onMain(func() { now, _ = rowAtView(tree, 300) })
	if now != under {
		t.Errorf("the row 300 px down is %+v, was %+v", now, under)
	}
}

// Users join above the view: the top row stays where it is.
func TestGTKUsersJoiningAboveKeepTheView(t *testing.T) {
	needGTK(t)
	tree, b, _, entry := focusTree(t, 30, 300)
	onMain(func() { entry.GrabFocus() })
	order := listOrder(tree)
	at := len(order) * 6 / 10
	scrollRowToTop(t, tree, order[at])

	var channels []uint32
	for _, id := range order[:at] {
		if !id.user && id.id != 0 {
			channels = append(channels, id.id)
		}
	}
	var evs []bus.Event
	for i := range 20 {
		evs = append(evs, bus.UserAdded{User: bus.UserInfo{
			Session: uint32(1000 + i), Name: "newcomer", Channel: channels[i*len(channels)/20],
		}})
	}
	inOnePass(b, evs, func() {})
	waitFor(t, "the rebuild", func() bool {
		return len(tree.userRows) == 320 && !tree.rebuildPending && !tree.rebuildScheduled
	})
	time.Sleep(3 * rebuildInterval)
	keepsPlace(t, tree, order[at], 0, "the top row")
}

// A focused row in the middle of the view holds its place, not the top
// row: users between them leave. Both with the focus ring shown and hidden.
func TestGTKFocusedRowInViewKeepsItsPlace(t *testing.T) {
	needGTK(t)
	for _, visible := range []bool{true, false} {
		tree, b, win, _ := focusTree(t, 30, 300)
		order := listOrder(tree)
		at := len(order) * 6 / 10
		scrollRowToTop(t, tree, order[at])
		var focus rowID
		var top float64
		onMain(func() {
			focus, _ = rowAtView(tree, 300)
			top, _, _ = viewTop(tree, focus)
		})
		focusRow(t, tree, focus)
		onMain(func() { win.SetFocusVisible(visible) })
		time.Sleep(3 * rebuildInterval)
		keepsPlace(t, tree, focus, top, "focusing")
		f := slices.Index(order, focus)
		gone := append(usersBetween(order, 0, at, 10), usersBetween(order, at, f, 5)...)
		removeAndWait(t, tree, b, gone...)
		keepsPlace(t, tree, focus, top, "the focused row")
		if got, ok := focused(tree); !ok || got != focus {
			t.Errorf("focus on %+v (%v), want %+v", got, ok, focus)
		}
	}
}

// The focused row is out of view (scrolled away): the top row of the view
// holds its place and the focus stays where it was.
func TestGTKFocusedRowOutOfViewTopRowKeepsItsPlace(t *testing.T) {
	needGTK(t)
	tree, b, _, _ := focusTree(t, 30, 300)
	order := listOrder(tree)
	focus := order[len(order)/10]
	focusRow(t, tree, focus)
	at := len(order) * 6 / 10
	scrollRowToTop(t, tree, order[at])

	removeAndWait(t, tree, b, usersBetween(order, len(order)/10+1, at, 20)...)
	keepsPlace(t, tree, order[at], 0, "the top row")
	if got, ok := focused(tree); !ok || got != focus {
		t.Errorf("focus on %+v (%v), want %+v", got, ok, focus)
	}
}

// userFollowedByUser returns an index near frac of order whose row and
// next row are users.
func userFollowedByUser(t *testing.T, order []rowID, frac float64) int {
	t.Helper()
	for i := int(float64(len(order)) * frac); i+1 < len(order); i++ {
		if order[i].user && order[i+1].user {
			return i
		}
	}
	t.Fatal("no two users in a row")
	return 0
}

// The top row of the view leaves, with users above it: the row below it
// holds its place.
func TestGTKAnchorLeavesNextRowBelowKeepsItsPlace(t *testing.T) {
	needGTK(t)
	tree, b, _, entry := focusTree(t, 30, 300)
	onMain(func() { entry.GrabFocus() })
	order := listOrder(tree)
	at := userFollowedByUser(t, order, 0.6)
	scrollRowToTop(t, tree, order[at])
	var next float64
	onMain(func() { next, _, _ = viewTop(tree, order[at+1]) })

	removeAndWait(t, tree, b, append(usersBetween(order, 0, at, 10), order[at].id)...)
	keepsPlace(t, tree, order[at+1], next, "the row below the one that left")
}

// The anchor falls back to the rows below it, then to the rows above it,
// nearest first.
func TestAnchorCandidatesBelowThenAbove(t *testing.T) {
	order := []rowID{{id: 0}, {id: 1}, {user: true, id: 5}, {user: true, id: 6}, {id: 2}, {user: true, id: 7}}
	tops := []float64{0, 30, 60, 90, 120, 150}
	got := anchorCandidates(order, 2, tops, 50)
	want := []anchorCandidate{
		{row: rowID{user: true, id: 5}, channel: 1, offset: 10},
		{row: rowID{user: true, id: 6}, channel: 1, offset: 40},
		{row: rowID{id: 2}, channel: 2, offset: 70},
		{row: rowID{user: true, id: 7}, channel: 2, offset: 100},
		{row: rowID{id: 1}, channel: 1, offset: -20},
		{row: rowID{id: 0}, channel: 0, offset: -50},
	}
	if !slices.Equal(got, want) {
		t.Errorf("candidates\n got %+v\nwant %+v", got, want)
	}
}

// channelFarBelow returns a channel whose row is well below the view.
func channelFarBelow(t *testing.T, tree *Tree, order []rowID) uint32 {
	t.Helper()
	var ch uint32
	onMain(func() {
		page := tree.root.VAdjustment().PageSize()
		for _, id := range order {
			if top, _, ok := viewTop(tree, id); ok && !id.user && top > 3*page {
				ch = id.id
				return
			}
		}
	})
	if ch == 0 {
		t.Fatal("no channel far below the view")
	}
	return ch
}

// The top row of the view is a user who is moved into a channel far below:
// the view does not follow, the row below holds its place.
func TestGTKAnchorUserMovedAwayViewStays(t *testing.T) {
	needGTK(t)
	tree, b, _, entry := focusTree(t, 30, 300)
	onMain(func() { entry.GrabFocus() })
	order := listOrder(tree)
	at := userFollowedByUser(t, order, 0.3)
	scrollRowToTop(t, tree, order[at])
	var next float64
	onMain(func() { next, _, _ = viewTop(tree, order[at+1]) })

	moveUser(t, tree, b, order[at].id, channelFarBelow(t, tree, order))
	keepsPlace(t, tree, order[at+1], next, "the row below the user who was moved")
}

// focusedUserInView focuses a user about 300 px down the view, with users
// above it in the view, and returns it with its place on screen.
func focusedUserInView(t *testing.T, tree *Tree, order []rowID) (rowID, int, float64) {
	t.Helper()
	at := len(order) * 3 / 10
	scrollRowToTop(t, tree, order[at])
	var focus rowID
	var top float64
	onMain(func() {
		for y := 300.0; y < 500; y += 10 {
			if id, ok := rowAtView(tree, y); ok && id.user {
				focus = id
				top, _, _ = viewTop(tree, id)
				return
			}
		}
	})
	if !focus.user {
		t.Fatal("no user row 300 px down the view")
	}
	focusRow(t, tree, focus)
	return focus, at, top
}

// With the focus ring shown (keyboard use), the focused user is moved into
// a channel far below: the view follows just far enough to show the row
// fully, and Enter acts on it.
func TestGTKFocusedUserMovedKeyboardViewFollows(t *testing.T) {
	needGTK(t)
	tree, b, win, _ := focusTree(t, 30, 300)
	sidebarMargin(tree)
	order := listOrder(tree)
	focus, _, _ := focusedUserInView(t, tree, order)
	onMain(func() { win.SetFocusVisible(true) })
	targets := record[bus.ChatTargetChanged](t, b)

	moveUser(t, tree, b, focus.id, channelFarBelow(t, tree, order))
	if got, ok := focused(tree); !ok || got != focus {
		t.Fatalf("focus on %+v (%v), want the moved user %+v", got, ok, focus)
	}
	var top, h, page float64
	onMain(func() {
		top, h, _ = viewTop(tree, focus)
		page = tree.root.VAdjustment().PageSize()
	})
	if top < -tolerance || top+h > page+tolerance {
		t.Errorf("the moved focused row is at %.0f..%.0f, not fully in the view 0..%.0f", top, top+h, page)
	} else if math.Abs(top+h-page) > tolerance {
		t.Errorf("the view scrolled more than needed: the row ends at %.0f, the view at %.0f", top+h, page)
	}

	onMain(func() { coreglib.BaseObject(tree.list).Emit("activate-cursor-row") })
	got := targets(1)
	if len(got) == 0 || got[len(got)-1].Session != focus.id {
		t.Errorf("Enter after the move went to %+v, want user %d", got, focus.id)
	}
}

// With the focus ring hidden (mouse use), the same move leaves the view as
// it is: the top row and the row under a point above the moved user stay.
func TestGTKFocusedUserMovedMouseViewStays(t *testing.T) {
	needGTK(t)
	tree, b, win, _ := focusTree(t, 30, 300)
	order := listOrder(tree)
	focus, at, top := focusedUserInView(t, tree, order)
	onMain(func() { win.SetFocusVisible(false) })
	var under rowID
	onMain(func() { under, _ = rowAtView(tree, top-5) })

	moveUser(t, tree, b, focus.id, channelFarBelow(t, tree, order))
	keepsPlace(t, tree, order[at], 0, "the top row")
	var now rowID
	onMain(func() { now, _ = rowAtView(tree, top-5) })
	if now != under {
		t.Errorf("the row %.0f px down is %+v, was %+v", top-5, now, under)
	}
	if got, ok := focused(tree); !ok || got != focus {
		t.Errorf("focus on %+v (%v), want it to follow the moved user %+v", got, ok, focus)
	}
}

// A rebuild while the list is hidden (the sidebar folded away) does not
// keep the window laying out frame after frame while it waits for the
// rows to get a size; shown again, the top row is where it was.
func TestGTKHiddenListAnchorWaitsQuietly(t *testing.T) {
	needGTK(t)
	tree, b, _, entry := focusTree(t, 30, 300)
	onMain(func() { entry.GrabFocus() })
	order := listOrder(tree)
	at := len(order) * 6 / 10
	scrollRowToTop(t, tree, order[at])

	onMain(func() { tree.root.SetVisible(false) })
	time.Sleep(3 * rebuildInterval)
	var mu sync.Mutex
	layouts := 0
	var clock *gdk.FrameClock
	var h coreglib.SignalHandle
	onMain(func() {
		clock = gdk.BaseFrameClock(tree.list.FrameClock())
		h = clock.ConnectLayout(func() { mu.Lock(); layouts++; mu.Unlock() })
	})
	t.Cleanup(func() { onMain(func() { clock.HandlerDisconnect(h) }) })
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := layouts
		layouts = 0
		return n
	}
	time.Sleep(500 * time.Millisecond)
	idle := count()

	removeAndWait(t, tree, b, usersBetween(order, 0, at, 20)...)
	count()
	time.Sleep(500 * time.Millisecond)
	waiting := count()
	if waiting > idle+3 {
		t.Errorf("waiting for the hidden list laid out %d times in 500 ms (%d without a rebuild)", waiting, idle)
	}

	onMain(func() { tree.root.SetVisible(true) })
	time.Sleep(3 * rebuildInterval)
	keepsPlace(t, tree, order[at], 0, "the top row after showing the list again")
}
