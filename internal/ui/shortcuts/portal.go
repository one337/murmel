// Package shortcuts registers Murmel's global shortcuts with the desktop
// through the Global Shortcuts portal (org.freedesktop.portal.GlobalShortcuts)
// and reports when they are pressed and released, also while another
// window has the focus.
//
// The desktop owns the key bindings: Murmel only names its shortcuts, the
// user assigns the keys in the desktop's dialog (BindShortcuts the first
// time, ConfigureShortcuts later). None has a key by default.
//
// Calls block on D-Bus (and BindShortcuts on the user): run them off the
// main loop. Callbacks run on the main loop.
package shortcuts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// Shortcut IDs, as registered with the portal.
const (
	PushToTalk      = "push-to-talk"
	ToggleMute      = "toggle-mute"
	ToggleDeafen    = "toggle-deafen"
	PreviousChannel = "previous-channel"
	NextChannel     = "next-channel"
)

// Shortcut is one shortcut Murmel offers.
type Shortcut struct {
	ID          string
	Description string
}

// Binding is a shortcut with the key the desktop assigned ("" = none).
type Binding struct {
	ID      string
	Trigger string
}

const (
	portalName   = "org.freedesktop.portal.Desktop"
	portalPath   = "/org/freedesktop/portal/desktop"
	ifShortcuts  = "org.freedesktop.portal.GlobalShortcuts"
	ifRequest    = "org.freedesktop.portal.Request"
	ifRegistry   = "org.freedesktop.host.portal.Registry"
	callTimeout  = 10 * time.Second
	userTimeout  = 10 * time.Minute // BindShortcuts may wait for the user
	responseOK   = 0
	responseUser = 1 // cancelled by the user
)

// ErrUnavailable means the desktop has no Global Shortcuts portal.
var ErrUnavailable = errors.New("shortcuts: the desktop offers no global shortcuts")

// ErrCancelled means the user closed the desktop's dialog.
var ErrCancelled = errors.New("shortcuts: cancelled")

// Portal is a session with the Global Shortcuts portal.
type Portal struct {
	appID     string
	shortcuts []Shortcut
	// OnActivated is called on the main loop when a shortcut is pressed
	// (pressed true) or released.
	OnActivated func(id string, pressed bool)
	// OnChanged is called on the main loop when the desktop reports new
	// bindings (after the user changed them).
	OnChanged func([]Binding)

	mu      sync.Mutex
	conn    *gio.DBusConnection
	session string
	version uint32
}

// New prepares a portal session for appID offering shortcuts; nothing is
// sent before Bind.
func New(appID string, shortcuts []Shortcut) *Portal {
	return &Portal{appID: appID, shortcuts: shortcuts}
}

// Available reports whether the desktop offers the portal (without
// registering anything).
func Available(ctx context.Context) bool {
	conn, err := gio.BusGetSync(ctx, gio.BusTypeSession)
	if err != nil {
		return false
	}
	_, err = portalVersion(ctx, conn)
	return err == nil
}

func portalVersion(ctx context.Context, conn *gio.DBusConnection) (uint32, error) {
	reply, err := conn.CallSync(ctx, portalName, portalPath, "org.freedesktop.DBus.Properties", "Get",
		glib.NewVariantTuple([]*glib.Variant{glib.NewVariantString(ifShortcuts), glib.NewVariantString("version")}),
		glib.NewVariantType("(v)"), gio.DBusCallFlagsNone, int(callTimeout/time.Millisecond))
	if err != nil {
		return 0, ErrUnavailable
	}
	return reply.ChildValue(0).Variant().Uint32(), nil
}

