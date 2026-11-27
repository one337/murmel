package connection

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/certs"
	"github.com/one337/murmel/internal/i18n"
	"github.com/one337/murmel/internal/settings"
	"github.com/one337/murmel/internal/ui"
	"github.com/one337/murmel/internal/ui/secrets"
)

// memStore keeps passwords in memory, for tests. Its State is state, or
// InKeyring when unset; err, when set, is what every call returns.
type memStore struct {
	mu      sync.Mutex
	pw      map[secrets.Key]string
	state   secrets.State
	err     error
	lookups int
}

func (m *memStore) State() secrets.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == secrets.Unknown {
		return secrets.InKeyring
	}
	return m.state
}

func (m *memStore) Save(k secrets.Key, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if m.pw == nil {
		m.pw = map[secrets.Key]string{}
	}
	m.pw[k] = password
	return nil
}

func (m *memStore) Lookup(k secrets.Key) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lookups++
	if m.err != nil {
		return "", m.err
	}
	return m.pw[k], nil
}

func (m *memStore) Delete(k secrets.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	delete(m.pw, k)
	return nil
}

// stored reports the password stored for k.
func (m *memStore) stored(k secrets.Key) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pw, ok := m.pw[k]
	return pw, ok
}

// eventually polls cond for up to five seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal(what)
}

var testEntry = settings.ServerEntry{Name: "a@one.localhost", Host: "one.localhost", Port: 64738, Username: "a"}

var testKey = secrets.Key{Host: "one.localhost", Port: 64738, Username: "a"}

// Saving a connection puts its password into the controller's store; an
// empty password removes the stored one.
func TestSaveServerUsesTheStore(t *testing.T) {
	m := &memStore{}
	c := &Controller{settings: settings.NewMemory(), passwords: m}
	c.saveServer(testEntry, "secret")
	eventually(t, "password not stored", func() bool { pw, _ := m.stored(testKey); return pw == "secret" })
	c.saveServer(testEntry, "")
	eventually(t, "password not removed", func() bool { _, ok := m.stored(testKey); return !ok })
}

// storedPassword is only ever in the memStore of newPasswordTestController,
// so finding it shows that the form asked that store.
const storedPassword = "only in the test store"

// newPasswordTestController returns a controller with testEntry saved and
// its password storedPassword in a memStore.
func newPasswordTestController(t *testing.T) (*Controller, *memStore) {
	t.Helper()
	mgr, err := certs.NewManager(certs.WithDataDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	st := settings.NewMemory()
	st.SetServers([]settings.ServerEntry{testEntry})
	m := &memStore{}
	if err := m.Save(testKey, storedPassword); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	win := testWindow(t)
	return &Controller{win: win, appBus: bus.New(nil), settings: st, certs: mgr, passwords: m, appCtx: ctx}, m
}

// Choosing a saved connection fills the form with the password from the
// controller's store.
func TestGTKSavedConnectionFillsStoredPassword(t *testing.T) {
	needGTK(t)
	c, _ := newPasswordTestController(t)
	var f *connectForm
	onMain(func() {
		dialog, form, _ := c.newConnectDialog(ui.ConnectRequest{})
		dialog.Present(c.win)
		f = form
		f.fill(testEntry)
	})
	eventually(t, "stored password not filled in", func() bool {
		var text string
		onMain(func() { text = f.password.Text() })
		return text == storedPassword
	})
}

// Forgetting a saved connection removes its password from the store.
func TestGTKForgetRemovesStoredPassword(t *testing.T) {
	needGTK(t)
	c, m := newPasswordTestController(t)
	onMain(func() {
		dialog, _, saved := c.newConnectDialog(ui.ConnectRequest{})
		dialog.Present(c.win)
		saved.forget(saved.rows[0])
	})
	eventually(t, "password not removed", func() bool { _, ok := m.stored(testKey); return !ok })
}

// setFor sets the store's state and the error its calls return.
func (m *memStore) setFor(state secrets.State, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state, m.err = state, err
}

func (m *memStore) lookupCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lookups
}

var (
	errRefused = fmt.Errorf("saving: %w", &secrets.Error{Kind: secrets.Refused})
	errGone    = &secrets.Error{Kind: secrets.Gone}
	errOther   = &secrets.Error{Kind: secrets.Other}
)

func TestPasswordProblem(t *testing.T) {
	if passwordProblem(nil, false) != "" || passwordProblem(secrets.ErrNoKeyring, true) != "" {
		t.Error("success or no keyring is a problem")
	}
	seen := map[string]bool{}
	for _, err := range []error{errRefused, errGone, errOther} {
		for _, removing := range []bool{false, true} {
			text := passwordProblem(err, removing)
			if text == "" || seen[text] {
				t.Errorf("%v removing %v: %q (empty or seen before)", err, removing, text)
			}
			seen[text] = true
		}
	}
}

