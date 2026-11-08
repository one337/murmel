// Package secrets stores server passwords in the desktop keyring through the
// freedesktop Secret Service D-Bus API (GNOME Keyring, KWallet), using gio's
// D-Bus client — no libsecret build dependency.
//
// Every call blocks on D-Bus: run it off the GTK main loop. Errors are
// returned, never fatal; a missing or locked keyring simply means the
// password is not remembered.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

const (
	service    = "org.freedesktop.secrets"
	servicePth = "/org/freedesktop/secrets"
	ifService  = "org.freedesktop.Secret.Service"
	ifItem     = "org.freedesktop.Secret.Item"
	ifColl     = "org.freedesktop.Secret.Collection"
	defaultCol = "/org/freedesktop/secrets/aliases/default"

	// schema tags every item Murmel writes, so lookups never match foreign
	// entries.
	schema = "io.github.murmel.Murmel.ServerPassword"

	callTimeout = 5 * time.Second
)

// ErrLocked is returned when the keyring is locked and would need an
// interactive unlock prompt, which this client does not drive.
var ErrLocked = errors.New("secrets: keyring is locked")

// Key identifies one server account.
type Key struct {
	Host     string
	Port     int
	Username string
}

func (k Key) attributes() *glib.Variant {
	return stringDict(map[string]string{
		"xdg:schema": schema,
		"host":       k.Host,
		"port":       strconv.Itoa(k.Port),
		"username":   k.Username,
	})
}

func (k Key) label() string {
	return fmt.Sprintf("Murmel: %s@%s:%d", k.Username, k.Host, k.Port)
}

// Store saves (or replaces) the password for k.
func Store(k Key, password string) error {
	conn, session, err := open()
	if err != nil {
		return err
	}
	props := glib.NewVariantArray(glib.NewVariantType("{sv}"), []*glib.Variant{
		glib.NewVariantDictEntry(glib.NewVariantString(ifItem+".Label"),
			glib.NewVariantVariant(glib.NewVariantString(k.label()))),
		glib.NewVariantDictEntry(glib.NewVariantString(ifItem+".Attributes"),
			glib.NewVariantVariant(k.attributes())),
	})
	secret := glib.NewVariantTuple([]*glib.Variant{
		glib.NewVariantObjectPath(session),
		bytes(nil),
		bytes([]byte(password)),
		glib.NewVariantString("text/plain"),
	})
	reply, err := call(conn, defaultCol, ifColl, "CreateItem",
		glib.NewVariantTuple([]*glib.Variant{props, secret, glib.NewVariantBoolean(true)}), "(oo)")
	if err != nil {
		return err
	}
	if prompt := reply.ChildValue(1).String(); prompt != "/" {
		return ErrLocked
	}
	return nil
}

// Lookup returns the stored password for k ("" and no error when none is
// stored).
func Lookup(k Key) (string, error) {
	conn, session, err := open()
	if err != nil {
		return "", err
	}
	items, err := search(conn, k)
	if err != nil || len(items) == 0 {
		return "", err
	}
	reply, err := call(conn, items[0], ifItem, "GetSecret",
		glib.NewVariantTuple([]*glib.Variant{glib.NewVariantObjectPath(session)}), "((oayays))")
	if err != nil {
		return "", err
	}
	value := reply.ChildValue(0).ChildValue(2)
	out := make([]byte, value.NChildren())
	for i := range out {
		out[i] = value.ChildValue(uint(i)).Byte()
	}
	return string(out), nil
}

// Delete removes the stored password for k (no error when none exists).
func Delete(k Key) error {
	conn, _, err := open()
	if err != nil {
		return err
	}
	items, err := search(conn, k)
	if err != nil {
		return err
	}
	for _, item := range items {
		if _, err := call(conn, item, ifItem, "Delete", nil, "(o)"); err != nil {
			return err
		}
	}
	return nil
}

// open connects to the session bus and opens a "plain" Secret Service
// session (the secret travels over the local bus unencrypted, like
// libsecret's default for the session bus).
func open() (*gio.DBusConnection, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	conn, err := gio.BusGetSync(ctx, gio.BusTypeSession)
	if err != nil {
		return nil, "", fmt.Errorf("secrets: session bus: %w", err)
	}
	reply, err := call(conn, servicePth, ifService, "OpenSession",
		glib.NewVariantTuple([]*glib.Variant{
			glib.NewVariantString("plain"),
			glib.NewVariantVariant(glib.NewVariantString("")),
		}), "(vo)")
	if err != nil {
		return nil, "", fmt.Errorf("secrets: open session: %w", err)
	}
	return conn, reply.ChildValue(1).String(), nil
}

// search returns the unlocked items matching k. Locked matches are
// reported as ErrLocked.
func search(conn *gio.DBusConnection, k Key) ([]string, error) {
	reply, err := call(conn, servicePth, ifService, "SearchItems",
		glib.NewVariantTuple([]*glib.Variant{k.attributes()}), "(aoao)")
	if err != nil {
		return nil, err
	}
	unlocked := objectPaths(reply.ChildValue(0))
	if len(unlocked) == 0 && reply.ChildValue(1).NChildren() > 0 {
		return nil, ErrLocked
	}
	return unlocked, nil
}

func call(conn *gio.DBusConnection, path, iface, method string, params *glib.Variant, replyType string) (*glib.Variant, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return conn.CallSync(ctx, service, path, iface, method, params,
		glib.NewVariantType(replyType), gio.DBusCallFlagsNone, int(callTimeout/time.Millisecond))
}

func stringDict(m map[string]string) *glib.Variant {
	entries := make([]*glib.Variant, 0, len(m))
	for k, v := range m {
		entries = append(entries, glib.NewVariantDictEntry(glib.NewVariantString(k), glib.NewVariantString(v)))
	}
	return glib.NewVariantArray(glib.NewVariantType("{ss}"), entries)
}

// bytes builds an "ay" with exactly these bytes (NewVariantBytestring
// would append a NUL terminator).
func bytes(b []byte) *glib.Variant {
	children := make([]*glib.Variant, len(b))
	for i, c := range b {
		children[i] = glib.NewVariantByte(c)
	}
	return glib.NewVariantArray(glib.NewVariantType("y"), children)
}

func objectPaths(v *glib.Variant) []string {
	out := make([]string, v.NChildren())
	for i := range out {
		out[i] = v.ChildValue(uint(i)).String()
	}
	return out
}
