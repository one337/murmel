package chat

// history is the session-only scrollback buffer: append-only, dropping the
// oldest entry once the limit is reached. Generic on purpose so tests run
// without GTK.
type history[T any] struct {
	limit int
	items []T
}

func newHistory[T any](limit int) *history[T] {
	if limit < 1 {
		panic("chat: history limit must be >= 1")
	}
	return &history[T]{limit: limit, items: make([]T, 0, limit)}
}

// push appends item and reports the dropped oldest item, if any.
func (h *history[T]) push(item T) (dropped T, droppedOK bool) {
	if len(h.items) == h.limit {
		dropped = h.items[0]
		droppedOK = true
		h.items = h.items[1:]
	}
	h.items = append(h.items, item)
	return dropped, droppedOK
}

func (h *history[T]) len() int { return len(h.items) }

func (h *history[T]) last() (item T, ok bool) {
	if len(h.items) == 0 {
		return item, false
	}
	return h.items[len(h.items)-1], true
}
