package chat

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"io.github/murmel/internal/bus"
	"io.github/murmel/internal/protocol/gumble"
)

// sentRecorder records the messages handed to the server.
type sentRecorder struct {
	mu   sync.Mutex
	sent []string
}

func (r *sentRecorder) SendText(_, _ uint32, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, message)
	return nil
}

func (r *sentRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

func perms(p gumble.Permission) *int {
	v := int(p)
	return &v
}

const (
	textAllowed = gumble.PermissionEnter | gumble.PermissionSpeak | gumble.PermissionTextMessage
	textRefused = gumble.PermissionEnter | gumble.PermissionSpeak
)

// lockWindow shows a chat view (width wide) connected with oneself in
// channel 4 and anna in channel 5; the permissions of 4 and 5 are given
// (nil = not known yet).
func lockWindow(t *testing.T, width int, mine, annas *int) (*View, *bus.Bus, *sentRecorder, *gtk.Window) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := bus.New(nil)
	rec := &sentRecorder{}
	var v *View
	var win *gtk.Window
	onMain(func() {
		v = NewView(ctx, b, rec)
		win = gtk.NewWindow()
		win.SetDefaultSize(width, 500)
		win.SetChild(v.Root())
		win.Present()
	})
	t.Cleanup(func() { onMain(func() { win.Destroy() }) })
	b.Emit(bus.Connected{SelfSession: 1,
		Channels: []bus.ChannelInfo{
			{ID: 0, Name: "Root", Permissions: perms(textAllowed)},
			{ID: 4, Name: "Quiet", Permissions: mine},
			{ID: 5, Name: "Silent", Permissions: annas},
		},
		Users: []bus.UserInfo{{Session: 1, Name: "me", Channel: 4}, {Session: 7, Name: "anna", Channel: 5}},
	})
	waitUntil(t, "the connection", func() bool { return v.bus.State().SelfSession == 1 })
	return v, b, rec, win
}

// lockState reads what a conversation's field shows: the line above it
// ("" when hidden) and whether one can type.
func lockState(c *conversation) (hint string, editable bool) {
	if c.composer.hint.Visible() {
		hint = c.composer.hint.Text()
	}
	return hint, c.composer.view.Editable()
}

func waitLock(t *testing.T, what string, c func() *conversation, wantHint string) {
	t.Helper()
	var hint string
	var editable bool
	ok := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		onMain(func() { hint, editable = lockState(c()) })
		if hint == wantHint && editable == (wantHint == "") {
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("%s: line %q, editable %v; want line %q", what, hint, editable, wantHint)
	}
	// The line is the field's description (its text is checked with a
	// screen reader probe: GTK offers no way to read it here).
	var described bool
	onMain(func() {
		described = gtk.TestAccessibleHasProperty(c().composer.view, gtk.AccessiblePropertyDescription)
	})
	if described != (wantHint != "") {
		t.Errorf("%s: the field has a description: %v, want %v", what, described, wantHint != "")
	}
}

const channelLocked = "You may not send messages in this channel."

// Where the server would refuse a channel message, the field is locked
// with the reason above it, and Enter sends nothing.
func TestGTKChannelChatLockedWithoutTextPermission(t *testing.T) {
	needGTK(t)
	v, _, rec, _ := lockWindow(t, 600, perms(textRefused), perms(textAllowed))
	channel := func() *conversation { return v.channel }
	waitLock(t, "channel without Text Message", channel, channelLocked)
	onMain(func() {
		v.channel.composer.buffer.SetText("hello")
		v.channel.composer.Submit()
	})
	time.Sleep(100 * time.Millisecond)
	if n := rec.count(); n != 0 {
		t.Errorf("%d messages sent from a locked field", n)
	}
	var text string
	onMain(func() {
		text = v.channel.composer.buffer.Text(v.channel.composer.buffer.StartIter(), v.channel.composer.buffer.EndIter(), false)
	})
	if text != "hello" {
		t.Errorf("the typed text is %q, want it kept", text)
	}
}

