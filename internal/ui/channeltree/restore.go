package channeltree

import (
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// rowID names a row by what it shows: a channel by its ID, a user by
// their session. A rebuild replaces every row, so focus and selection are
// carried over by this identity, not by position.
type rowID struct {
	user bool
	id   uint32
}

// listState is what a rebuild must keep: keyboard focus, selection and
// scroll position.
type listState struct {
	// focus lists the rows that may take the keyboard focus after the
	// rebuild, best first: the focused row, then (if it goes) the users
	// after it in the same channel, its channel, and the nearest rows
	// above and below it. Empty when the focus was not on a row.
	focus []focusCandidate

	selected    rowID
	hasSelected bool

	scroll float64
}

// focusCandidate is a row to focus. A user other than the focused one
// only counts while still in the channel the old row was in; the focused
// user keeps the focus wherever they went (anyChannel).
type focusCandidate struct {
	row        rowID
	channel    uint32
	anyChannel bool
}

// rowIDOf finds which channel or user row is row.
func (t *Tree) rowIDOf(row *gtk.ListBoxRow) (rowID, bool) {
	for id, r := range t.channelRows {
		if sameRow(r.root, row) {
			return rowID{id: id}, true
		}
	}
	for session, r := range t.userRows {
		if sameRow(r.root, row) {
			return rowID{user: true, id: session}, true
		}
	}
	return rowID{}, false
}

// rowFor returns the current row of id, nil if there is none.
func (t *Tree) rowFor(id rowID) *gtk.ListBoxRow {
	if id.user {
		if r, ok := t.userRows[id.id]; ok {
			return r.root
		}
		return nil
	}
	if r, ok := t.channelRows[id.id]; ok {
		return r.root
	}
	return nil
}

// saveListState records focus, selection and scroll position before the
// rows go.
func (t *Tree) saveListState() listState {
	s := listState{scroll: t.root.VAdjustment().Value()}
	if sel := t.list.SelectedRow(); sel != nil {
		s.selected, s.hasSelected = t.rowIDOf(sel)
	}
	root := t.list.Root()
	if root == nil {
		return s
	}
	focused := focusedRow(root.Focus(), t.list)
	if focused == nil {
		return s
	}

	// The rows in their current order, as identities.
	ids := make(map[uintptr]rowID, len(t.channelRows)+len(t.userRows))
	for id, r := range t.channelRows {
		ids[coreglib.InternObject(r.root).Native()] = rowID{id: id}
	}
	for session, r := range t.userRows {
		ids[coreglib.InternObject(r.root).Native()] = rowID{user: true, id: session}
	}
	focusedNative := coreglib.InternObject(focused).Native()
	var order []rowID
	at := -1
	for c := t.list.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		native := coreglib.InternObject(c).Native()
		id, ok := ids[native]
		if !ok {
			continue // the placeholder
		}
		if native == focusedNative {
			at = len(order)
		}
		order = append(order, id)
	}
	if at < 0 {
		return s
	}
	s.focus = focusCandidates(order, at)
	return s
}

// focusCandidates lists the rows to focus when order[at] had the focus, in
// the order focus falls back to them.
func focusCandidates(order []rowID, at int) []focusCandidate {
	// The channel the focused row is in: itself, or the channel row above.
	channel := uint32(0)
	for i := at; i >= 0; i-- {
		if !order[i].user {
			channel = order[i].id
			break
		}
	}
	out := []focusCandidate{{row: order[at], anyChannel: true}}
	if order[at].user {
		for i := at + 1; i < len(order) && order[i].user; i++ {
			out = append(out, focusCandidate{row: order[i], channel: channel})
		}
		for i := at - 1; i >= 0; i-- {
			if !order[i].user {
				out = append(out, focusCandidate{row: order[i]})
				break
			}
		}
	}
	// Nearest rows above, then below: users only in the channel they
	// were in.
	for i := at - 1; i >= 0; i-- {
		out = append(out, focusCandidate{row: order[i], channel: channelAbove(order, i)})
	}
	for i := at + 1; i < len(order); i++ {
		out = append(out, focusCandidate{row: order[i], channel: channelAbove(order, i)})
	}
	return out
}

// channelAbove is the channel order[i] is in (or is).
func channelAbove(order []rowID, i int) uint32 {
	for ; i >= 0; i-- {
		if !order[i].user {
			return order[i].id
		}
	}
	return 0
}

// focusedRow returns the row of list that holds the focus widget, nil if
// the focus is elsewhere.
func focusedRow(focus gtk.Widgetter, list *gtk.ListBox) *gtk.ListBoxRow {
	listNative := coreglib.InternObject(list).Native()
	for w := focus; w != nil; {
		base := gtk.BaseWidget(w)
		parent := base.Parent()
		if parent == nil {
			return nil
		}
		if coreglib.InternObject(parent).Native() == listNative {
			row, _ := coreglib.InternObject(w).Cast().(*gtk.ListBoxRow)
			return row
		}
		w = parent
	}
	return nil
}

// restoreListState gives the new rows the focus, selection and scroll
// position the old ones had.
func (t *Tree) restoreListState(s listState) {
	t.restoring = true
	defer func() { t.restoring = false }()

	// The list keeps its old height until the next layout, so the old
	// position is not clamped here.
	t.heldScroll, t.holdScroll = s.scroll, true
	t.root.VAdjustment().SetValue(s.scroll)

	if len(s.focus) > 0 {
		// The focus ring stays as shown or hidden.
		if row := t.focusTarget(s.focus); row != nil {
			row.GrabFocus()
		}
	}
	if s.hasSelected {
		if row := t.rowFor(s.selected); row != nil {
			t.list.SelectRow(row)
		}
	}
}

// keepHeldScroll undoes scrolling after a rebuild until the new rows have
// a size. The list scrolls to a row it focuses (the restored focus, or
// the next row of an arrow key pressed during the rebuild); a row without
// a size counts as being at the top, so it would scroll to the top. Once
// the rows are laid out, scrolling to the focused row works as usual.
func (t *Tree) keepHeldScroll() {
	if !t.holdScroll {
		return
	}
	if row := t.list.RowAtIndex(0); row == nil || row.Width() > 0 {
		t.holdScroll = false
		return
	}
	if adj := t.root.VAdjustment(); adj.Value() != t.heldScroll {
		adj.SetValue(t.heldScroll)
	}
}

// focusTarget picks the first candidate that still has a row (a user only
// while still in the same channel).
func (t *Tree) focusTarget(cands []focusCandidate) *gtk.ListBoxRow {
	for _, c := range cands {
		row := t.rowFor(c.row)
		if row == nil {
			continue
		}
		if c.row.user && !c.anyChannel {
			if u, ok := t.model.users[c.row.id]; !ok || u.Channel != c.channel {
				continue
			}
		}
		return row
	}
	return nil
}
