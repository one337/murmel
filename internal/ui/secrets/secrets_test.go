package secrets

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/one337/murmel/internal/ui/secrets/secretstest"
)

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		name   string
		domain string
		code   int
		want   Kind
	}{
		{"no secret service", dbusDomain, dbusServiceUnknown, Gone},
		{"name without owner", dbusDomain, dbusNameHasNoOwner, Gone},
		{"denied by policy", dbusDomain, dbusAccessDenied, Gone},
		{"service failed to start", dbusDomain, dbusSpawnExecFailed, Gone},
		{"service not found", dbusDomain, dbusSpawnServiceNotFound, Gone},
		{"other D-Bus error", dbusDomain, dbusFailed, Other},
		{"unlock cancelled", ioDomain, ioCancelled, Refused},
		{"keyring locked", secretDomain, secretIsLocked, Refused},
		{"same code, other domain", ioDomain, dbusServiceUnknown, Other},
		{"unknown domain", "g-file-error-quark", ioCancelled, Other},
	} {
		if got := classify(tc.domain, tc.code); got != tc.want {
			t.Errorf("%s: classify(%s, %d) = %d, want %d", tc.name, tc.domain, tc.code, got, tc.want)
		}
	}
}

func TestKindOf(t *testing.T) {
	err := fmt.Errorf("saving: %w", &Error{Kind: Refused, msg: "x"})
	if KindOf(err) != Refused {
		t.Errorf("KindOf(wrapped Refused) = %d", KindOf(err))
	}
	if KindOf(errors.New("plain")) != Other {
		t.Error("a plain error is not Other")
	}
	if KindOf(nil) != Other {
		t.Error("nil is not Other")
	}
}

// The real libsecret calls run in a child process: libsecret picks its
// backend once per process, from the environment the child gets. The
// child never sees the desktop's session bus.
const helperEnv = "MURMEL_SECRETS_TEST_HELPER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		// A child of runHelper: its whole environment was set there.
		os.Exit(helper(mode))
	}
	cleanup := secretstest.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// A Keyring call in the test program itself reaches only the throwaway
// file secretstest set up, never the desktop's keyring.
func TestThisProgramUsesTheTestFile(t *testing.T) {
	file := secretstest.File()
	k := Key{Host: "example.invalid", Port: 64738, Username: "isolated"}
	if err := (Keyring{}).Save(k, "pw"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("libsecret did not write the test file: %v", err)
	}
	if pw, err := (Keyring{}).Lookup(k); err != nil || pw != "pw" {
		t.Errorf("Lookup = %q, %v", pw, err)
	}
	if err := (Keyring{}).Delete(k); err != nil {
		t.Error(err)
	}
}

// result is one call of the child, as it reports it.
type result struct {
	Call string
	Got  string // the password a lookup returned
	Err  string
	Kind Kind
}

func helper(mode string) int {
	k := Key{Host: "example.invalid", Port: 64738, Username: "alice"}
	other := Key{Host: "example.invalid", Port: 64739, Username: "alice"}
	var s Store = Keyring{}
	enc := json.NewEncoder(os.Stdout)
	report := func(call, pw string, err error) {
		r := result{Call: call, Got: pw}
		if err != nil {
			r.Err, r.Kind = err.Error(), KindOf(err)
		}
		_ = enc.Encode(r)
	}
	switch mode {
	case "roundtrip":
		report("save", "", s.Save(k, "first"))
		pw, err := s.Lookup(k)
		report("lookup", pw, err)
		report("save again", "", s.Save(k, "second"))
		pw, err = s.Lookup(k)
		report("lookup again", pw, err)
		pw, err = s.Lookup(other)
		report("lookup other", pw, err)
		report("delete", "", s.Delete(k))
		pw, err = s.Lookup(k)
		report("lookup deleted", pw, err)
		report("delete again", "", s.Delete(k))
	case "fail":
		report("save", "", s.Save(k, "first"))
		pw, err := s.Lookup(k)
		report("lookup", pw, err)
		report("delete", "", s.Delete(k))
	default:
		return 2
	}
	return 0
}