// Bind opens the session (once) and registers the shortcuts. The first
// time, the desktop asks the user to assign keys; later it returns the
// stored bindings without asking.
func (p *Portal) Bind(ctx context.Context) ([]Binding, error) {
	if err := p.open(ctx); err != nil {
		return nil, err
	}
	list := make([]*glib.Variant, 0, len(p.shortcuts))
	for _, s := range p.shortcuts {
		list = append(list, glib.NewVariantTuple([]*glib.Variant{
			glib.NewVariantString(s.ID),
			dict(map[string]*glib.Variant{"description": glib.NewVariantString(s.Description)}),
		}))
	}
	results, err := p.request(ctx, userTimeout, "BindShortcuts", func(token string) *glib.Variant {
		return glib.NewVariantTuple([]*glib.Variant{
			glib.NewVariantObjectPath(p.session),
			glib.NewVariantArray(glib.NewVariantType("(sa{sv})"), list),
			glib.NewVariantString(""),
			dict(map[string]*glib.Variant{"handle_token": glib.NewVariantString(token)}),
		})
	})
	if err != nil {
		return nil, err
	}
	return bindings(lookup(results, "shortcuts")), nil
}

// List returns the current bindings without asking the user (empty
// before the first Bind).
func (p *Portal) List(ctx context.Context) ([]Binding, error) {
	if err := p.open(ctx); err != nil {
		return nil, err
	}
	results, err := p.request(ctx, callTimeout, "ListShortcuts", func(token string) *glib.Variant {
		return glib.NewVariantTuple([]*glib.Variant{
			glib.NewVariantObjectPath(p.session),
			dict(map[string]*glib.Variant{"handle_token": glib.NewVariantString(token)}),
		})
	})
	if err != nil {
		return nil, err
	}
	return bindings(lookup(results, "shortcuts")), nil
}

// Configure opens the desktop's dialog for changing the keys (portal
// version 2); the new bindings arrive through OnChanged. With version 1
// it registers again, which also asks.
func (p *Portal) Configure(ctx context.Context) error {
	if err := p.open(ctx); err != nil {
		return err
	}
	if p.version < 2 {
		_, err := p.Bind(ctx)
		return err
	}
	_, err := p.conn.CallSync(ctx, portalName, portalPath, ifShortcuts, "ConfigureShortcuts",
		glib.NewVariantTuple([]*glib.Variant{
			glib.NewVariantObjectPath(p.session),
			glib.NewVariantString(""),
			dict(nil),
		}), nil, gio.DBusCallFlagsNone, int(callTimeout/time.Millisecond))
	return err
}

// open connects and creates the session, once.
func (p *Portal) open(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != "" {
		return nil
	}
	// A connection of its own: an app outside a sandbox has to register its
	// ID before its first portal call on a connection.
	addr, err := gio.DBusAddressGetForBusSync(ctx, gio.BusTypeSession)
	if err != nil {
		return fmt.Errorf("shortcuts: session bus: %w", err)
	}
	conn, err := gio.NewDBusConnectionForAddressSync(ctx, addr,
		gio.DBusConnectionFlagsAuthenticationClient|gio.DBusConnectionFlagsMessageBusConnection, nil)
	if err != nil {
		return fmt.Errorf("shortcuts: session bus: %w", err)
	}
	p.conn = conn
	if p.version, err = portalVersion(ctx, conn); err != nil {
		return err
	}
	if _, err := os.Stat("/.flatpak-info"); err != nil {
		// Outside Flatpak the portal cannot tell which app is asking.
		if _, err := conn.CallSync(ctx, portalName, portalPath, ifRegistry, "Register",
			glib.NewVariantTuple([]*glib.Variant{glib.NewVariantString(p.appID), dict(nil)}),
			nil, gio.DBusCallFlagsNone, int(callTimeout/time.Millisecond)); err != nil {
			slog.Info("shortcuts: registering the app id failed", "err", err)
		}
	}
	sessionToken := token()
	results, err := p.requestLocked(ctx, callTimeout, "CreateSession", func(t string) *glib.Variant {
		return glib.NewVariantTuple([]*glib.Variant{dict(map[string]*glib.Variant{
			"handle_token":         glib.NewVariantString(t),
			"session_handle_token": glib.NewVariantString(sessionToken),
		})})
	})
	if err != nil {
		return err
	}
	h := lookup(results, "session_handle")
	if h == nil {
		return errors.New("shortcuts: no session")
	}
	p.session = h.String()
	p.subscribe()
	return nil
}

