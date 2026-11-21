// Package portal tells which XDG desktop portals the session offers.
package portal

import (
	"context"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// Name and Path address the portal frontend on the session bus.
const (
	Name = "org.freedesktop.portal.Desktop"
	Path = "/org/freedesktop/portal/desktop"
)

// Version returns the version of the portal interface iface, or 0 when the
// session does not offer it. It reads the interface's "version" property
// through a proxy, which only caches what exists: a missing portal is not a
// failed call. Failed calls through gotk4 leave a GLib critical in the log
// (its finalizer frees the NULL reply), so callers ask this before calling
// the portal.
func Version(ctx context.Context, conn *gio.DBusConnection, iface string) uint32 {
	proxy, err := gio.NewDBusProxySync(ctx, conn, gio.DBusProxyFlagsDoNotConnectSignals, nil, Name, Path, iface)
	if err != nil || proxy == nil {
		return 0
	}
	v := proxy.CachedProperty("version")
	if v == nil || !v.IsOfType(glib.NewVariantType("u")) {
		return 0
	}
	return v.Uint32()
}
