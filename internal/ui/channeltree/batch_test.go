package channeltree

import (
	"context"
	"sync"
	"testing"
	"time"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
)

// batchTree builds a tree with channels channels and users users in a
// window and waits until it shows them.
func batchTree(t *testing.T, channels, users int) (*Tree, *bus.Bus, *gtk.Window) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := bus.New(nil)
	var tree *Tree
	var win *gtk.Window
	onMain(func() {
		win = gtk.NewWindow()
		win.SetDefaultSize(320, 600)
		tree = NewTree(ctx, b, win)
		win.SetChild(tree.Root())
		win.Present()
	})
	t.Cleanup(func() { onMain(func() { win.Destroy() }) })
	b.Emit(connectedEvent(channels, users))
	waitFor(t, "the tree to be built", func() bool {
		return len(tree.userRows) == users && len(tree.channelRows) == channels+1
	})
	return tree, b, win
}

// waitFor polls cond on the main loop until it holds (5 s at most).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	ok := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		onMain(func() { ok = cond() })
		if ok {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// inOnePass emits evs while the main loop is held, so the bus queues all of
// them before the first one is handled: they arrive in one main-loop pass.
// probe runs right after the last of them was handled.
func inOnePass(b *bus.Bus, evs []bus.Event, probe func()) {
	done := make(chan struct{})
	onMain(func() {
		for _, ev := range evs {
			b.Emit(ev)
		}
		time.Sleep(100 * time.Millisecond) // the bus queues its idle callbacks
		glib.IdleAdd(func() bool { probe(); close(done); return false })
	})
	<-done
}

// countInserted counts the rows the list takes in from now on (GTK runs
// the filter function once for every inserted row; the tree sets none);
// the returned func stops counting and reports the number.
func countInserted(tree *Tree) func() int {
	var mu sync.Mutex
	n := 0
	onMain(func() {
		tree.list.SetFilterFunc(func(*gtk.ListBoxRow) bool {
			mu.Lock()
			n++
			mu.Unlock()
			return true
		})
		// Setting the function filters the rows already there: not counted.
		mu.Lock()
		n = 0
		mu.Unlock()
	})
	return func() int {
		var rows int
		onMain(func() {
			tree.list.SetFilterFunc(nil)
			mu.Lock()
			rows = n
			mu.Unlock()
		})
		return rows
	}
}

// countRebuilds counts the rebuilds from now on by watching lastRebuild,
// which every rebuild sets; two rebuilds are at least rebuildInterval
// apart, far more than the 5 ms between looks. The returned func stops
// counting and reports the number.
func countRebuilds(tree *Tree) func() int {
	var last time.Time
	onMain(func() { last = tree.lastRebuild })
	n := 0
	look := func() {
		if !tree.lastRebuild.Equal(last) {
			n++
			last = tree.lastRebuild
		}
	}
	stop := make(chan chan int)
	go func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case reply := <-stop:
				onMain(look)
				reply <- n
				return
			case <-tick.C:
				onMain(look)
			}
		}
	}()
	return func() int {
		reply := make(chan int)
		stop <- reply
		return <-reply
	}
}

// checkListMatchesModel fails unless the list shows exactly the model's
// walk order, one row per channel and user.
func checkListMatchesModel(t *testing.T, tree *Tree) {
	t.Helper()
	onMain(func() {
		walk := tree.model.walk()
		i := 0
		for c := tree.list.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
			row, isRow := coreglib.InternObject(c).Cast().(*gtk.ListBoxRow)
			if !isRow {
				continue // the placeholder
			}
			if i >= len(walk) {
				t.Errorf("the list has more rows than the model (%d)", len(walk))
				return
			}
			var want *gtk.ListBoxRow
			if e := walk[i]; e.kind == entryChannel {
				if r, ok := tree.channelRows[e.id]; ok {
					want = r.root
				}
			} else if r, ok := tree.userRows[e.id]; ok {
				want = r.root
			}
			if !sameRow(row, want) {
				t.Errorf("row %d does not show model entry %+v", i, walk[i])
				return
			}
			i++
		}
		if i != len(walk) {
			t.Errorf("the list shows %d rows, the model %d", i, len(walk))
		}
	})
}

// Structural changes that arrive in one main-loop pass (20 users leaving,
// 5 moving) are shown by one rebuild, not one per event.
func TestGTKStructuralEventsAreBatched(t *testing.T) {
	needGTK(t)
	const channels, users = 30, 100
	tree, b, _ := batchTree(t, channels, users)
	time.Sleep(2 * rebuildInterval)

	var evs []bus.Event
	for s := 1; s <= 20; s++ {
		evs = append(evs, bus.UserRemoved{Session: uint32(s * 4)})
	}
	var moved []uint32
	onMain(func() {
		for s := uint32(1); len(moved) < 5; s += 4 {
			u := tree.model.users[s]
			u.Channel = u.Channel%channels + 1
			evs = append(evs, bus.UserUpdated{User: u})
			moved = append(moved, s)
		}
	})
	stop := countRebuilds(tree)
	var rowsDuring int
	var pending bool
	inOnePass(b, evs, func() {
		rowsDuring = len(tree.userRows)
		pending = tree.rebuildPending
	})
	waitFor(t, "the batched rebuild", func() bool { return !tree.rebuildPending && !tree.rebuildScheduled })
	time.Sleep(3 * rebuildInterval) // a second rebuild would show here
	rebuilds := stop()

	if rowsDuring != users || !pending {
		t.Errorf("after the last event: %d user rows and pending %v, want the old %d rows and a pending rebuild", rowsDuring, pending, users)
	}
	if rebuilds != 1 {
		t.Errorf("25 structural events in one pass caused %d rebuilds, want 1", rebuilds)
	}
	onMain(func() {
		if len(tree.userRows) != users-20 {
			t.Errorf("the tree shows %d users, want %d", len(tree.userRows), users-20)
		}
		for _, s := range moved {
			if _, ok := tree.userRows[s]; !ok {
				t.Errorf("moved user %d has no row", s)
			}
		}
	})
	checkListMatchesModel(t, tree)
}

