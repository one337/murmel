package bus

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"

	"io.github/murmel/internal/protocol/gumble"
)

// bufferSize is the event channel capacity mandated by the plan. It is large
// enough to absorb bursts (server sync delivers the whole channel/user tree
// as a burst of Added events); overflow drops instead of blocking.
const bufferSize = 256

// Bus ferries events from gumble's blocking listener callbacks to handlers
// running on the main loop (see SetMainLoop). Create one per connection with New; events
// flow: gumble listener → copy → non-blocking send → pump goroutine → state
// store + main-loop dispatch → subscribed handlers.
//
// Handlers must not be registered or invoked on the network loop; Emit is the
// only method safe to call from any goroutine at any rate.
type Bus struct {
	events chan Event
	done   chan struct{}
	wg     sync.WaitGroup

	closeOnce sync.Once

	dropped atomic.Uint64

	// idleAdd schedules a callback on the main loop: dispatchMainLoop in
	// production; injectable in tests.
	idleAdd func(f func() bool)
	logger  *slog.Logger

	store *store
	reg   *registry

	detachGumble func()
}

// New creates a Bus and, if client is non-nil, attaches the bridge listener
// to the client's configuration via the public gumble API. A nil client
// yields a working bus with no gumble bridge — tests and pre-connection UI
// drive it via Emit.
func New(client *gumble.Client) *Bus {
	b := newBus(dispatchMainLoop, slog.Default())
	if client != nil && client.Config != nil {
		b.detachGumble = attachGumbleBridge(b, client)
	}
	return b
}

// NewForConfig creates a Bus whose gumble bridge is attached to cfg before
// the client exists. Use it for a connection that is about to be dialed:
// gumble fires OnConnect (and the initial channel/user state) from inside
// DialWithDialer when ServerSync arrives, so a bridge attached after Dial
// returns would miss the Connected event.
func NewForConfig(cfg *gumble.Config) *Bus {
	b := newBus(dispatchMainLoop, slog.Default())
	if cfg != nil {
		detacher := cfg.Attach(&gumbleBridge{bus: b})
		b.detachGumble = detacher.Detach
	}
	return b
}

func newBus(idleAdd func(f func() bool), logger *slog.Logger) *Bus {
	b := &Bus{
		events:  make(chan Event, bufferSize),
		done:    make(chan struct{}),
		idleAdd: idleAdd,
		logger:  logger,
		store:   newStore(),
		reg:     newRegistry(),
	}
	b.wg.Add(1)
	go b.pump()
	return b
}

// pump is the single consumer of the event channel and the single writer of
// the state store. For each event it first folds the event into the store,
// then schedules dispatch on the main loop. Idle sources of equal priority
// run FIFO, so handler dispatch order matches send order.
//
// When Close is called, pump drains every event already buffered before
// exiting, so events emitted before Close returns are always applied to the
// store and handed to idleAdd — never silently lost.
func (b *Bus) pump() {
	defer b.wg.Done()
	for {
		select {
		case <-b.done:
			b.drain()
			return
		case ev := <-b.events:
			b.handle(ev)
		}
	}
}

func (b *Bus) drain() {
	for {
		select {
		case ev := <-b.events:
			b.handle(ev)
		default:
			return
		}
	}
}

func (b *Bus) handle(ev Event) {
	b.store.apply(ev)
	b.idleAdd(func() bool {
		b.dispatch(ev)
		return false
	})
}

// dispatch runs on the main loop and fans the event out to the live
// subscribers of the event's concrete type. The subscriber list is snapshotted
// here, at run time; each subscription additionally checks its dead flag
// under its own mutex, so an Unsubscribe that returned before dispatch cannot
// have its handler invoked afterwards.
func (b *Bus) dispatch(ev Event) {
	for _, sub := range b.reg.subscribers(reflect.TypeOf(ev)) {
		sub.run(ev)
	}
}

// Emit enqueues an event. It never blocks: if the buffer is full the event is
// dropped, the dropped counter incremented, and a debug log record emitted.
// It is the entry point used by the gumble bridge and by future emitters
// (ConnectError, VoiceActivity, LatencyStats). Emit transfers ownership: the
// bus may retain the event, so callers must not mutate slice or pointer
// fields of the event after Emit returns (the gumble bridge always copies
// fork data into fresh values before emitting).
func (b *Bus) Emit(ev Event) {
	select {
	case <-b.done:
		return
	default:
	}
	select {
	case b.events <- ev:
	default:
		n := b.dropped.Add(1)
		b.logger.Debug("bus: event dropped, buffer full",
			"event", fmt.Sprintf("%T", ev), "dropped_total", n)
	}
}

