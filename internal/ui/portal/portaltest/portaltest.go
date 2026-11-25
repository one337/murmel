// Package portaltest runs a stand-in for the desktop portal on a private
// session bus, for tests of code that talks to portals.
package portaltest

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/one337/murmel/internal/ui/portal"
)

// Run runs the tests of m on a private session bus: a dbus-daemon that can
// start no services, so neither the desktop's portals nor anything else
// answer unless a test serves them. Call it from TestMain before anything
// opens the session bus. Without dbus-daemon the tests are skipped, except
// in CI, where they fail.
func Run(m *testing.M) int {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		if os.Getenv("CI") != "" {
			slog.Error("portal tests need dbus-daemon")
			return 1
		}
		slog.Warn("portal tests skipped: no dbus-daemon")
		return 0
	}
	bus := gio.NewTestDBus(gio.TestDBusNone)
	bus.Up()
	// Stop, not Down: Down waits for the shared session connection to be
	// freed, which Go's garbage collector decides.
	defer bus.Stop()
	return m.Run()
}

// Portal answers on the private bus as org.freedesktop.portal.Desktop.
type Portal struct {
	// Versions lists the interfaces the portal offers, with their version.
	Versions map[string]uint32
	// Settings holds what org.freedesktop.portal.Settings.ReadAll returns:
	// namespace → key → value.
	Settings map[string]map[string]*glib.Variant

	mu    sync.Mutex
	calls []string
}

// Serve makes p own the portal's bus name until the test ends.
func (p *Portal) Serve(t testing.TB) {
	t.Helper()
	ctx := context.Background()
	addr, err := gio.DBusAddressGetForBusSync(ctx, gio.BusTypeSession)
	if err != nil {
		t.Fatalf("session bus: %v", err)
	}
	conn, err := gio.NewDBusConnectionForAddressSync(ctx, addr,
		gio.DBusConnectionFlagsAuthenticationClient|gio.DBusConnectionFlagsMessageBusConnection, nil)
	if err != nil {
		t.Fatalf("session bus: %v", err)
	}
	filter := conn.AddFilter(func(c *gio.DBusConnection, msg *gio.DBusMessage, incoming bool) *gio.DBusMessage {
		if !incoming || msg.MessageType() != gio.DBusMessageTypeMethodCall || msg.Path() != portal.Path {
			return msg
		}
		reply := p.answer(msg)
		if reply == nil {
			return msg
		}
		if _, err := c.SendMessage(reply, gio.DBusSendMessageFlagsNone); err != nil {
			t.Errorf("portal reply: %v", err)
		}
		return nil
	})
	// RequestName goes to the bus itself and does not fail.
	if _, err := conn.CallSync(ctx, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus",
		"RequestName", glib.NewVariantTuple([]*glib.Variant{glib.NewVariantString(portal.Name), glib.NewVariantUint32(0)}),
		nil, gio.DBusCallFlagsNone, -1); err != nil {
		t.Fatalf("own %s: %v", portal.Name, err)
	}
	t.Cleanup(func() {
		conn.RemoveFilter(filter)
		if err := conn.CloseSync(ctx); err != nil {
			t.Errorf("close portal connection: %v", err)
		}
	})
}

// Calls returns "interface.method" for every call the portal received.
func (p *Portal) Calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

// answer builds the reply to a call, or returns nil to leave it to GDBus.
func (p *Portal) answer(msg *gio.DBusMessage) *gio.DBusMessage {
	iface, method, body := msg.Interface(), msg.Member(), msg.Body()
	p.mu.Lock()
	p.calls = append(p.calls, iface+"."+method)
	p.mu.Unlock()

	switch {
	case iface == "org.freedesktop.DBus.Properties" && method == "Get":
		target, name := body.ChildValue(0).String(), body.ChildValue(1).String()
		v, ok := p.Versions[target]
		if !ok || name != "version" {
			return msg.NewMethodErrorLiteral("org.freedesktop.DBus.Error.InvalidArgs", "no such property")
		}
		return withBody(msg, glib.NewVariantVariant(glib.NewVariantUint32(v)))
	case iface == "org.freedesktop.DBus.Properties" && method == "GetAll":
		v, ok := p.Versions[body.ChildValue(0).String()]
		if !ok {
			return msg.NewMethodErrorLiteral("org.freedesktop.DBus.Error.InvalidArgs", "no such interface")
		}
		return withBody(msg, dict("{sv}", map[string]*glib.Variant{
			"version": glib.NewVariantVariant(glib.NewVariantUint32(v)),
		}))
	case !p.offers(iface):
		return msg.NewMethodErrorLiteral("org.freedesktop.DBus.Error.UnknownInterface", "no such interface")
	case iface == "org.freedesktop.portal.Settings" && method == "ReadAll":
		out := map[string]*glib.Variant{}
		for _, ns := range body.ChildValue(0).Strv() {
			if keys, ok := p.Settings[ns]; ok {
				values := map[string]*glib.Variant{}
				for k, v := range keys {
					values[k] = glib.NewVariantVariant(v)
				}
				out[ns] = dict("{sv}", values)
			}
		}
		return withBody(msg, dict("{sa{sv}}", out))
	case strings.HasPrefix(iface, "org.freedesktop.portal.") && method == "CreateSession":
		// The answer would come as a Request signal; tests that get here
		// only check which calls were made.
		return withBody(msg, glib.NewVariantObjectPath(portal.Path+"/request/test"))
	case iface == "org.freedesktop.host.portal.Registry" && method == "Register":
		return msg.NewMethodReply()
	}
	return msg.NewMethodErrorLiteral("org.freedesktop.DBus.Error.UnknownMethod", "not served by the test portal")
}

func (p *Portal) offers(iface string) bool {
	_, ok := p.Versions[iface]
	return ok
}

func withBody(msg *gio.DBusMessage, v *glib.Variant) *gio.DBusMessage {
	reply := msg.NewMethodReply()
	reply.SetBody(glib.NewVariantTuple([]*glib.Variant{v}))
	return reply
}

// dict builds an array of dictionary entries of type entry ("{sv}", …).
func dict(entry string, m map[string]*glib.Variant) *glib.Variant {
	entries := make([]*glib.Variant, 0, len(m))
	for k, v := range m {
		entries = append(entries, glib.NewVariantDictEntry(glib.NewVariantString(k), v))
	}
	return glib.NewVariantArray(glib.NewVariantType(entry), entries)
}
