package bus

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// syncIdleAdd runs dispatch inline on the pump goroutine, so tests can
// synchronize by waiting for the handler of the event they emitted.
func syncIdleAdd(f func() bool) { f() }

func testBus(idleAdd func(f func() bool)) *Bus {
	return newBus(idleAdd, slog.Default())
}

// signal returns a bus plus a channel carrying every dispatched event of
// type T. Waiting on the channel synchronizes the test with the pump (FIFO:
// when event N is observed, all earlier events are already folded).
func signal[T Event](t *testing.T, b *Bus) (received chan T, unsubscribe func()) {
	t.Helper()
	ch := make(chan T, bufferSize)
	unsub := Subscribe(b, func(ev T) { ch <- ev })
	return ch, unsub
}

func waitEvent[T Event](t *testing.T, ch chan T, what string) T {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatalf("event %s not dispatched within timeout", what)
		return *new(T)
	}
}

// waitCond polls until cond holds; the pump consumes the event channel
// asynchronously, so assertions right after Emit must go through this.
func waitCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not reached within timeout: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStateFoldAndSnapshotDeepCopy(t *testing.T) {
	b := testBus(syncIdleAdd)
	defer b.Close()

	chConn, unsubConn := signal[Connected](t, b)
	defer unsubConn()
	chCh, unsubCh := signal[ChannelUpdated](t, b)
	defer unsubCh()
	chUsr, unsubUsr := signal[UserRemoved](t, b)
	defer unsubUsr()
	chDisc, unsubDisc := signal[Disconnected](t, b)
	defer unsubDisc()
	chErr, unsubErr := signal[ConnectError](t, b)
	defer unsubErr()

	br := 72000
	b.Emit(Connected{
		SelfSession: 7,
		Welcome:     "hi",
		MaxBitrate:  &br,
		Channels: []ChannelInfo{
			{ID: 1, Name: "root"},
			{ID: 2, ParentID: 1, Name: "lounge", Links: []uint32{3, 4}},
		},
		Users: []UserInfo{{Session: 7, Channel: 1, Name: "me", SelfMuted: true}},
	})
	waitEvent[Connected](t, chConn, "Connected")

	s1 := b.State()
	if s1.Connection != StateConnected || s1.SelfSession != 7 || s1.LastError != "" {
		t.Fatalf("state after connect = %+v", s1)
	}
	if len(s1.Channels) != 2 || len(s1.Users) != 1 {
		t.Fatalf("seeded tree missing: %d channels, %d users", len(s1.Channels), len(s1.Users))
	}
	lounge := s1.Channels[2]
	if lounge.ParentID != 1 || len(lounge.Links) != 2 || lounge.Links[0] != 3 || lounge.Links[1] != 4 {
		t.Fatalf("channel snapshot wrong: %+v", lounge)
	}
	if u := s1.Users[7]; u.Name != "me" || !u.SelfMuted || u.Channel != 1 {
		t.Fatalf("user snapshot wrong: %+v", u)
	}

	s1.Channels[2].Links[0] = 99
	delete(s1.Channels, 1)
	delete(s1.Users, 7)

	s2 := b.State()
	if s2.Channels[2].Links[0] != 3 || len(s2.Channels) != 2 {
		t.Fatalf("snapshot aliases store: %+v", s2.Channels[2])
	}
	if _, ok := s2.Users[7]; !ok {
		t.Fatal("deleting from snapshot must not touch the store")
	}

	b.Emit(UserRemoved{Session: 7})
	waitEvent[UserRemoved](t, chUsr, "UserRemoved")
	if _, ok := b.State().Users[7]; ok {
		t.Fatal("removed user still in state")
	}

	b.Emit(Disconnected{Reason: DisconnectKicked, Text: "bye"})
	waitEvent[Disconnected](t, chDisc, "Disconnected")
	s3 := b.State()
	if s3.Connection != StateDisconnected || len(s3.Channels) != 0 || len(s3.Users) != 0 {
		t.Fatalf("disconnect must clear the world state: %+v", s3)
	}

	b.Emit(ConnectError{Reason: RejectServerFull, Text: "full"})
	waitEvent[ConnectError](t, chErr, "ConnectError")
	if s4 := b.State(); s4.LastError != "full" {
		t.Fatalf("LastError = %q, want %q", s4.LastError, "full")
	}

	b.Emit(ChannelUpdated{Channel: ChannelInfo{ID: 9, Name: "x"}})
	waitEvent[ChannelUpdated](t, chCh, "ChannelUpdated")
	if s5 := b.State(); len(s5.Channels) != 1 {
		t.Fatalf("channel update after disconnect folded: %+v", s5.Channels)
	}
}

