// Package secrets stores server passwords with libsecret.
//
// Inside the Flatpak sandbox libsecret keeps them in an encrypted file in
// the app's own data directory, with a key from the Secret portal: Murmel
// sees only its own secrets, not the desktop keyring. Elsewhere (RPM,
// builds from source) libsecret uses the desktop keyring through the
// Secret Service (GNOME Keyring, KWallet), with the same schema and
// attributes Murmel always used, so stored passwords stay found.
//
// Every call blocks (D-Bus, and possibly an unlock prompt): run it off the
// GTK main loop. Errors are returned, never fatal; a missing keyring or
// portal simply means the password is not remembered.
package secrets

/*
#cgo pkg-config: libsecret-1
#include <stdlib.h>
#include <libsecret/secret.h>

// The attribute names and types match what Murmel stored through the
// Secret Service before it used libsecret (the port as a string).
static const SecretSchema *murmel_schema(void) {
	static const SecretSchema schema = {
		"io.github.murmel.Murmel.ServerPassword", SECRET_SCHEMA_NONE,
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

// Each function returns NULL on success, or an error message to free.
static char *murmel_take_error(GError *error) {
	char *msg = g_strdup(error->message);
	g_error_free(error);
	return msg;
}

static char *murmel_store(const char *host, const char *port, const char *username,
                          const char *label, const char *password) {
	GError *error = NULL;
	GHashTable *a = murmel_attributes(host, port, username);
	secret_password_storev_sync(murmel_schema(), a, SECRET_COLLECTION_DEFAULT,
	                            label, password, NULL, &error);
	g_hash_table_unref(a);
	return error ? murmel_take_error(error) : NULL;
}

static char *murmel_lookup(const char *host, const char *port, const char *username,
                           char **password) {
	GError *error = NULL;
	GHashTable *a = murmel_attributes(host, port, username);
	gchar *pw = secret_password_lookupv_sync(murmel_schema(), a, NULL, &error);
	g_hash_table_unref(a);
	if (error)
		return murmel_take_error(error);
	*password = pw ? strdup(pw) : NULL;
	secret_password_free(pw);
	return NULL;
}

static char *murmel_clear(const char *host, const char *port, const char *username) {
	GError *error = NULL;
	GHashTable *a = murmel_attributes(host, port, username);
	secret_password_clearv_sync(murmel_schema(), a, NULL, &error);
	g_hash_table_unref(a);
	return error ? murmel_take_error(error) : NULL;
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

func takeError(msg *C.char) error {
	if msg == nil {
		return nil
	}
	defer C.g_free(C.gpointer(msg))
	return errors.New("secrets: " + C.GoString(msg))
}

// Store saves (or replaces) the password for k.
func Store(k Key, password string) error {
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
	return takeError(C.murmel_store(a.host, a.port, a.username, label, pw))
}

// Lookup returns the stored password for k ("" and no error when none is
// stored).
func Lookup(k Key) (string, error) {
	a := k.c()
	defer a.free()
	var pw *C.char
	if err := takeError(C.murmel_lookup(a.host, a.port, a.username, &pw)); err != nil {
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

// Delete removes the stored password for k (no error when none exists).
func Delete(k Key) error {
	a := k.c()
	defer a.free()
	return takeError(C.murmel_clear(a.host, a.port, a.username))
}
