package channeltree

import (
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
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
// what is on screen.
type listState struct {
	// focus lists the rows that may take the keyboard focus after the
	// rebuild, best first: the focused row, then (if it goes) the users
	// after it in the same channel, its channel, and the nearest rows
	// above and below it. Empty when the focus was not on a row.
	focus []focusCandidate

	selected    rowID
	hasSelected bool

	scroll float64

	// anchor lists the rows that keep their place on screen, best first:
	// the focused row if it is in view, else the top row in view; then
	// the rows below it and the rows above it, nearest first. With the
	// focus ring hidden (mouse use), the focused row is followed by the
	// top row in view and its neighbours instead, so that the view stays
	// as it is when the focused row goes. Empty when the rows had no
	// layout yet.
	anchor []anchorCandidate

	// focusShown: the focus ring was shown and the focused row was in
	// view. If the focus then ends on another row, or the same user in
	// another channel, that row is scrolled fully into view.
	focusShown bool
	// focusChannel is the channel the focused row was in (or is).
	focusChannel uint32
	// ensureFocus is carried over from a rebuild whose rows never got a
	// layout.
	ensureFocus bool
	// reveal: one's own row is to be scrolled into view (see
	// Tree.revealSelf), also carried over.
	reveal bool
}

// anchorCandidate is a row and how far its top was below the top of the
// view. A user only counts while still in the channel they were in, so
// that a user being moved elsewhere does not carry the view along.
type anchorCandidate struct {
	row     rowID
	channel uint32
	offset  float64
}

// pendingAnchor is applied once the new rows have a layout.
type pendingAnchor struct {
	anchor []anchorCandidate
	// ensureFocus scrolls the focused row fully into view (by as little
	// as possible) after anchoring.
	ensureFocus bool
	// reveal then does the same for one's own row; it wins where both do
	// not fit.
	reveal bool
	clock  *gdk.FrameClock
	layout coreglib.SignalHandle
	// retried is the frame in which the layout phase was asked to run
	// once more.
	retried int64
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

// saveListState records focus, selection and the rows' place on screen
// before the rows go.
func (t *Tree) saveListState() listState {
	adj := t.root.VAdjustment()
	s := listState{scroll: adj.Value()}
	if sel := t.list.SelectedRow(); sel != nil {
		s.selected, s.hasSelected = t.rowIDOf(sel)
	}

	// The rows in their current order, as identities.
	ids := make(map[uintptr]rowID, len(t.channelRows)+len(t.userRows))
	for id, r := range t.channelRows {
		ids[coreglib.InternObject(r.root).Native()] = rowID{id: id}
	}
	for session, r := range t.userRows {
		ids[coreglib.InternObject(r.root).Native()] = rowID{user: true, id: session}
	}
	var focused *gtk.ListBoxRow
	focusVisible := false
	if root := t.list.Root(); root != nil {
		focused = focusedRow(root.Focus(), t.list)
		if w, ok := coreglib.InternObject(root).Cast().(interface{ FocusVisible() bool }); ok {
			focusVisible = w.FocusVisible()
		}
	}
	var focusedNative uintptr
	if focused != nil {
		focusedNative = coreglib.InternObject(focused).Native()
	}
	var order []rowID
	var rows []*gtk.ListBoxRow
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
		rows = append(rows, t.rowFor(id))
	}
	if at >= 0 {
		s.focus = focusCandidates(order, at)
		s.focusChannel = channelAbove(order, at)
	}

	if t.anchor != nil {
		// The last rebuild's rows never got a layout: keep its anchor.
		s.anchor, s.scroll, s.ensureFocus, s.reveal = t.anchor.anchor, t.heldScroll, t.anchor.ensureFocus, t.anchor.reveal
		return s
	}
	if len(rows) == 0 || rows[0].Width() == 0 {
		return s
	}
	tops := make([]float64, len(rows))
	inView := make([]bool, len(rows))
	page := adj.PageSize()
	for i, row := range rows {
		top, height, ok := t.contentBounds(row)
		if !ok {
			return s
		}
		tops[i] = top
		inView[i] = top+height > s.scroll && top < s.scroll+page
	}
	top := -1
	for i := range rows {
		if inView[i] {
			top = i
			break
		}
	}
	switch {
	case at >= 0 && inView[at] && focusVisible:
		// Keyboard use: the focused row holds its place; if it goes, the
		// rows next to it do.
		s.anchor = anchorCandidates(order, at, tops, s.scroll)
		s.focusShown = true
	case at >= 0 && inView[at]:
		// Mouse use: the focused row holds its place; if it goes, the
		// view stays as it is, held by its top row.
		s.anchor = append(anchorCandidates(order, at, tops, s.scroll)[:1:1],
			anchorCandidates(order, top, tops, s.scroll)...)
	case top >= 0:
		s.anchor = anchorCandidates(order, top, tops, s.scroll)
	}
	return s
}

// anchorCandidates lists the rows that keep their place on screen when
// order[from] was the anchor: itself, the rows below, then the rows above,
// nearest first.
func anchorCandidates(order []rowID, from int, tops []float64, scroll float64) []anchorCandidate {
	out := make([]anchorCandidate, 0, len(order))
	add := func(i int) {
		out = append(out, anchorCandidate{row: order[i], channel: channelAbove(order, i), offset: tops[i] - scroll})
	}
	for i := from; i < len(order); i++ {
		add(i)
	}
	for i := from - 1; i >= 0; i-- {
		add(i)
	}
	return out
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

// restoreListState gives the new rows the focus and selection the old ones
// had, and keeps what was on screen there (see anchorAfterLayout).
func (t *Tree) restoreListState(s listState) {
	t.restoring = true
	defer func() { t.restoring = false }()

	// The list keeps its old height until the next layout, so the old
	// position is not clamped here.
	t.heldScroll, t.holdScroll = s.scroll, true
	t.root.VAdjustment().SetValue(s.scroll)

	var target *gtk.ListBoxRow
	if len(s.focus) > 0 {
		// The focus ring stays as shown or hidden.
		if target = t.focusTarget(s.focus); target != nil {
			target.GrabFocus()
		}
	}
	if s.hasSelected {
		if row := t.rowFor(s.selected); row != nil {
			t.list.SelectRow(row)
		}
	}

	// The focus moved to another row, or followed its user to another
	// channel: with the focus ring shown, that row must be in view.
	ensureFocus := s.ensureFocus
	if s.focusShown && target != nil {
		id, _ := t.rowIDOf(target)
		ensureFocus = id != s.focus[0].row || t.channelOf(id) != s.focusChannel
	}
	reveal := s.reveal || t.revealSelf
	t.revealSelf = false
	t.anchorAfterLayout(s.anchor, ensureFocus, reveal)
}

// channelOf is the channel a row is in (or, for a channel row, is).
func (t *Tree) channelOf(id rowID) uint32 {
	if id.user {
		return t.model.users[id.id].Channel
	}
	return id.id
}

// keepHeldScroll undoes scrolling after a rebuild until the new rows have
// a size. The list scrolls to a row it focuses (the restored focus, or
// the next row of an arrow key pressed during the rebuild); a row without
// a size counts as being at the top, so it would scroll to the top. Once
// the rows are laid out (and the anchor is applied), scrolling to the
// focused row works as usual.
func (t *Tree) keepHeldScroll() {
	if !t.holdScroll {
		return
	}
	if row := t.list.RowAtIndex(0); row == nil || (row.Width() > 0 && t.anchor == nil) {
		t.holdScroll = false
		return
	}
	if adj := t.root.VAdjustment(); adj.Value() != t.heldScroll {
		adj.SetValue(t.heldScroll)
	}
}

// anchorAfterLayout puts the anchor row back at its old place on screen
// as soon as the new rows are laid out: in the layout phase of the frame,
// after GTK's own layout, so that no frame is drawn with the rows in
// another place (setting the scroll value there lays out again before
// drawing).
func (t *Tree) anchorAfterLayout(anchor []anchorCandidate, ensureFocus, reveal bool) {
	t.dropAnchor()
	fc := t.list.FrameClock()
	if fc == nil || (len(anchor) == 0 && !ensureFocus && !reveal) {
		return
	}
	p := &pendingAnchor{anchor: anchor, ensureFocus: ensureFocus, reveal: reveal, clock: gdk.BaseFrameClock(fc), retried: -1}
	p.layout = coreglib.InternObject(p.clock).ConnectAfter("layout", func() {
		if t.anchor != p {
			return
		}
		if row := t.list.RowAtIndex(0); row != nil && row.Width() == 0 {
			// Not laid out yet in this pass: once more in this frame, so
			// that the frame is not drawn first. A list without a size
			// (hidden) waits for the next layout instead.
			if frame := p.clock.FrameCounter(); p.retried != frame {
				p.retried = frame
				p.clock.RequestPhase(gdk.FrameClockPhaseLayout)
			}
			return
		}
		t.dropAnchor()
		t.applyAnchor(p)
	})
	t.anchor = p
	p.clock.RequestPhase(gdk.FrameClockPhaseLayout)
}

// dropAnchor forgets a pending anchor.
func (t *Tree) dropAnchor() {
	if p := t.anchor; p != nil {
		t.anchor = nil
		coreglib.InternObject(p.clock).HandlerDisconnect(p.layout)
	}
}

// applyAnchor scrolls so that the first anchor candidate that is still
// there is where it was on screen, then (ensureFocus) by as little as
// possible so that the focused row is fully in view, and (reveal) one's
// own row.
func (t *Tree) applyAnchor(p *pendingAnchor) {
	adj := t.root.VAdjustment()
	value := t.heldScroll
	for _, c := range p.anchor {
		row := t.rowFor(c.row)
		if row == nil || (c.row.user && t.channelOf(c.row) != c.channel) {
			continue
		}
		if top, _, ok := t.contentBounds(row); ok {
			value = top - c.offset
			break
		}
	}
	show := func(row *gtk.ListBoxRow) {
		if top, height, ok := t.contentBounds(row); ok {
			bottom := top + height
			if bottom > value+adj.PageSize() {
				value = bottom - adj.PageSize()
			}
			if top < value {
				value = top
			}
		}
	}
	if p.ensureFocus {
		if root := t.list.Root(); root != nil {
			if row := focusedRow(root.Focus(), t.list); row != nil {
				show(row)
			}
		}
	}
	if p.reveal {
		if r, ok := t.userRows[t.model.self]; ok {
			show(r.root)
		}
	}
	value = max(adj.Lower(), min(value, adj.Upper()-adj.PageSize()))
	t.holdScroll = false
	adj.SetValue(value)
}

// contentBounds is the top and height of row in the scrolled content,
// the coordinates the scroll value counts in. The list itself may sit
// lower than the content's top (the sidebar style gives it a margin), so
// the list's own coordinates would be off by that much.
func (t *Tree) contentBounds(row *gtk.ListBoxRow) (top, height float64, ok bool) {
	b, ok := row.ComputeBounds(t.root.Child())
	if !ok {
		return 0, 0, false
	}
	return float64(b.Y()) + t.root.VAdjustment().Value(), float64(b.Height()), true
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
