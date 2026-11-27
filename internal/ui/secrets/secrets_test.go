package secrets

import (
	"os"
	"testing"

	"github.com/one337/murmel/internal/ui/secrets/secretstest"
)

func TestMain(m *testing.M) {
	cleanup := secretstest.Isolate()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// A libsecret call in the test program itself reaches only the throwaway
// file secretstest set up, never the desktop's keyring.
func TestThisProgramUsesTheTestFile(t *testing.T) {
	file := secretstest.File()
	k := Key{Host: "example.invalid", Port: 64738, Username: "isolated"}
	if err := Store(k, "pw"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("libsecret did not write the test file: %v", err)
	}
	if pw, err := Lookup(k); err != nil || pw != "pw" {
		t.Errorf("Lookup = %q, %v", pw, err)
	}
	if err := Delete(k); err != nil {
		t.Error(err)
	}
}
