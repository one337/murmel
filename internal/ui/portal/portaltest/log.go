package portaltest

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

// GLibProblems collects the GLib warnings and criticals logged from now
// until the test ends. The returned function first lets the garbage
// collector run, since gotk4 frees GLib values in finalizers, and then
// reports what was logged.
func GLibProblems(t testing.TB) func() []string {
	prev := slog.Default()
	// A handler of its own: wrapping slog's default handler deadlocks once
	// SetDefault has pointed the log package at the wrapper.
	h := &collector{next: slog.NewTextHandler(os.Stderr, nil), store: &store{}}
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() []string {
		for range 5 {
			runtime.GC()
			time.Sleep(20 * time.Millisecond)
		}
		h.store.mu.Lock()
		defer h.store.mu.Unlock()
		return append([]string(nil), h.store.msgs...)
	}
}

type collector struct {
	next  slog.Handler
	store *store
}

type store struct {
	mu   sync.Mutex
	msgs []string
}

func (c *collector) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelWarn || c.next.Enabled(ctx, l)
}

func (c *collector) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		fromGLib := false
		r.Attrs(func(a slog.Attr) bool {
			fromGLib = a.Key == "glib_domain"
			return !fromGLib
		})
		if fromGLib {
			c.store.mu.Lock()
			c.store.msgs = append(c.store.msgs, r.Message)
			c.store.mu.Unlock()
		}
	}
	if !c.next.Enabled(ctx, r.Level) {
		return nil
	}
	return c.next.Handle(ctx, r)
}

func (c *collector) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &collector{next: c.next.WithAttrs(attrs), store: c.store}
}

func (c *collector) WithGroup(name string) slog.Handler {
	return &collector{next: c.next.WithGroup(name), store: c.store}
}