// TestEventCopyNoAliasWithStore pins the store's side of the copy contract:
// folding an event into the store never aliases the emitter's payload, and
// snapshots never alias the store. (Zero-copy handler delivery is intended;
// the bridge is what copies fork data, per the bridge tests.)
func TestEventCopyNoAliasWithStore(t *testing.T) {
	b := testBus(syncIdleAdd)
	defer b.Close()

	ch, unsub := signal[ChannelAdded](t, b)
	defer unsub()

	links := []uint32{5, 6}
	b.Emit(ChannelAdded{Channel: ChannelInfo{ID: 3, Name: "ch", Links: links}})
	waitEvent[ChannelAdded](t, ch, "ChannelAdded")

	links[0] = 99
	if st := b.State(); st.Channels[3].Links[0] != 5 {
		t.Fatalf("store aliases emitter payload: %+v", st.Channels[3])
	}
	st := b.State()
	st.Channels[3].Links[1] = 99
	if b.State().Channels[3].Links[1] != 6 {
		t.Fatal("snapshot aliases store Links slice")
	}
}

func TestBackpressureDropsAndCounts(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var dispatched atomic.Int64
	idleAdd := func(f func() bool) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		dispatched.Add(1)
		f()
	}
	b := testBus(idleAdd)
	defer b.Close()

	b.Emit(Connected{})
	sent := 1
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pump never reached idleAdd")
	}
	for i := 0; i < bufferSize; i++ {
		b.Emit(UserUpdated{User: UserInfo{Session: uint32(i)}})
		sent++
	}
	if n := b.Dropped(); n != 0 {
		t.Fatalf("buffer of %d must absorb %d events, dropped = %d", bufferSize, bufferSize, n)
	}
	for i := 0; i < 7; i++ {
		b.Emit(UserUpdated{})
		sent++
	}
	if n := b.Dropped(); n != 7 {
		t.Fatalf("Dropped() = %d, want 7", n)
	}

	close(release)
	b.Close()
	if n := dispatched.Load(); n != int64(sent-7) {
		t.Fatalf("delivered %d, want %d (delivered+dropped must equal sent)", n, sent-7)
	}
}

func TestCloseDrainsPendingEvents(t *testing.T) {
	var mu sync.Mutex
	var dispatched []func() bool
	idleAdd := func(f func() bool) {
		mu.Lock()
		dispatched = append(dispatched, f)
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	b := testBus(idleAdd)

	var handled atomic.Int64
	unsub := Subscribe(b, func(UserAdded) { handled.Add(1) })
	defer unsub()

	const n = 12
	for i := 0; i < n; i++ {
		b.Emit(UserAdded{})
	}
	b.Close()
	b.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(dispatched) != n {
		t.Fatalf("Close drained %d of %d buffered events", len(dispatched), n)
	}
	for _, f := range dispatched {
		f()
	}
	if handled.Load() != n {
		t.Fatalf("handlers ran %d times, want %d", handled.Load(), n)
	}

	b.Emit(UserAdded{})
	if d := b.Dropped(); d != 0 {
		t.Fatalf("Emit after Close must be a no-op, dropped = %d", d)
	}
}

func TestSubscribeUnsubscribeStrict(t *testing.T) {
	b := testBus(syncIdleAdd)
	defer b.Close()

	var calls atomic.Int64
	unsub := Subscribe(b, func(TextMessage) { calls.Add(1) })
	if SubscribeCount[TextMessage](b) != 1 {
		t.Fatal("SubscribeCount after subscribe = 0")
	}
	b.Emit(TextMessage{Message: "a"})
	waitCond(t, "first delivery", func() bool { return calls.Load() == 1 })

	unsub()
	unsub()
	if SubscribeCount[TextMessage](b) != 0 {
		t.Fatal("unsubscribe must remove the subscription")
	}
	b.Emit(TextMessage{Message: "b"})
	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("handler called after unsubscribe: %d calls", calls.Load())
	}
}