// runHelper runs the child with only the variables in env (and a HOME and
// XDG directories of its own) and returns its reports by call.
func runHelper(t *testing.T, mode string, env ...string) map[string]result {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append([]string{
		helperEnv + "=" + mode,
		"HOME=" + dir,
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		"XDG_DATA_HOME=" + filepath.Join(dir, "data"),
		"XDG_CACHE_HOME=" + filepath.Join(dir, "cache"),
		"XDG_RUNTIME_DIR=" + dir,
	}, env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper %s: %v\n%s", mode, err, out)
	}
	got := map[string]result{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		var r result
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("helper output %q: %v", sc.Text(), err)
		}
		got[r.Call] = r
	}
	return got
}

// Passwords go through libsecret and come back, here into libsecret's own
// encrypted file (the backend it uses in a sandbox).
func TestKeyringRoundtrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "test.keyring")
	got := runHelper(t, "roundtrip",
		"DBUS_SESSION_BUS_ADDRESS=disabled:",
		"SECRET_BACKEND=file",
		"SECRET_FILE_TEST_PATH="+file,
		"SECRET_FILE_TEST_PASSWORD=test only")
	for call, want := range map[string]string{
		"save": "", "lookup": "first", "save again": "", "lookup again": "second",
		"lookup other": "", "delete": "", "lookup deleted": "", "delete again": "",
	} {
		r, ok := got[call]
		switch {
		case !ok:
			t.Errorf("%s: no report", call)
		case r.Err != "":
			t.Errorf("%s: %s", call, r.Err)
		case r.Got != want:
			t.Errorf("%s: password %q, want %q", call, r.Got, want)
		}
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("libsecret did not use the test file: %v", err)
	}
}

// A file libsecret cannot create is an Other failure.
func TestKeyringOtherFailure(t *testing.T) {
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got := runHelper(t, "fail",
		"DBUS_SESSION_BUS_ADDRESS=disabled:",
		"SECRET_BACKEND=file",
		"SECRET_FILE_TEST_PATH="+filepath.Join(notDir, "test.keyring"),
		"SECRET_FILE_TEST_PASSWORD=test only")
	for _, call := range []string{"save", "lookup", "delete"} {
		if r := got[call]; r.Err == "" || r.Kind != Other {
			t.Errorf("%s: %+v, want an Other failure", call, r)
		}
	}
}

// On a session bus without a Secret Service, and none to start, every call
// fails as Gone.
func TestKeyringWithoutSecretService(t *testing.T) {
	addr := privateBus(t)
	got := runHelper(t, "fail",
		"DBUS_SESSION_BUS_ADDRESS="+addr,
		"SECRET_BACKEND=service")
	for _, call := range []string{"save", "lookup", "delete"} {
		if r := got[call]; r.Err == "" || r.Kind != Gone {
			t.Errorf("%s: %+v, want a Gone failure", call, r)
		}
	}
}

// privateBus starts a dbus-daemon that can start no services and returns
// its address; the test stops it. Without dbus-daemon the test is skipped,
// except in CI, where it fails.
func privateBus(t *testing.T) string {
	t.Helper()
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("dbus-daemon is needed")
		}
		t.Skip("no dbus-daemon")
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "session.conf")
	// No <servicedir> and no standard ones: nothing can be started.
	err = os.WriteFile(conf, []byte(`<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:dir=`+dir+`</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(daemon, "--config-file="+conf, "--nofork", "--print-address=1")
	cmd.Env = []string{"HOME=" + dir}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line := make(chan string, 1)
	go func() {
		l, _ := bufio.NewReader(stdout).ReadString('\n')
		line <- strings.TrimSpace(l)
	}()
	select {
	case addr := <-line:
		if addr == "" {
			t.Fatal("dbus-daemon printed no address")
		}
		return addr
	case <-time.After(10 * time.Second):
		t.Fatal("dbus-daemon did not start")
	}
	return ""
}
