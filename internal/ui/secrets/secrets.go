// Package secrets stores server passwords.
//
// Keyring keeps them with libsecret. Inside the Flatpak sandbox libsecret
// keeps them in an encrypted file in the app's own data directory, with a
// key from the Secret portal: Murmel sees only its own secrets, not the
// desktop keyring. Elsewhere (RPM, builds from source) libsecret uses the
// desktop keyring through the Secret Service (GNOME Keyring, KWallet),
// with the same schema and attributes Murmel always used, so stored
// passwords stay found.
//
// Every call blocks (D-Bus, and possibly an unlock prompt): run it off the
// GTK main loop. Errors are returned, never fatal; KindOf tells whether the
// keyring is gone or the user refused to unlock it.
package secrets

/*
#cgo pkg-config: libsecret-1
#include <stdlib.h>
#include <libsecret/secret.h>

// The attribute names and types match what Murmel stored through the
// Secret Service before it used libsecret (the port as a string).
static const SecretSchema *murmel_schema(void) {
	static const SecretSchema schema = {
		"io.github.one337.murmel.ServerPassword", SECRET_SCHEMA_NONE,
		{
			{ "host", SECRET_SCHEMA_ATTRIBUTE_STRING },
			{ "port", SECRET_SCHEMA_ATTRIBUTE_STRING },
			{ "username", SECRET_SCHEMA_ATTRIBUTE_STRING },
			{ NULL, 0 },
		}
	};
	return &schema;
}

static GHashTable *murmel_attributes(const char *host, const char *port, const char *username) {
	GHashTable *a = g_hash_table_new_full(g_str_hash, g_str_equal, NULL, g_free);
	g_hash_table_insert(a, "host", g_strdup(host));
	g_hash_table_insert(a, "port", g_strdup(port));
	g_hash_table_insert(a, "username", g_strdup(username));
	return a;
}

// murmel_error describes a failed call: the GError's domain (a static
// string), code and message (to free).
typedef struct {
	const char *domain;
	int code;
	char *message;
} murmel_error;

// Each function returns FALSE and fills err on failure.
static gboolean murmel_take_error(GError *error, murmel_error *err) {
	if (!error)
		return TRUE;
	err->domain = g_quark_to_string(error->domain);
	err->code = error->code;
	err->message = g_strdup(error->message);
	g_error_free(error);
	return FALSE;
}

static gboolean murmel_store(const char *host, const char *port, const char *username,
                             const char *label, const char *password, murmel_error *err) {
	GError *error = NULL;
	GHashTable *a = murmel_attributes(host, port, username);
	secret_password_storev_sync(murmel_schema(), a, SECRET_COLLECTION_DEFAULT,
	                            label, password, NULL, &error);
	g_hash_table_unref(a);
	return murmel_take_error(error, err);
}

static gboolean murmel_lookup(const char *host, const char *port, const char *username,
                              char **password, murmel_error *err) {
	GError *error = NULL;
	GHashTable *a = murmel_attributes(host, port, username);
	gchar *pw = secret_password_lookupv_sync(murmel_schema(), a, NULL, &error);
	g_hash_table_unref(a);
	if (error)
		return murmel_take_error(error, err);
	*password = pw ? strdup(pw) : NULL;
	secret_password_free(pw);
	return TRUE;
}

static gboolean murmel_clear(const char *host, const char *port, const char *username,
                             murmel_error *err) {
	GError *error = NULL;
	GHashTable *a = murmel_attributes(host, port, username);
	secret_password_clearv_sync(murmel_schema(), a, NULL, &error);
	g_hash_table_unref(a);
	return murmel_take_error(error, err);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"strconv"
	"unsafe"
)

// Key identifies one server account.
type Key struct {
	Host     string
	Port     int
	Username string
}

func (k Key) label() string {
	return fmt.Sprintf("Murmel: %s@%s:%d", k.Username, k.Host, k.Port)
}

// Store keeps server passwords. Lookup returns "" and no error when no
// password is stored; Delete of a missing password is no error.
type Store interface {
	Save(k Key, password string) error
	Lookup(k Key) (string, error)
	Delete(k Key) error
}

// Kind sorts the failures of a Store.
type Kind int

const (
	// Other is any failure not listed below.
	Other Kind = iota
	// Gone: there is no keyring to talk to (no Secret Service, the
	// sandbox does not let Murmel reach it).
	Gone
	// Refused: the keyring is there, but locked, or the user cancelled
	// the unlock prompt.
	Refused
)

// Error is a failure of a Store.
type Error struct {
	Kind Kind
	msg  string
}

func (e *Error) Error() string { return "secrets: " + e.msg }

// KindOf returns the Kind of err (Other when it is no *Error).
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return Other
}

// Error domains and codes libsecret reports (see classify).
const (
	dbusDomain   = "g-dbus-error-quark"
	ioDomain     = "g-io-error-quark"
	secretDomain = "secret-error"

	ioCancelled    = C.G_IO_ERROR_CANCELLED
	secretIsLocked = C.SECRET_ERROR_IS_LOCKED

	dbusFailed               = C.G_DBUS_ERROR_FAILED
	dbusServiceUnknown       = C.G_DBUS_ERROR_SERVICE_UNKNOWN
	dbusNameHasNoOwner       = C.G_DBUS_ERROR_NAME_HAS_NO_OWNER
	dbusAccessDenied         = C.G_DBUS_ERROR_ACCESS_DENIED
	dbusSpawnExecFailed      = C.G_DBUS_ERROR_SPAWN_EXEC_FAILED
	dbusSpawnServiceNotFound = C.G_DBUS_ERROR_SPAWN_SERVICE_NOT_FOUND
)

// goneCodes are the D-Bus errors that say no keyring can be reached: no
// Secret Service on the bus and none to start, or a sandbox or security
// policy that does not let Murmel talk to it.
var goneCodes = map[int]bool{
	dbusServiceUnknown:                       true,
	dbusNameHasNoOwner:                       true,
	dbusAccessDenied:                         true,
	dbusSpawnExecFailed:                      true,
	C.G_DBUS_ERROR_SPAWN_FORK_FAILED:         true,
	C.G_DBUS_ERROR_SPAWN_CHILD_EXITED:        true,
	C.G_DBUS_ERROR_SPAWN_CHILD_SIGNALED:      true,
	C.G_DBUS_ERROR_SPAWN_FAILED:              true,
	C.G_DBUS_ERROR_SPAWN_SETUP_FAILED:        true,
	C.G_DBUS_ERROR_SPAWN_CONFIG_INVALID:      true,
	C.G_DBUS_ERROR_SPAWN_SERVICE_INVALID:     true,
	dbusSpawnServiceNotFound:                 true,
	C.G_DBUS_ERROR_SPAWN_PERMISSIONS_INVALID: true,
	C.G_DBUS_ERROR_SPAWN_FILE_INVALID:        true,
	C.G_DBUS_ERROR_SPAWN_NO_MEMORY:           true,
}

// classify sorts a GError by its domain and code.
func classify(domain string, code int) Kind {
	switch {
	case domain == dbusDomain && goneCodes[code]:
		return Gone
	case domain == ioDomain && code == ioCancelled,
		domain == secretDomain && code == secretIsLocked:
		return Refused
	}
	return Other
}

// cstrings holds C copies of k's attributes; free releases them.
type cstrings struct{ host, port, username *C.char }

func (k Key) c() cstrings {
	return cstrings{
		host:     C.CString(k.Host),
		port:     C.CString(strconv.Itoa(k.Port)),
		username: C.CString(k.Username),
	}
}

func (s cstrings) free() {
	C.free(unsafe.Pointer(s.host))
	C.free(unsafe.Pointer(s.port))
	C.free(unsafe.Pointer(s.username))
}

func takeError(ok C.gboolean, e *C.murmel_error) error {
	if ok != 0 {
		return nil
	}
	defer C.g_free(C.gpointer(e.message))
	domain := C.GoString(e.domain)
	return &Error{
		Kind: classify(domain, int(e.code)),
		msg:  fmt.Sprintf("%s (%s %d)", C.GoString(e.message), domain, int(e.code)),
	}
}

// Keyring is the Store in the desktop keyring (or, in a sandbox, the
// encrypted file libsecret keeps with the Secret portal).
type Keyring struct{}

// Save stores (or replaces) the password for k.
func (Keyring) Save(k Key, password string) error {
	a := k.c()
	defer a.free()
	label := C.CString(k.label())
	defer C.free(unsafe.Pointer(label))
	pw := C.CString(password)
	defer func() {
		// Overwrite the C copy of the password before freeing it.
		C.memset(unsafe.Pointer(pw), 0, C.size_t(len(password)))
		C.free(unsafe.Pointer(pw))
	}()
	var e C.murmel_error
	return takeError(C.murmel_store(a.host, a.port, a.username, label, pw, &e), &e)
}

// Lookup returns the stored password for k.
func (Keyring) Lookup(k Key) (string, error) {
	a := k.c()
	defer a.free()
	var pw *C.char
	var e C.murmel_error
	if err := takeError(C.murmel_lookup(a.host, a.port, a.username, &pw, &e), &e); err != nil {
		return "", err
	}
	if pw == nil {
		return "", nil
	}
	defer func() {
		C.memset(unsafe.Pointer(pw), 0, C.strlen(pw))
		C.free(unsafe.Pointer(pw))
	}()
	return C.GoString(pw), nil
}

// Delete removes the stored password for k.
func (Keyring) Delete(k Key) error {
	a := k.c()
	defer a.free()
	var e C.murmel_error
	return takeError(C.murmel_clear(a.host, a.port, a.username, &e), &e)
}
