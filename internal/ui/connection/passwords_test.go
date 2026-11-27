package connection

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/certs"
	"github.com/one337/murmel/internal/settings"
	"github.com/one337/murmel/internal/ui"
	"github.com/one337/murmel/internal/ui/secrets"
)

// memStore keeps passwords in memory, for tests.
type memStore struct {
	mu sync.Mutex
	pw map[secrets.Key]string
}

func (m *memStore) Save(k secrets.Key, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pw == nil {
		m.pw = map[secrets.Key]string{}
	}
	m.pw[k] = password
	return nil
}

func (m *memStore) Lookup(k secrets.Key) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pw[k], nil
}

func (m *memStore) Delete(k secrets.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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