// TestUnsubscribeLinearizedWithDispatch proves the strict guarantee: while a
// handler is mid-dispatch, Unsubscribe blocks until the handler returns, and
// afterwards no further invocation happens — even though dispatch runs on a
// different goroutine than Unsubscribe.
func TestUnsubscribeLinearizedWithDispatch(t *testing.T) {
	handlerEntered := make(chan struct{}, 1)
	handlerRelease := make(chan struct{})
	var calls atomic.Int64

	var mu sync.Mutex
	var pending []func() bool
	b := newBus(func(f func() bool) {
		mu.Lock()
		pending = append(pending, f)
		mu.Unlock()
	}, slog.Default())
	defer b.Close()

	unsub := Subscribe(b, func(TextMessage) {
		handlerEntered <- struct{}{}
		<-handlerRelease
		calls.Add(1)
	})

	b.Emit(TextMessage{})
	var f func() bool
	waitCond(t, "pump schedules dispatch", func() bool {
		mu.Lock()
		defer mu.Unlock()
		if len(pending) == 0 {
			return false
		}
		f = pending[0]
		return true
	})

	go f()
	<-handlerEntered

	unsubDone := make(chan struct{})
	go func() { unsub(); close(unsubDone) }()
	select {
	case <-unsubDone:
		t.Fatal("Unsubscribe returned while handler still running")
	case <-time.After(50 * time.Millisecond):
	}

	close(handlerRelease)
	select {
	case <-unsubDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Unsubscribe never returned after handler finished")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestSubscribeCtxCancel(t *testing.T) {
	b := testBus(syncIdleAdd)
	defer b.Close()

	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int64
	unsub := SubscribeCtx(ctx, b, func(VoiceActivity) { calls.Add(1) })
	_ = unsub

	b.Emit(VoiceActivity{Session: 1, Active: true})
	waitCond(t, "delivery before cancellation", func() bool { return calls.Load() == 1 })

	cancel()
	waitCond(t, "cancellation unsubscribes", func() bool { return SubscribeCount[VoiceActivity](b) == 0 })
	b.Emit(VoiceActivity{Session: 2})
	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("handler called after ctx cancellation")
	}
	unsub()
	unsub()
}

func TestMultipleSubscribersAndTypeIsolation(t *testing.T) {
	b := testBus(syncIdleAdd)
	defer b.Close()

	var msgs, joins atomic.Int64
	unsubA := Subscribe(b, func(TextMessage) { msgs.Add(1) })
	unsubB := Subscribe(b, func(TextMessage) { msgs.Add(1) })
	unsubC := Subscribe(b, func(UserAdded) { joins.Add(1) })
	defer func() { unsubA(); unsubB(); unsubC() }()

	b.Emit(TextMessage{})
	waitCond(t, "both subscribers fire", func() bool { return msgs.Load() == 2 })
	if joins.Load() != 0 {
		t.Fatal("handler received event of another type")
	}
	if SubscribeCount[TextMessage](b) != 2 || SubscribeCount[UserAdded](b) != 1 {
		t.Fatal("SubscribeCount mismatch")
	}
}

func TestConcurrentSubscribeEmitRace(t *testing.T) {
	b := testBus(syncIdleAdd)
	defer b.Close()

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				unsub := Subscribe(b, func(TextMessage) {})
				b.Emit(TextMessage{})
				unsub()
			}
		}()
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				b.Emit(LatencyStats{RTT: time.Duration(i)})
			}
		}()
	}
	wg.Wait()

	deadline := time.Now().Add(5 * time.Second)
	for SubscribeCount[TextMessage](b) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if SubscribeCount[TextMessage](b) != 0 {
		t.Fatal("subscriptions leaked")
	}
}

func TestNewNilClientSmoke(t *testing.T) {
	b := New(nil)
	b.Emit(Connected{})
	b.Close()
}
