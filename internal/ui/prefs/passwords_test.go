package prefs

import (
	"sync"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	"github.com/one337/murmel/internal/bus"
	"github.com/one337/murmel/internal/ui/secrets"
)

// fixedPasswords stores nothing; its State is state (set with set).
type fixedPasswords struct {
	mu    *sync.Mutex
	state *secrets.State
}

func newFixedPasswords(s secrets.State) fixedPasswords {
	return fixedPasswords{mu: &sync.Mutex{}, state: &s}
}

func (f fixedPasswords) set(s secrets.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	*f.state = s
}

func (f fixedPasswords) State() secrets.State {
	if f.state == nil {
		return secrets.InKeyring
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.state
}

func (fixedPasswords) Save(secrets.Key, string) error     { return nil }
func (fixedPasswords) Lookup(secrets.Key) (string, error) { return "", nil }
func (fixedPasswords) Delete(secrets.Key) error           { return nil }

func TestPasswordStorageText(t *testing.T) {
	seen := map[string]bool{}
	for _, tc := range []struct {
		state     secrets.State
		sandboxed bool
		subtitle  bool
	}{
		{secrets.Unknown, false, false},
		{secrets.InKeyring, false, false},
		{secrets.NoKeyring, false, true},
		{secrets.NoKeyring, true, true},
	} {
		title, subtitle := passwordStorageText(tc.state, tc.sandboxed)
		if title == "" || (subtitle != "") != tc.subtitle {
			t.Errorf("%v sandboxed %v: %q / %q", tc.state, tc.sandboxed, title, subtitle)
		}
		if seen[title+subtitle] {
			t.Errorf("%v sandboxed %v: text not distinct", tc.state, tc.sandboxed)
		}
		seen[title+subtitle] = true
	}
}

// The Passwords row says where passwords go and follows a change while the
// preferences are open.
func TestGTKPasswordsRowFollowsTheState(t *testing.T) {
	needGTK(t)
	b := bus.New(nil)
	pw := newFixedPasswords(secrets.InKeyring)
	var row *adw.ActionRow
	var dialog *adw.PreferencesDialog
	onMain(func() {
		dialog = adw.NewPreferencesDialog()
		_, row = passwordsGroup(pw, b, dialog)
	})
	title := func() string {
		var s string
		onMain(func() { s = row.Title() })
		return s
	}
	want, _ := passwordStorageText(secrets.InKeyring, false)
	if got := title(); got != want {
		t.Fatalf("title %q, want %q", got, want)
	}
	pw.set(secrets.NoKeyring)
	b.Emit(bus.PasswordStorageChanged{})
	want, _ = passwordStorageText(secrets.NoKeyring, false)
	for deadline := time.Now().Add(5 * time.Second); title() != want; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("title %q, want %q", title(), want)
		}
	}
	onMain(func() { dialog.ForceClose() })
}