// Write alone also allows writing; the lock follows permission updates and
// the local user's moves.
func TestGTKChannelChatLockFollowsPermissionsAndMoves(t *testing.T) {
	needGTK(t)
	v, b, rec, _ := lockWindow(t, 600, perms(textAllowed), perms(textRefused))
	channel := func() *conversation { return v.channel }
	waitLock(t, "allowed", channel, "")

	b.Emit(bus.ChannelUpdated{Channel: bus.ChannelInfo{ID: 4, Name: "Quiet", Permissions: perms(textRefused)},
		Changed: bus.ChannelChangePermission})
	waitLock(t, "permission taken away", channel, channelLocked)

	b.Emit(bus.ChannelUpdated{Channel: bus.ChannelInfo{ID: 4, Name: "Quiet", Permissions: perms(gumble.PermissionWrite)},
		Changed: bus.ChannelChangePermission})
	waitLock(t, "Write only", channel, "")

	b.Emit(bus.UserUpdated{User: bus.UserInfo{Session: 1, Name: "me", Channel: 5}, Changed: bus.UserChangeChannel})
	waitLock(t, "moved into a channel without Text Message", channel, channelLocked)

	b.Emit(bus.UserUpdated{User: bus.UserInfo{Session: 1, Name: "me", Channel: 0}, Changed: bus.UserChangeChannel})
	waitLock(t, "moved back out", channel, "")
	onMain(func() {
		v.channel.composer.buffer.SetText("hello")
		v.channel.composer.Submit()
	})
	if n := rec.count(); n != 1 {
		t.Errorf("%d messages sent from the unlocked field, want 1", n)
	}
}

// Unknown permissions leave the field open and are asked for once; the
// answer decides.
func TestGTKChannelChatAsksForUnknownPermissions(t *testing.T) {
	needGTK(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var asked []uint32
	var v *View
	var b *bus.Bus
	b = bus.New(nil)
	bus.SubscribeCtx(ctx, b, func(ev bus.ChannelPermissionRequested) {
		mu.Lock()
		asked = append(asked, ev.ChannelID)
		mu.Unlock()
	})
	var win *gtk.Window
	onMain(func() {
		v = NewView(ctx, b, &sentRecorder{})
		win = gtk.NewWindow()
		win.SetChild(v.Root())
		win.Present()
	})
	defer onMain(func() { win.Destroy() })
	b.Emit(bus.Connected{SelfSession: 1,
		Channels: []bus.ChannelInfo{{ID: 0, Name: "Root"}, {ID: 4, Name: "Quiet"}},
		Users:    []bus.UserInfo{{Session: 1, Name: "me", Channel: 4}},
	})
	channel := func() *conversation { return v.channel }
	waitUntil(t, "a question for the permissions", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(asked) > 0
	})
	waitLock(t, "unknown", channel, "")
	b.Emit(bus.UserUpdated{User: bus.UserInfo{Session: 1, Name: "me", Channel: 4, SelfMuted: true}, Changed: bus.UserChangeAudio})
	b.Emit(bus.ChannelUpdated{Channel: bus.ChannelInfo{ID: 4, Name: "Quiet", Permissions: perms(textRefused)},
		Changed: bus.ChannelChangePermission})
	waitLock(t, "answer: refused", channel, channelLocked)
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 1 || asked[0] != 4 {
		t.Errorf("asked for %v, want once for channel 4", asked)
	}
}

