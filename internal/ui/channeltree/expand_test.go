package channeltree

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// rowObjects maps every row to its GObject.
func rowObjects(tree *Tree) map[rowID]uintptr {
	out := map[rowID]uintptr{}
	onMain(func() {
		for id, r := range tree.channelRows {
			out[rowID{id: id}] = coreglib.InternObject(r.root).Native()
		}
		for s, r := range tree.userRows {
			out[rowID{user: true, id: s}] = coreglib.InternObject(r.root).Native()
		}
	})
	return out
}

// sameObjects checks that every row of before that still exists is the
// same object.
func sameObjects(t *testing.T, before, after map[rowID]uintptr, what string) {
	t.Helper()
	replaced := 0
	for id, obj := range after {
		if old, ok := before[id]; ok && old != obj {
			replaced++
		}
	}
	if replaced > 0 {
		t.Errorf("%s replaced %d rows that stayed", what, replaced)
	}
}

// nestedTree is a focus tree where channel 6 lies in channel 3 and channel
// 7 in channel 6, all with users.
func nestedTree(t *testing.T) (*Tree, *bus.Bus, *gtk.Window) {
	t.Helper()
	tree, b, win, _ := focusTree(t, 5, 40)
	b.Emit(bus.ChannelAdded{Channel: bus.ChannelInfo{ID: 6, ParentID: 3, Name: "Channel 3a"}})
	b.Emit(bus.ChannelAdded{Channel: bus.ChannelInfo{ID: 7, ParentID: 6, Name: "Channel 3b"}})
	for s := uint32(100); s < 106; s++ {
		b.Emit(bus.UserAdded{User: bus.UserInfo{Session: s, Name: "nested", Channel: 6 + s%2}})
	}
	waitFor(t, "the sub-channels", func() bool {
		return len(tree.channelRows) == 8 && len(tree.userRows) == 46 && !tree.rebuildPending && !tree.rebuildScheduled
	})
	time.Sleep(3 * rebuildInterval)
	return tree, b, win
}

// Closing and opening a channel adds and removes only the rows inside it;
// all other rows, the channel's own among them, stay the same objects.
func TestGTKExpandCollapseKeepsTheOtherRows(t *testing.T) {
	needGTK(t)
	tree, _, _ := nestedTree(t)
	before := rowObjects(tree)
	inside := usersOf(tree, 3)[0]
	onMain(func() { tree.toggleExpand(3) })
	closed := rowObjects(tree)
	sameObjects(t, before, closed, "closing channel 3")
	for _, id := range []rowID{{id: 6}, {id: 7}, {user: true, id: inside}} {
		if _, ok := closed[id]; ok {
			t.Errorf("%+v is still shown in the closed channel", id)
		}
	}
	checkListMatchesModel(t, tree)

	onMain(func() { tree.toggleExpand(3) })
	sameObjects(t, closed, rowObjects(tree), "opening channel 3")
	if n := len(rowObjects(tree)); n != len(before) {
		t.Errorf("after opening again %d rows, want %d", n, len(before))
	}
	checkListMatchesModel(t, tree)
}

// A sub-channel keeps its state while its parent is closed.
func TestGTKExpandKeepsSubChannelClosed(t *testing.T) {
	needGTK(t)
	tree, _, _ := nestedTree(t)
	onMain(func() {
		tree.toggleExpand(6)
		tree.toggleExpand(3)
		tree.toggleExpand(3)
	})
	objs := rowObjects(tree)
	if _, ok := objs[rowID{id: 6}]; !ok {
		t.Error("channel 6 is not shown after opening its parent")
	}
	if _, ok := objs[rowID{id: 7}]; ok {
		t.Error("channel 7 is shown although channel 6 stayed closed")
	}
	checkListMatchesModel(t, tree)
}

// The focus on a row that goes with a closing channel moves to the
// channel's row, with the focus ring as it was; focus elsewhere stays.
func TestGTKCollapseMovesFocusToTheChannel(t *testing.T) {
	needGTK(t)
	for _, visible := range []bool{true, false} {
		tree, _, win := nestedTree(t)
		focusRow(t, tree, rowID{user: true, id: 101}) // in channel 7, inside 6 and 3
		onMain(func() { win.SetFocusVisible(visible) })
		onMain(func() { tree.toggleExpand(3) })
		if got, ok := focused(tree); !ok || got != (rowID{id: 3}) {
			t.Errorf("focus on %+v (%v), want channel 3", got, ok)
		}
		var ring bool
		onMain(func() { ring = win.FocusVisible() })
		if ring != visible {
			t.Errorf("focus ring %v, want %v as before", ring, visible)
		}

		focusRow(t, tree, rowID{id: 1})
		onMain(func() { tree.toggleExpand(3) })
		if got, ok := focused(tree); !ok || got != (rowID{id: 1}) {
			t.Errorf("opening channel 3 moved the focus from channel 1 to %+v (%v)", got, ok)
		}
	}
}

// The channel's row and the rows above it keep their place on screen.
func TestGTKExpandCollapseKeepsThePlace(t *testing.T) {
	needGTK(t)
	tree, _, _ := nestedTree(t)
	order := listOrder(tree)
	scrollRowToTop(t, tree, order[5])
	var top3 float64
	onMain(func() { top3, _, _ = viewTop(tree, rowID{id: 3}) })
	for i := 0; i < 2; i++ {
		onMain(func() { tree.toggleExpand(3) })
		time.Sleep(3 * rebuildInterval)
		keepsPlace(t, tree, order[5], 0, "the top row")
		keepsPlace(t, tree, rowID{id: 3}, top3, "channel 3")
	}
}

