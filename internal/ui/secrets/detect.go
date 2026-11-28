package secrets

import (
	"context"
	"os"
	"slices"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"

	"github.com/one337/murmel/internal/ui/portal"
)

// State says where Murmel can save passwords.
type State int

const (
	// Unknown: Detect has not finished yet.
	Unknown State = iota
	// InKeyring: there is a keyring (the Secret Service, or in a sandbox
	// the Secret portal).
	InKeyring
	// NoKeyring: there is none; passwords are not saved.
	NoKeyring
	// InFile: there is no keyring, and the user chose to keep passwords
	// in an unencrypted file (see File).
	InFile
)

func (s State) String() string {
	switch s {
	case InKeyring:
		return "keyring"
	case NoKeyring:
		return "none"
	case InFile:
		return "file"
	}
	return "unknown"
}

const (
	secretServiceName = "org.freedesktop.secrets"
	secretPortal      = "org.freedesktop.portal.Secret"
)

// Sandboxed reports whether libsecret treats the process as sandboxed and
// keeps passwords in its own file with a key from the Secret portal: in a
// Flatpak (flatpakInfo is /.flatpak-info) or a Snap.
func Sandboxed(flatpakInfo string) bool {
	if _, ok := os.LookupEnv("SNAP_NAME"); ok {
		return true
	}
	_, err := os.Stat(flatpakInfo)
	return err == nil
}

// Detect finds out, without any prompt, whether libsecret has a keyring to
// talk to. It blocks: run it off the GTK main loop.
func Detect(ctx context.Context) State {
	addr, err := gio.DBusAddressGetForBusSync(ctx, gio.BusTypeSession)
	if err != nil {
		return NoKeyring
	}
	conn, err := gio.NewDBusConnectionForAddressSync(ctx, addr,
		gio.DBusConnectionFlagsAuthenticationClient|gio.DBusConnectionFlagsMessageBusConnection, nil)
	if err != nil {
		return NoKeyring
	}
	defer func() { _ = conn.CloseSync(ctx) }()
	return DetectOn(ctx, conn, Sandboxed("/.flatpak-info"))
}

// DetectOn is Detect on the session bus conn. In a sandbox it applies
// libsecret's own rule: the Secret portal in version 1. Elsewhere a Secret
// Service must run or be startable on the bus.
func DetectOn(ctx context.Context, conn *gio.DBusConnection, sandboxed bool) State {
	if sandboxed {
		if portal.Version(ctx, conn, secretPortal) == 1 {
			return InKeyring
		}
		return NoKeyring
	}
	if busCall(ctx, conn, "NameHasOwner", glib.NewVariantString(secretServiceName), func(v *glib.Variant) bool {
		return v.ChildValue(0).Boolean()
	}) {
		return InKeyring
	}
	if busCall(ctx, conn, "ListActivatableNames", nil, func(v *glib.Variant) bool {
		return slices.Contains(v.ChildValue(0).Strv(), secretServiceName)
	}) {
		return InKeyring
	}
	return NoKeyring
}

// busCall calls method of the bus itself (with arg, if any) and passes the
// reply to ok; a failed call counts as false.
func busCall(ctx context.Context, conn *gio.DBusConnection, method string, arg *glib.Variant, ok func(*glib.Variant) bool) bool {
	var params *glib.Variant
	if arg != nil {
		params = glib.NewVariantTuple([]*glib.Variant{arg})
	}
	reply, err := conn.CallSync(ctx, "org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus",
		method, params, nil, gio.DBusCallFlagsNoAutoStart, 5000)
	if err != nil || reply == nil {
		return false
	}
	return ok(reply)
}
