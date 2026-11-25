package clock

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/one337/murmel/internal/ui/portal"
)

const (
	ifSettings      = "org.freedesktop.portal.Settings"
	interfaceSchema = "org.gnome.desktop.interface"
	clockFormatKey  = "clock-format"
	portalTimeout   = 5 * time.Second
)

var (
	errNoPortal = errors.New("no settings portal")
	errNoKey    = errors.New("the settings portal has no clock-format")
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
			conn.SignalSubscribe(portal.Name, ifSettings, "SettingChanged", portal.Path, interfaceSchema,
				gio.DBusSignalFlagsNone, func(_ *gio.DBusConnection, _, _, _, _ string, params *glib.Variant) {
					if params.ChildValue(1).String() == clockFormatKey {
						set(unwrap(params.ChildValue(2)).String())
					}
				})
			set(format)
		})
	}()
}

// readPortal asks the Settings portal for clock-format. ReadAll exists in
// every version of the interface and leaves out what the portal does not
// know instead of failing.
func readPortal(ctx context.Context, conn *gio.DBusConnection) (string, error) {
	if portal.Version(ctx, conn, ifSettings) == 0 {
		return "", errNoPortal
	}
	namespaces := glib.NewVariantArray(glib.NewVariantType("s"),
		[]*glib.Variant{glib.NewVariantString(interfaceSchema)})
	reply, err := conn.CallSync(ctx, portal.Name, portal.Path, ifSettings, "ReadAll",
		glib.NewVariantTuple([]*glib.Variant{namespaces}), glib.NewVariantType("(a{sa{sv}})"),
		gio.DBusCallFlagsNone, int(portalTimeout/time.Millisecond))
	if err != nil {
		return "", err
	}
	keys := lookup(reply.ChildValue(0), interfaceSchema)
	if keys == nil {
		return "", errNoKey
	}
	v := lookup(keys, clockFormatKey)
	if v == nil {
		return "", errNoKey
	}
	return unwrap(v).String(), nil
}

// lookup returns the value of key in the dictionary dict, or nil.
func lookup(dict *glib.Variant, key string) *glib.Variant {
	for i := uint(0); i < dict.NChildren(); i++ {
		entry := dict.ChildValue(i)
		if entry.ChildValue(0).String() == key {
			return entry.ChildValue(1)
		}
	}
	return nil
}

// unwrap strips the variant boxes the portal puts around a value.
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
