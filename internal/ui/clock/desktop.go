package clock

import (
	"context"
	"log/slog"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

const (
	portalName      = "org.freedesktop.portal.Desktop"
	portalPath      = "/org/freedesktop/portal/desktop"
	ifSettings      = "org.freedesktop.portal.Settings"
	interfaceSchema = "org.gnome.desktop.interface"
	clockFormatKey  = "clock-format"
	portalTimeout   = 5 * time.Second
)

// desktopSettings keeps the GSettings fallback (and its signal) alive.
var desktopSettings *gio.Settings

// watchDesktop reads the clock format through the Settings portal — the
// same for Flatpak, Snap and a host install — off the main loop, and
// follows its SettingChanged signal. Without a portal that knows the key
// it uses GSettings when the GNOME schema is installed; otherwise the
// format stays unknown and the locale decides.
func watchDesktop(set func(string)) {
	go func() {
		ctx := context.Background()
		conn, err := gio.BusGetSync(ctx, gio.BusTypeSession)
		var format string
		if err == nil {
			format, err = readPortal(ctx, conn)
		}
		glib.IdleAdd(func() {
			if err != nil {
				slog.Debug("clock format: no settings portal", "err", err)
				watchGSettings(set)
				return
			}
			conn.SignalSubscribe(portalName, ifSettings, "SettingChanged", portalPath, interfaceSchema,
				gio.DBusSignalFlagsNone, func(_ *gio.DBusConnection, _, _, _, _ string, params *glib.Variant) {
					if params.ChildValue(1).String() == clockFormatKey {
						set(unwrap(params.ChildValue(2)).String())
					}
				})
			set(format)
		})
	}()
}

// readPortal asks the Settings portal for clock-format (ReadOne, or Read
// on portals older than version 2).
func readPortal(ctx context.Context, conn *gio.DBusConnection) (string, error) {
	args := glib.NewVariantTuple([]*glib.Variant{
		glib.NewVariantString(interfaceSchema), glib.NewVariantString(clockFormatKey),
	})
	timeout := int(portalTimeout / time.Millisecond)
	reply, err := conn.CallSync(ctx, portalName, portalPath, ifSettings, "ReadOne",
		args, glib.NewVariantType("(v)"), gio.DBusCallFlagsNone, timeout)
	if err != nil {
		reply, err = conn.CallSync(ctx, portalName, portalPath, ifSettings, "Read",
			args, glib.NewVariantType("(v)"), gio.DBusCallFlagsNone, timeout)
		if err != nil {
			return "", err
		}
	}
	return unwrap(reply.ChildValue(0)).String(), nil
}

// unwrap strips the variant boxes around a value (Read boxes it twice).
func unwrap(v *glib.Variant) *glib.Variant {
	for v.IsOfType(glib.NewVariantType("v")) {
		v = v.Variant()
	}
	return v
}

// watchGSettings follows clock-format through GSettings when the GNOME
// schema is installed. Main loop only.
func watchGSettings(set func(string)) {
	src := gio.SettingsSchemaSourceGetDefault()
	if src == nil {
		return
	}
	schema := src.Lookup(interfaceSchema, true)
	if schema == nil || !schema.HasKey(clockFormatKey) {
		return
	}
	desktopSettings = gio.NewSettings(interfaceSchema)
	desktopSettings.ConnectChanged(func(key string) {
		if key == clockFormatKey {
			set(desktopSettings.String(clockFormatKey))
		}
	})
	set(desktopSettings.String(clockFormatKey))
}
