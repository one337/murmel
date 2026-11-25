package clock

import (
	"context"
	"os"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/one337/murmel/internal/ui/portal/portaltest"
)

func TestMain(m *testing.M) { os.Exit(portaltest.Run(m)) }

func readFromSession(t *testing.T) (string, error) {
	t.Helper()
	conn, err := gio.BusGetSync(context.Background(), gio.BusTypeSession)
	if err != nil {
		t.Fatalf("session bus: %v", err)
	}
	return readPortal(context.Background(), conn)
}

func TestNoSettingsPortalLogsNothing(t *testing.T) {
	problems := portaltest.GLibProblems(t)
	if f, err := readFromSession(t); err == nil {
		t.Errorf("read %q without a portal", f)
	}
	if p := problems(); len(p) > 0 {
		t.Errorf("GLib logged %q", p)
	}
}

func TestSettingsPortalWithoutTheKeyLogsNothing(t *testing.T) {
	problems := portaltest.GLibProblems(t)
	fake := &portaltest.Portal{Versions: map[string]uint32{"org.freedesktop.portal.Settings": 1}}
	fake.Serve(t)
	if f, err := readFromSession(t); err == nil {
		t.Errorf("read %q from a portal without the key", f)
	}
	if p := problems(); len(p) > 0 {
		t.Errorf("GLib logged %q", p)
	}
}

func TestSettingsPortalGivesTheClockFormat(t *testing.T) {
	problems := portaltest.GLibProblems(t)
	fake := &portaltest.Portal{
		Versions: map[string]uint32{"org.freedesktop.portal.Settings": 2},
		Settings: map[string]map[string]*glib.Variant{
			interfaceSchema: {clockFormatKey: glib.NewVariantString(format12h), "font-name": glib.NewVariantString("Sans 11")},
		},
	}
	fake.Serve(t)
	f, err := readFromSession(t)
	if err != nil || f != format12h {
		t.Errorf("read %q, %v; want %q", f, err, format12h)
	}
	if p := problems(); len(p) > 0 {
		t.Errorf("GLib logged %q", p)
	}
}