// A password that cannot be saved after connecting is reported on the
// application bus (the window shows it); no keyring is not reported, nor
// a failed removal when the connection is saved without a password.
func TestGTKSaveServerReportsProblems(t *testing.T) {
	needGTK(t) // the bus delivers on the GLib main loop
	b := bus.New(nil)
	t.Cleanup(b.Close)
	got := make(chan string, 4)
	bus.Subscribe(b, func(ev bus.PasswordProblem) { got <- ev.Text })
	m := &memStore{}
	c := &Controller{settings: settings.NewMemory(), passwords: m, appBus: b}

	m.setFor(secrets.InKeyring, errRefused)
	c.saveServer(testEntry, "secret")
	select {
	case text := <-got:
		if text != passwordProblem(errRefused, false) {
			t.Errorf("reported %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not reported")
	}

	// Not reported: no keyring, and tidying up without a password.
	none := &memStore{state: secrets.NoKeyring, err: secrets.ErrNoKeyring}
	(&Controller{settings: settings.NewMemory(), passwords: none, appBus: b}).saveServer(testEntry, "secret")
	locked := &memStore{err: errRefused}
	(&Controller{settings: settings.NewMemory(), passwords: locked, appBus: b}).saveServer(testEntry, "")
	select {
	case text := <-got:
		t.Errorf("reported %q", text)
	case <-time.After(300 * time.Millisecond):
	}
}

// Without a keyring the prompt cannot remember the password and says why.
func TestGTKRememberRowWithoutKeyring(t *testing.T) {
	needGTK(t)
	onMain(func() {
		on := rememberRow(secrets.InKeyring)
		if !on.Active() || !on.Sensitive() {
			t.Error("with a keyring: not on, or disabled")
		}
		for _, s := range []secrets.State{secrets.NoKeyring} {
			off := rememberRow(s)
			if off.Active() || off.Sensitive() || off.Subtitle() == on.Subtitle() {
				t.Errorf("%v: active %v, sensitive %v, subtitle %q", s, off.Active(), off.Sensitive(), off.Subtitle())
			}
		}
	})
}

// The form says when the password will not be saved with the connection,
// also when that turns out while it is open, and then looks nothing up.
func TestGTKFormWithoutKeyring(t *testing.T) {
	needGTK(t)
	c, m := newPasswordTestController(t)
	var f *connectForm
	var withKeyring string
	onMain(func() {
		dialog, form, _ := c.newConnectDialog(ui.ConnectRequest{Host: "one.localhost", Username: "a"})
		dialog.Present(c.win)
		f = form
		withKeyring = f.saveRow.Subtitle()
	})
	m.setFor(secrets.NoKeyring, secrets.ErrNoKeyring)
	c.appBus.Emit(bus.PasswordStorageChanged{})
	want := fmt.Sprintf(i18n.Tr("Saved as %s, without the password"), "a@one.localhost")
	eventually(t, "subtitle not updated", func() bool {
		var sub string
		onMain(func() { sub = f.saveRow.Subtitle() })
		return sub == want && sub != withKeyring
	})
	before := m.lookupCount()
	onMain(func() { f.fill(testEntry) })
	time.Sleep(200 * time.Millisecond)
	onMain(func() {
		if !f.pwField.known() || f.password.Text() != "" {
			t.Error("the field waits for a lookup")
		}
	})
	if m.lookupCount() != before {
		t.Error("looked up without a keyring")
	}
}

// A saved password that cannot be read (locked keyring) is named in the
// field's title until the user types.
func TestGTKUnreadablePasswordIsShown(t *testing.T) {
	needGTK(t)
	c, m := newPasswordTestController(t)
	m.setFor(secrets.InKeyring, errRefused)
	var f *connectForm
	var normal string
	onMain(func() {
		dialog, form, _ := c.newConnectDialog(ui.ConnectRequest{})
		dialog.Present(c.win)
		f = form
		normal = f.password.Title()
		f.fill(testEntry)
	})
	unreadable := i18n.Tr("Couldn't read password")
	eventually(t, "unreadable password not shown", func() bool {
		var title string
		onMain(func() { title = f.password.Title() })
		return title == unreadable
	})
	onMain(func() {
		if f.pwField.known() {
			t.Error("an unread password counts as known")
		}
		f.password.SetText("typed")
		if f.password.Title() != normal {
			t.Errorf("title %q after typing", f.password.Title())
		}
	})
}

// A password that cannot be removed when a saved connection is forgotten
// is reported in the connect dialog itself.
func TestGTKForgetProblemShowsInTheDialog(t *testing.T) {
	needGTK(t)
	c, m := newPasswordTestController(t)
	m.setFor(secrets.InKeyring, errRefused)
	var dialog *adw.Dialog
	onMain(func() {
		d, _, saved := c.newConnectDialog(ui.ConnectRequest{})
		d.Present(c.win)
		dialog = d
		saved.forget(saved.rows[0])
	})
	want := passwordProblem(errRefused, true)
	eventually(t, "no toast in the dialog", func() bool {
		found := false
		onMain(func() { found = hasLabel(dialog.Child(), want) })
		return found
	})
}

// hasLabel reports whether a label below w shows text.
func hasLabel(w gtk.Widgetter, text string) bool {
	if w == nil {
		return false
	}
	base := gtk.BaseWidget(w)
	if l, ok := w.(*gtk.Label); ok && l.Label() == text {
		return true
	}
	for child := base.FirstChild(); child != nil; child = gtk.BaseWidget(child).NextSibling() {
		if hasLabel(child, text) {
			return true
		}
	}
	return false
}
