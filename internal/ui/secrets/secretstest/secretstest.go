// Package secretstest keeps tests away from the desktop keyring.
//
// Every test program that links libsecret calls Isolate from its TestMain
// before any test runs (a test in this package checks that). Isolate
// takes away the session bus and points libsecret at a throwaway file, so
// no code path a test reaches can read or change the user's keyring, even
// when the tests run in a normal desktop session.
package secretstest

/*
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"
)

// file is the file libsecret uses while isolated.
var file string

// File returns the file libsecret writes to after Isolate.
func File() string { return file }

// unset are variables that could lead GLib or libsecret elsewhere.
var unset = []string{"DBUS_STARTER_ADDRESS", "DBUS_STARTER_BUS_TYPE", "SNAP_NAME"}

// Isolate sets up the process environment and returns a function that
// removes the throwaway directory. It ends the program when the
// environment cannot be set up: the tests must not run then.
func Isolate() (cleanup func()) {
	dir, err := os.MkdirTemp("", "murmel-secrets-test-")
	if err != nil {
		fail(err)
	}
	file = filepath.Join(dir, "test.keyring")
	set := map[string]string{
		// No session bus: neither the Secret Service nor the Secret
		// portal can be reached, and nothing is started on the desktop's
		// bus. GLib does not fall back to $XDG_RUNTIME_DIR/bus for it.
		"DBUS_SESSION_BUS_ADDRESS": "disabled:",
		// Any libsecret call goes to libsecret's own file backend, with
		// a file in dir and a fixed key instead of one from a portal.
		"SECRET_BACKEND":            "file",
		"SECRET_FILE_TEST_PATH":     file,
		"SECRET_FILE_TEST_PASSWORD": "test only",
	}
	for k, v := range set {
		if err := os.Setenv(k, v); err != nil {
			fail(err)
		}
	}
	for _, k := range unset {
		if err := os.Unsetenv(k); err != nil {
			fail(err)
		}
	}
	// libsecret reads the C environment: make sure it sees the same.
	for k, v := range set {
		if got := cgetenv(k); got != v {
			fail(fmt.Errorf("%s is %q for C code, want %q", k, got, v))
		}
	}
	for _, k := range unset {
		if got := cgetenv(k); got != "" {
			fail(fmt.Errorf("%s is still set for C code", k))
		}
	}
	return func() { _ = os.RemoveAll(dir) }
}

func cgetenv(k string) string {
	ck := C.CString(k)
	defer C.free(unsafe.Pointer(ck))
	return C.GoString(C.getenv(ck))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "secretstest: cannot keep the tests away from the keyring:", err)
	os.Exit(1)
}