// A private message is refused where its recipient is: the field follows
// the recipient's channel.
func TestGTKPrivateChatLockFollowsTheRecipient(t *testing.T) {
	needGTK(t)
	v, b, _, _ := lockWindow(t, 600, perms(textAllowed), perms(textRefused))
	var dm *conversation
	onMain(func() {
		key := keyForUser(bus.UserInfo{Session: 7, Name: "anna"})
		v.openDM(key, "anna")
		dm = v.dms[key]
	})
	conv := func() *conversation { return dm }
	waitLock(t, "recipient in a channel without Text Message", conv, "anna is in a channel where you may not write.")
	waitLock(t, "the channel chat meanwhile", func() *conversation { return v.channel }, "")

	b.Emit(bus.UserUpdated{User: bus.UserInfo{Session: 7, Name: "anna", Channel: 4}, Changed: bus.UserChangeChannel})
	waitLock(t, "recipient moved into a channel with Text Message", conv, "")
}

// Locking the focused field is announced once; an unfocused field is not.
func TestGTKLockAnnouncedOnlyWithFocus(t *testing.T) {
	needGTK(t)
	v, _, _, _ := lockWindow(t, 600, perms(textAllowed), perms(textAllowed))
	waitLock(t, "allowed", func() *conversation { return v.channel }, "")
	var unfocused, focused, again bool
	onMain(func() {
		unfocused = v.lock(v.channel, channelLocked)
		v.lock(v.channel, "")
		v.channel.composer.view.GrabFocus()
	})
	waitUntil(t, "the field's focus", func() bool { return v.channel.composer.view.HasFocus() })
	onMain(func() {
		focused = v.lock(v.channel, channelLocked)
		again = v.lock(v.channel, channelLocked)
	})
	if unfocused || !focused || again {
		t.Errorf("announced: unfocused %v, focused %v, unchanged %v; want false, true, false", unfocused, focused, again)
	}
}

// In a narrow window the line wraps and stays within the window.
func TestGTKLockLineWrapsInANarrowWindow(t *testing.T) {
	needGTK(t)
	v, b, _, win := lockWindow(t, 360, perms(textAllowed), perms(textRefused))
	long := strings.Repeat("Annabelle", 3)
	b.Emit(bus.UserUpdated{User: bus.UserInfo{Session: 7, Name: long, Channel: 5}, Changed: bus.UserChangeName})
	var dm *conversation
	onMain(func() {
		key := keyForUser(bus.UserInfo{Session: 7, Name: long})
		v.openDM(key, long)
		dm = v.dms[key]
	})
	want := long + " is in a channel where you may not write."
	waitLock(t, "narrow", func() *conversation { return dm }, want)
	var lines, hintW, winW int
	waitUntil(t, "the line laid out", func() bool {
		lines = dm.composer.hint.Layout().LineCount()
		hintW, winW = dm.composer.hint.Width(), win.Width()
		return hintW > 0
	})
	if winW > 360 {
		t.Fatalf("window %d px wide, want 360", winW)
	}
	t.Logf("line: %d lines, %d px in a %d px window", lines, hintW, winW)
	if lines < 2 || hintW > winW {
		t.Errorf("line: %d lines, %d px in a %d px window; want wrapped and within", lines, hintW, winW)
	}
	if text := dm.composer.hint.Layout().Text(); text != want {
		t.Errorf("line text %q, want %q", text, want)
	}
}

// A recipient who goes offline locks the field with the same line; when
// they come back it opens again.
func TestGTKPrivateChatLockedWhileTheRecipientIsOffline(t *testing.T) {
	needGTK(t)
	v, b, _, _ := lockWindow(t, 600, perms(textAllowed), perms(textAllowed))
	var dm *conversation
	onMain(func() {
		key := keyForUser(bus.UserInfo{Session: 7, Name: "anna"})
		v.openDM(key, "anna")
		dm = v.dms[key]
	})
	conv := func() *conversation { return dm }
	waitLock(t, "recipient online", conv, "")

	b.Emit(bus.UserRemoved{Session: 7})
	waitLock(t, "recipient offline", conv, "anna is offline.")

	b.Emit(bus.UserAdded{User: bus.UserInfo{Session: 9, Name: "anna", Channel: 5}})
	waitLock(t, "recipient back", conv, "")
}