// subscribe forwards the session's signals to the callbacks. gio calls
// them on the default main context, the GTK main loop.
func (p *Portal) subscribe() {
	activation := func(pressed bool) gio.DBusSignalCallback {
		return func(_ *gio.DBusConnection, _, _, _, _ string, params *glib.Variant) {
			if params.ChildValue(0).String() != p.session || p.OnActivated == nil {
				return
			}
			p.OnActivated(params.ChildValue(1).String(), pressed)
		}
	}
	p.conn.SignalSubscribe(portalName, ifShortcuts, "Activated", portalPath, "",
		gio.DBusSignalFlagsNone, activation(true))
	p.conn.SignalSubscribe(portalName, ifShortcuts, "Deactivated", portalPath, "",
		gio.DBusSignalFlagsNone, activation(false))
	p.conn.SignalSubscribe(portalName, ifShortcuts, "ShortcutsChanged", portalPath, "",
		gio.DBusSignalFlagsNone, func(_ *gio.DBusConnection, _, _, _, _ string, params *glib.Variant) {
			if params.ChildValue(0).String() != p.session || p.OnChanged == nil {
				return
			}
			p.OnChanged(bindings(params.ChildValue(1)))
		})
}

func (p *Portal) request(ctx context.Context, timeout time.Duration, method string, params func(token string) *glib.Variant) (*glib.Variant, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requestLocked(ctx, timeout, method, params)
}

// requestLocked calls a portal method that answers through a Request
// object and waits for its Response.
func (p *Portal) requestLocked(ctx context.Context, timeout time.Duration, method string, params func(token string) *glib.Variant) (*glib.Variant, error) {
	t := token()
	sender := strings.ReplaceAll(strings.TrimPrefix(p.conn.UniqueName(), ":"), ".", "_")
	path := portalPath + "/request/" + sender + "/" + t
	type response struct {
		code    uint32
		results *glib.Variant
	}
	done := make(chan response, 1)
	sub := p.conn.SignalSubscribe(portalName, ifRequest, "Response", path, "",
		gio.DBusSignalFlagsNone, func(_ *gio.DBusConnection, _, _, _, _ string, v *glib.Variant) {
			select {
			case done <- response{v.ChildValue(0).Uint32(), v.ChildValue(1)}:
			default:
			}
		})
	defer p.conn.SignalUnsubscribe(sub)
	if _, err := p.conn.CallSync(ctx, portalName, portalPath, ifShortcuts, method, params(t),
		glib.NewVariantType("(o)"), gio.DBusCallFlagsNone, int(callTimeout/time.Millisecond)); err != nil {
		return nil, fmt.Errorf("shortcuts: %s: %w", method, err)
	}
	select {
	case r := <-done:
		switch r.code {
		case responseOK:
			return r.results, nil
		case responseUser:
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("shortcuts: %s failed", method)
	case <-time.After(timeout):
		return nil, fmt.Errorf("shortcuts: %s: no answer", method)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// bindings reads an a(sa{sv}) shortcut list.
func bindings(list *glib.Variant) []Binding {
	if list == nil {
		return nil
	}
	out := make([]Binding, 0, list.NChildren())
	for i := uint(0); i < list.NChildren(); i++ {
		s := list.ChildValue(i)
		b := Binding{ID: s.ChildValue(0).String()}
		if v := lookup(s.ChildValue(1), "trigger_description"); v != nil {
			b.Trigger = v.String()
		}
		out = append(out, b)
	}
	return out
}

// lookup returns the value of key in an a{sv}, or nil.
func lookup(d *glib.Variant, key string) *glib.Variant {
	if d == nil {
		return nil
	}
	for i := uint(0); i < d.NChildren(); i++ {
		e := d.ChildValue(i)
		if e.ChildValue(0).String() == key {
			return e.ChildValue(1).Variant()
		}
	}
	return nil
}

func dict(m map[string]*glib.Variant) *glib.Variant {
	entries := make([]*glib.Variant, 0, len(m))
	for k, v := range m {
		entries = append(entries, glib.NewVariantDictEntry(glib.NewVariantString(k), glib.NewVariantVariant(v)))
	}
	return glib.NewVariantArray(glib.NewVariantType("{sv}"), entries)
}

func token() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "murmel" + hex.EncodeToString(b)
}
