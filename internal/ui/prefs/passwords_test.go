package prefs

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

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

func (fixedPasswords) File() secrets.FileInfo             { return secrets.FileInfo{} }
func (fixedPasswords) UseFile() error                     { return nil }
func (fixedPasswords) StopUsingFile() error               { return nil }
func (fixedPasswords) MoveToKeyring() error               { return nil }
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
		{secrets.InFile, false, false},
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
		_, rows := passwordsGroup(pw, b, dialog)
		row = rows.status
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

// memKeyring is a keyring in memory.
type memKeyring struct {
	mu sync.Mutex
	pw map[secrets.Key]string
}

func (m *memKeyring) Save(k secrets.Key, pw string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pw[k] = pw
	return nil
}

func (m *memKeyring) Lookup(k secrets.Key) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pw[k], nil
}

func (m *memKeyring) Delete(k secrets.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pw, k)
	return nil
}

// answerConfirm makes every confirmation answer ok and records headings.
func answerConfirm(t *testing.T, ok bool) func() []string {
	var mu sync.Mutex
	var asked []string
	real := confirm
	t.Cleanup(func() { confirm = real })
	confirm = func(_ gtk.Widgetter, heading, _, _ string, _ adw.ResponseAppearance, answer func(bool)) {
		mu.Lock()
		asked = append(asked, heading)
		mu.Unlock()
		answer(ok)
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
}

func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var ok bool
		onMain(func() { ok = cond() })
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
	}
}

func passwordsWithFile(t *testing.T, s secrets.State) (*secrets.Manager, *secrets.File, *bus.Bus) {
	b := bus.New(nil)
	f := secrets.NewFile(filepath.Join(t.TempDir(), "passwords.json"))
	m := secrets.NewManager(&memKeyring{pw: map[secrets.Key]string{}}, f,
		func() secrets.State { return s }, func() { b.Emit(bus.PasswordStorageChanged{}) })
	for deadline := time.Now().Add(5 * time.Second); m.State() == secrets.Unknown; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no detection")
		}
	}
	return m, f, b
}

// Without a keyring the switch turns the file on after a confirmation and
// off after another; the status row names the file.
func TestGTKPasswordFileSwitch(t *testing.T) {
	needGTK(t)
	m, f, b := passwordsWithFile(t, secrets.NoKeyring)
	asked := answerConfirm(t, true)
	var rows *passwordRows
	var dialog *adw.PreferencesDialog
	onMain(func() {
		dialog = adw.NewPreferencesDialog()
		_, rows = passwordsGroup(m, b, dialog)
	})
	onMain(func() {
		if !rows.file.Visible() || rows.file.Active() || rows.move.Visible() {
			t.Errorf("without a file: switch visible %v active %v, move visible %v",
				rows.file.Visible(), rows.file.Active(), rows.move.Visible())
		}
		rows.file.SetActive(true)
	})
	inFile, _ := passwordStorageText(secrets.InFile, false)
	until(t, "file not turned on", func() bool { return f.Exists() && rows.status.Title() == inFile })
	onMain(func() {
		if rows.status.Subtitle() != f.Path() || !rows.file.Active() {
			t.Errorf("status subtitle %q, switch %v", rows.status.Subtitle(), rows.file.Active())
		}
		rows.file.SetActive(false)
	})
	until(t, "file not turned off", func() bool { return !f.Exists() && !rows.file.Active() })
	if got := asked(); len(got) != 2 {
		t.Errorf("asked %v", got)
	}
	onMain(func() { dialog.ForceClose() })
}

// Cancelling the confirmation leaves the switch and the file as they were.
func TestGTKPasswordFileSwitchCancelled(t *testing.T) {
	needGTK(t)
	m, f, b := passwordsWithFile(t, secrets.NoKeyring)
	answerConfirm(t, false)
	var rows *passwordRows
	var dialog *adw.PreferencesDialog
	onMain(func() {
		dialog = adw.NewPreferencesDialog()
		_, rows = passwordsGroup(m, b, dialog)
		rows.file.SetActive(true)
	})
	until(t, "switch stays on", func() bool { return !rows.file.Active() })
	if f.Exists() {
		t.Error("file created after cancelling")
	}
	onMain(func() { dialog.ForceClose() })
}

// With a keyring and passwords left in the file, the Move row moves them
// and disappears; the switch is not offered.
func TestGTKPasswordMoveRow(t *testing.T) {
	needGTK(t)
	m, f, b := passwordsWithFile(t, secrets.InKeyring)
	if err := f.Create(); err != nil {
		t.Fatal(err)
	}
	k := secrets.Key{Host: "example.invalid", Port: 1, Username: "a"}
	if err := f.Save(k, "pw"); err != nil {
		t.Fatal(err)
	}
	asked := answerConfirm(t, true)
	var rows *passwordRows
	var dialog *adw.PreferencesDialog
	onMain(func() {
		dialog = adw.NewPreferencesDialog()
		_, rows = passwordsGroup(m, b, dialog)
		if rows.file.Visible() || !rows.move.Visible() || !rows.moveButton.Sensitive() {
			t.Errorf("switch visible %v, move visible %v sensitive %v",
				rows.file.Visible(), rows.move.Visible(), rows.moveButton.Sensitive())
		}
		coreglib.BaseObject(rows.moveButton).Emit("clicked")
	})
	until(t, "move row still shown", func() bool { return !rows.move.Visible() })
	if f.Exists() {
		t.Error("file not deleted")
	}
	if pw, _ := m.Lookup(k); pw != "pw" {
		t.Errorf("keyring has %q", pw)
	}
	if got := asked(); len(got) != 1 {
		t.Errorf("asked %v", got)
	}
	onMain(func() { dialog.ForceClose() })
}

// Damaged copies set aside are named.
func TestGTKPasswordDamagedRow(t *testing.T) {
	needGTK(t)
	m, f, b := passwordsWithFile(t, secrets.NoKeyring)
	if err := f.Create(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Path(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Lookup(secrets.Key{}); err != nil {
		t.Fatal(err)
	}
	var rows *passwordRows
	var dialog *adw.PreferencesDialog
	onMain(func() {
		dialog = adw.NewPreferencesDialog()
		_, rows = passwordsGroup(m, b, dialog)
		damaged := f.Damaged()
		if !rows.damaged.Visible() || len(damaged) != 1 || rows.damaged.Subtitle() != damaged[0] {
			t.Errorf("damaged row visible %v subtitle %q, copies %v", rows.damaged.Visible(), rows.damaged.Subtitle(), damaged)
		}
		dialog.ForceClose()
	})
}