// Dropped reports how many events have been dropped due to a full buffer.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }

// State returns a deep-copy snapshot of the bus-maintained world state.
// Intended to be read from main-loop handlers.
func (b *Bus) State() State { return b.store.snapshot() }

// Close stops the pump goroutine and detaches the gumble bridge. It drains
// every event buffered at that point (they are applied to the store and
// dispatched via idleAdd like any other event); only events emitted
// concurrently with Close may race and be dropped. Emit after Close is a
// no-op. Close is idempotent.
func (b *Bus) Close() {
	b.closeOnce.Do(func() {
		close(b.done)
		if b.detachGumble != nil {
			b.detachGumble()
		}
		b.wg.Wait()
	})
}

// subscription is one registered handler. mu linearizes handler invocation
// against removal: dispatch runs the handler only while holding mu, and
// removal marks the subscription dead under mu. Hence once Unsubscribe
// returns, the handler is neither running nor invoked again — even when
// Unsubscribe is called from a non-main-loop goroutine mid-dispatch.
type subscription struct {
	id       uint64
	mu       sync.Mutex
	dead     bool
	dispatch func(Event)
}

func (s *subscription) run(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dead {
		s.dispatch(ev)
	}
}

func (s *subscription) kill() {
	s.mu.Lock()
	s.dead = true
	s.mu.Unlock()
}

// registry maps event concrete type to subscriber list. All operations are
// goroutine-safe; dispatch reads happen on the main loop, Subscribe and
// Unsubscribe may come from any thread (e.g. a context cancellation watcher).
type registry struct {
	mu     sync.RWMutex
	byType map[reflect.Type][]*subscription
	nextID uint64
}

func newRegistry() *registry {
	return &registry{byType: make(map[reflect.Type][]*subscription)}
}

func (r *registry) add(t reflect.Type, dispatch func(Event)) *subscription {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	sub := &subscription{id: r.nextID, dispatch: dispatch}
	r.byType[t] = append(r.byType[t], sub)
	return sub
}

func (r *registry) remove(t reflect.Type, id uint64) {
	r.mu.Lock()
	list := r.byType[t]
	for i, s := range list {
		if s.id == id {
			s.kill()
			r.byType[t] = append(list[:i:i], list[i+1:]...)
			break
		}
	}
	if len(r.byType[t]) == 0 {
		delete(r.byType, t)
	}
	r.mu.Unlock()
}

func (r *registry) subscribers(t reflect.Type) []*subscription {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*subscription, len(r.byType[t]))
	copy(out, r.byType[t])
	return out
}

func (r *registry) count(t reflect.Type) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byType[t])
}

// Subscribe registers handler for events of the concrete type T. Handlers run
// on the GLib main loop, in send order. The returned unsubscribe function is
// idempotent; call it on widget destroy. Once it returns, the handler will
// not be invoked again.
func Subscribe[T Event](b *Bus, handler func(T)) (unsubscribe func()) {
	t := reflect.TypeOf((*T)(nil)).Elem()
	sub := b.reg.add(t, func(ev Event) { handler(ev.(T)) })
	var once sync.Once
	return func() {
		once.Do(func() { b.reg.remove(t, sub.id) })
	}
}

// SubscribeCtx is Subscribe with automatic unsubscription when ctx is done —
// the context-per-widget lifecycle pattern: tie ctx to the widget's lifetime
// and cancellation tears the handler down without an explicit call. The
// returned function cancels the watcher and unsubscribes; it is idempotent.
func SubscribeCtx[T Event](ctx context.Context, b *Bus, handler func(T)) (unsubscribe func()) {
	inner := Subscribe(b, handler)
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			inner()
		case <-stopped:
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stopped)
			inner()
		})
	}
}

// SubscribeCount reports the number of live handlers registered for T.
// Primarily for leak checks in tests.
func SubscribeCount[T Event](b *Bus) int {
	return b.reg.count(reflect.TypeOf((*T)(nil)).Elem())
}
