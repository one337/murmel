package secrets

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/one337/murmel/internal/ui/portal/portaltest"
)

// connect opens a connection to the bus at addr, closed when the test ends.
func connect(t *testing.T, addr string) *gio.DBusConnection {
	t.Helper()
	ctx := context.Background()
	conn, err := gio.NewDBusConnectionForAddressSync(ctx, addr,
		gio.DBusConnectionFlagsAuthenticationClient|gio.DBusConnectionFlagsMessageBusConnection, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseSync(ctx) })
	return conn
}

// own makes a connection of its own own name on the bus at addr.
func own(t *testing.T, addr, name string) {
	t.Helper()
	_, err := connect(t, addr).CallSync(context.Background(), "org.freedesktop.DBus", "/org/freedesktop/DBus",
		"org.freedesktop.DBus", "RequestName",
		glib.NewVariantTuple([]*glib.Variant{glib.NewVariantString(name), glib.NewVariantUint32(0)}),
		nil, gio.DBusCallFlagsNone, -1)
	if err != nil {
		t.Fatal(err)
	}
}

// startableSecretService returns a service directory in which the Secret
// Service can be started (by a program that would fail).
func startableSecretService(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "org.freedesktop.secrets.service"),
		[]byte("[D-BUS Service]\nName=org.freedesktop.secrets\nExec=/bin/false\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDetectOnTheHost(t *testing.T) {
	ctx := context.Background()
	t.Run("secret service runs", func(t *testing.T) {
		addr := privateBus(t, "")
		own(t, addr, secretServiceName)
		if got := DetectOn(ctx, connect(t, addr), false); got != InKeyring {
			t.Errorf("got %v", got)
		}
	})
	t.Run("secret service startable", func(t *testing.T) {
		addr := privateBus(t, startableSecretService(t))
		conn := connect(t, addr)
		if got := DetectOn(ctx, conn, false); got != InKeyring {
			t.Errorf("got %v", got)
		}
		// Detecting started nothing.
		if busCall(ctx, conn, "NameHasOwner", glib.NewVariantString(secretServiceName), func(v *glib.Variant) bool {
			return v.ChildValue(0).Boolean()
		}) {
			t.Error("the Secret Service was started")
		}
	})
	t.Run("no secret service", func(t *testing.T) {
		addr := privateBus(t, "")
		if got := DetectOn(ctx, connect(t, addr), false); got != NoKeyring {
			t.Errorf("got %v", got)
		}
	})
}

func TestDetectInASandbox(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		portal map[string]uint32
		want   State
	}{
		{"secret portal 1", map[string]uint32{secretPortal: 1}, InKeyring},
		{"secret portal 0", map[string]uint32{secretPortal: 0}, NoKeyring},
		{"secret portal 2", map[string]uint32{secretPortal: 2}, NoKeyring},
		{"portal without secret", map[string]uint32{"org.freedesktop.portal.Settings": 2}, NoKeyring},
		{"no portal", nil, NoKeyring},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr := privateBus(t, "")
			if tc.portal != nil {
				(&portaltest.Portal{Versions: tc.portal}).ServeAt(t, addr)
			}
			// A Secret Service on the bus does not count in a sandbox:
			// libsecret does not use it there.
			own(t, addr, secretServiceName)
			if got := DetectOn(ctx, connect(t, addr), true); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// Without a session bus (as in every test program) there is no keyring.
func TestDetectWithoutSessionBus(t *testing.T) {
	if got := Detect(context.Background()); got != NoKeyring {
		t.Errorf("got %v", got)
	}
}

func TestSandboxed(t *testing.T) {
	info := filepath.Join(t.TempDir(), "flatpak-info")
	if Sandboxed(info) {
		t.Error("sandboxed without Flatpak or Snap")
	}
	if err := os.WriteFile(info, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !Sandboxed(info) {
		t.Error("not sandboxed in a Flatpak")
	}
	t.Setenv("SNAP_NAME", "murmel")
	if !Sandboxed(filepath.Join(t.TempDir(), "none")) {
		t.Error("not sandboxed in a Snap")
	}
}