// Toggling while a rebuild waits for its turn still ends with the right
// rows.
func TestGTKExpandWhileRebuildPending(t *testing.T) {
	needGTK(t)
	tree, b, _ := nestedTree(t)
	gone := usersOf(tree, 3)[0]
	inOnePass(b, []bus.Event{bus.UserRemoved{Session: gone}}, func() {
		tree.toggleExpand(3)
		tree.toggleExpand(6)
	})
	waitFor(t, "the rebuild", func() bool { return !tree.rebuildPending && !tree.rebuildScheduled })
	time.Sleep(2 * rebuildInterval)
	checkListMatchesModel(t, tree)
	onMain(func() {
		if tree.model.channels[3].expanded || tree.model.channels[6].expanded {
			t.Error("the channels did not close")
		}
	})
}

// Right and Left open and close channels and move along the tree; the
// chat target stays.
func TestGTKLeftRightKeys(t *testing.T) {
	needGTK(t)
	tree, b, _ := nestedTree(t)
	targets := record[bus.ChatTargetChanged](t, b)
	k := keys{tree}
	step := func(start rowID, right bool, wantFocus rowID, wantOpen map[uint32]bool, what string) {
		t.Helper()
		focusRow(t, tree, start)
		key := uint(gdk.KEY_Left)
		if right {
			key = gdk.KEY_Right
		}
		var handled bool
		onMain(func() { handled = press(k, key, 0) })
		if got, ok := focused(tree); !handled || !ok || got != wantFocus {
			t.Errorf("%s: handled %v, focus on %+v (%v), want %+v", what, handled, got, ok, wantFocus)
		}
		onMain(func() {
			for ch, open := range wantOpen {
				if tree.model.channels[ch].expanded != open {
					t.Errorf("%s: channel %d open %v, want %v", what, ch, !open, open)
				}
			}
		})
		checkListMatchesModel(t, tree)
	}
	user7 := rowID{user: true, id: 101}
	step(rowID{id: 6}, false, rowID{id: 6}, map[uint32]bool{6: false}, "Left on the open channel 6")
	step(rowID{id: 6}, false, rowID{id: 3}, map[uint32]bool{6: false}, "Left on the closed channel 6")
	step(rowID{id: 6}, true, rowID{id: 6}, map[uint32]bool{6: true}, "Right on the closed channel 6")
	var first rowID
	onMain(func() {
		row := tree.channelRows[6].root
		first, _ = tree.rowIDOf(tree.list.RowAtIndex(row.Index() + 1))
	})
	step(rowID{id: 6}, true, first, map[uint32]bool{6: true}, "Right on the open channel 6")
	step(user7, false, rowID{id: 7}, nil, "Left on a user")
	step(user7, true, user7, nil, "Right on a user")
	step(rowID{id: 0}, false, rowID{id: 0}, map[uint32]bool{0: false}, "Left on the open top channel")
	step(rowID{id: 0}, false, rowID{id: 0}, map[uint32]bool{0: false}, "Left on the closed top channel")
	step(rowID{id: 0}, true, rowID{id: 0}, map[uint32]bool{0: true}, "Right on the closed top channel")
	onMain(func() {
		if tree.list.SelectedRow() != nil {
			t.Error("Left/Right selected a row")
		}
	})
	if got := targets(0); len(got) != 0 {
		t.Errorf("Left/Right changed the chat target: %+v", got)
	}
}

// Right on a channel without users or sub-channels does nothing.
func TestGTKRightOnEmptyChannel(t *testing.T) {
	needGTK(t)
	tree, _, _, _ := focusTree(t, 6, 5) // channel 1 has no users
	focusRow(t, tree, rowID{id: 1})
	onMain(func() { press(keys{tree}, gdk.KEY_Right, 0) })
	if got, ok := focused(tree); !ok || got != (rowID{id: 1}) {
		t.Errorf("Right on the empty channel 1 moved the focus to %+v (%v)", got, ok)
	}
}

// Channel rows with something to open tell assistive technologies whether
// they are open; empty channels say nothing about it.
func TestGTKChannelRowsAnnounceExpanded(t *testing.T) {
	needGTK(t)
	tree, _, _, _ := focusTree(t, 6, 5) // channel 1 has no users
	onMain(func() {
		if !gtk.TestAccessibleHasState(tree.channelRows[2].root, gtk.AccessibleStateExpanded) {
			t.Error("channel 2 (with users) does not announce expanded")
		}
		if gtk.TestAccessibleHasState(tree.channelRows[1].root, gtk.AccessibleStateExpanded) {
			t.Error("the empty channel 1 announces expanded")
		}
		tree.toggleExpand(2)
		if !gtk.TestAccessibleHasState(tree.channelRows[2].root, gtk.AccessibleStateExpanded) {
			t.Error("the closed channel 2 does not announce its state")
		}
	})
}

// Rows removed by closing a channel are freed, in Go and in GTK.
func TestGTKCollapseFreesRows(t *testing.T) {
	needGTK(t)
	tree, _, _, _ := focusTree(t, 5, 100)
	var structs []weak.Pointer[userRowWidgets]
	var finalized atomic.Int32
	inside := usersOf(tree, 3)
	onMain(func() {
		for _, s := range inside {
			r := tree.userRows[s]
			structs = append(structs, weak.Make(r))
			coreglib.WeakRefObject(r.root, func() { finalized.Add(1) })
		}
	})
	for range 20 {
		onMain(func() { tree.toggleExpand(3) })
	}
	const slack = 2
	var alive int
	for range 3 {
		collect()
		alive = 0
		for _, p := range structs {
			if p.Value() != nil {
				alive++
			}
		}
		if alive <= slack && int(finalized.Load()) >= len(structs)-slack {
			return
		}
		runtime.Gosched()
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("of %d user rows removed by closing, %d still alive in Go, %d of them finalized in GTK", len(structs), alive, finalized.Load())
}