// Structural changes spread over a second (20 users leaving 50 ms apart)
// cause far fewer rebuilds than events: at most one per rebuildInterval.
func TestGTKSpreadEventsAreThrottled(t *testing.T) {
	needGTK(t)
	tree, b, _ := batchTree(t, 30, 100)
	time.Sleep(2 * rebuildInterval)

	stop := countRebuilds(tree)
	start := time.Now()
	for s := 1; s <= 20; s++ {
		b.Emit(bus.UserRemoved{Session: uint32(s * 4)})
		time.Sleep(50 * time.Millisecond)
	}
	waitFor(t, "the last rebuild", func() bool {
		return len(tree.userRows) == 80 && !tree.rebuildPending && !tree.rebuildScheduled
	})
	elapsed := time.Since(start)
	time.Sleep(3 * rebuildInterval)
	rebuilds := stop()

	limit := int(elapsed/rebuildInterval) + 2
	t.Logf("20 events over %v: %d rebuilds", elapsed.Round(time.Millisecond), rebuilds)
	if rebuilds > limit || rebuilds > 15 {
		t.Errorf("20 events over %v caused %d rebuilds, want at most %d", elapsed.Round(time.Millisecond), rebuilds, min(limit, 15))
	}
	checkListMatchesModel(t, tree)
}

// A user leaves, and before the batched rebuild shows it, row updates for
// that user arrive (voice activity, local volume, unread count) and their
// context menu is asked for. Nothing touches the user the model no longer
// knows: the old row is left as it is until it goes, and no menu opens.
func TestGTKUpdatesForALeavingUserBeforeTheRebuild(t *testing.T) {
	needGTK(t)
	tree, b, _ := batchTree(t, 5, 20)
	time.Sleep(2 * rebuildInterval)

	const gone = 7
	var speakingShown, menuOpened, rowStillThere, modelKnows bool
	inOnePass(b, []bus.Event{
		bus.UserRemoved{Session: gone},
		bus.VoiceActivity{Session: gone, Active: true},
		bus.UserLocalAudioChanged{Session: gone, Volume: 0.5},
		bus.UnreadChanged{Session: gone, Count: 3},
	}, func() {
		r, ok := tree.userRows[gone]
		rowStillThere = ok
		if ok {
			speakingShown = r.icons[iconSpeaking].Visible()
		}
		_, modelKnows = tree.model.users[gone]
		// The context menu catches up on the rebuild first.
		tree.showUserMenu(gone, 0, 0)
		menuOpened = tree.menuRefresh != nil
	})
	if !rowStillThere {
		t.Error("the old row went before the batched rebuild (test does not cover the gap)")
	}
	if modelKnows {
		t.Error("the model still knows the user who left")
	}
	if speakingShown {
		t.Error("voice activity of a user who left was shown on the old row")
	}
	if menuOpened {
		t.Error("a context menu opened for a user who left")
	}
	waitFor(t, "the rebuild", func() bool { return !tree.rebuildPending && !tree.rebuildScheduled })
	onMain(func() {
		if _, ok := tree.userRows[gone]; ok {
			t.Error("the user who left still has a row")
		}
	})
	checkListMatchesModel(t, tree)
}

// A batched rebuild keeps the top row of the view where it was, as the
// single rebuild does.
func TestGTKBatchedRebuildKeepsTheView(t *testing.T) {
	needGTK(t)
	tree, b, _ := batchTree(t, 30, 300)
	time.Sleep(3 * rebuildInterval)

	var scroll, top float64
	var anchor rowID
	onMain(func() {
		adj := tree.root.VAdjustment()
		adj.SetValue((adj.Upper() - adj.PageSize()) * 0.9)
	})
	time.Sleep(3 * rebuildInterval)
	onMain(func() {
		scroll = tree.root.VAdjustment().Value()
		anchor, _ = rowAtView(tree, 0)
		top, _, _ = viewTop(tree, anchor)
	})
	if scroll < 1000 {
		t.Fatalf("could not scroll down (at %.0f)", scroll)
	}
	var evs []bus.Event
	for s := 1; s <= 20; s++ {
		evs = append(evs, bus.UserRemoved{Session: uint32(s)})
	}
	inOnePass(b, evs, func() {})
	waitFor(t, "the rebuild", func() bool { return len(tree.userRows) == 280 && !tree.rebuildPending })
	time.Sleep(3 * rebuildInterval)
	keepsPlace(t, tree, anchor, top, "the top row")
}
