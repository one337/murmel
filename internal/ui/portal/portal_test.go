package portal_test

import (
	"context"
	"os"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gio/v2"

	"github.com/one337/murmel/internal/ui/portal"
	"github.com/one337/murmel/internal/ui/portal/portaltest"
)

func TestMain(m *testing.M) { os.Exit(portaltest.Run(m)) }

func session(t *testing.T) *gio.DBusConnection {
	t.Helper()
	conn, err := gio.BusGetSync(context.Background(), gio.BusTypeSession)
	if err != nil {
		t.Fatalf("session bus: %v", err)
	}
	return conn
}

func TestVersionWithoutPortal(t *testing.T) {
	problems := portaltest.GLibProblems(t)
	if v := portal.Version(context.Background(), session(t), "org.freedesktop.portal.Settings"); v != 0 {
		t.Errorf("Version = %d without a portal, want 0", v)
	}
	if p := problems(); len(p) > 0 {
		t.Errorf("GLib logged %q", p)
	}
}

func TestVersionOfOfferedInterfacesOnly(t *testing.T) {
	problems := portaltest.GLibProblems(t)
	fake := &portaltest.Portal{Versions: map[string]uint32{"org.freedesktop.portal.Settings": 2}}
	fake.Serve(t)
	conn := session(t)
	if v := portal.Version(context.Background(), conn, "org.freedesktop.portal.Settings"); v != 2 {
		t.Errorf("Settings version = %d, want 2", v)
	}
	if v := portal.Version(context.Background(), conn, "org.freedesktop.portal.GlobalShortcuts"); v != 0 {
		t.Errorf("GlobalShortcuts version = %d on a portal without it, want 0", v)
	}
	if p := problems(); len(p) > 0 {
		t.Errorf("GLib logged %q", p)
	}
}
