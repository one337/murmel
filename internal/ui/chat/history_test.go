package chat

import "testing"

func TestHistoryCap(t *testing.T) {
	const limit = 1000
	h := newHistory[int](limit)

	dropped, ok := h.push(1)
	if dropped != 0 || ok {
		t.Fatalf("first push reported a drop: %d %v", dropped, ok)
	}
	for i := 2; i <= limit; i++ {
		h.push(i)
	}
	if h.len() != limit {
		t.Fatalf("len = %d, want %d", h.len(), limit)
	}

	for i := limit + 1; i <= limit+200; i++ {
		dropped, ok := h.push(i)
		if !ok {
			t.Fatalf("push %d: expected a drop", i)
		}
		want := i - limit
		if dropped != want {
			t.Fatalf("push %d dropped %d, want %d", i, dropped, want)
		}
	}
	if h.len() != limit {
		t.Fatalf("len after overflow = %d, want %d", h.len(), limit)
	}

	last, ok := h.last()
	if !ok || last != limit+200 {
		t.Fatalf("last = %d %v, want %d true", last, ok, limit+200)
	}
}

func TestHistorySmallLimit(t *testing.T) {
	h := newHistory[string](2)
	h.push("a")
	h.push("b")
	dropped, ok := h.push("c")
	if !ok || dropped != "a" {
		t.Fatalf("dropped = %q %v, want \"a\" true", dropped, ok)
	}
	if h.len() != 2 {
		t.Fatalf("len = %d, want 2", h.len())
	}
	last, _ := h.last()
	if last != "c" {
		t.Fatalf("last = %q, want c", last)
	}
}

func TestHistoryLastEmpty(t *testing.T) {
	h := newHistory[int](5)
	if _, ok := h.last(); ok {
		t.Fatal("last on empty history reported ok")
	}
}

func TestNewHistoryRejectsBadLimit(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("newHistory(0) did not panic")
		}
	}()
	newHistory[int](0)
}

func TestHistoryClear(t *testing.T) {
	h := newHistory[int](3)
	for i := 1; i <= 4; i++ {
		h.push(i)
	}
	got := h.clear()
	if len(got) != 3 || got[0] != 2 || h.len() != 0 {
		t.Fatalf("clear returned %v, len %d", got, h.len())
	}
	h.push(9)
	if last, ok := h.last(); !ok || last != 9 || h.len() != 1 {
		t.Fatalf("after clear: last %d %v len %d", last, ok, h.len())
	}
}
