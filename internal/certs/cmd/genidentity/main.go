// Command genidentity generates an Murmel identity and prints a JSON
// summary. QA helper for cross-checking identities with openssl.
//
// TEST-ONLY helper; safe to run against a throwaway --out directory.
package main

import (
	"crypto/rsa"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"io.github/murmel/internal/certs"
)

func main() {
	name := flag.String("name", "", "username for the certificate CN (required)")
	email := flag.String("email", "", "optional emailAddress RDN")
	out := flag.String("out", "", "identity directory (parent must be named 'identities'); default: managed identities dir with random id")
	flag.Parse()

	if *name == "" {
		fail(fmt.Errorf("--name is required"))
	}

	var m *certs.Manager
	if *out != "" {
		dir := filepath.Clean(*out)
		if filepath.Base(filepath.Dir(dir)) != "identities" {
			fail(fmt.Errorf("--out must live under an 'identities' directory (got %s)", dir))
		}
		m, _ = certs.NewManager(certs.WithDataDir(filepath.Dir(filepath.Dir(dir))))
		id, err := m.Generate(*name, *email)
		if err != nil {
			fail(err)
		}
		if err := renameIdentity(m, id.Meta.ID, filepath.Base(dir)); err != nil {
			fail(err)
		}
		printSummary(m, filepath.Base(dir))
		return
	}

	m, err := certs.NewManager()
	if err != nil {
		fail(err)
	}
	id, err := m.Generate(*name, *email)
	if err != nil {
		fail(err)
	}
	printSummary(m, id.Meta.ID)
}

func renameIdentity(m *certs.Manager, from, to string) error {
	dir := m.DataDir()
	if err := os.Rename(filepath.Join(dir, "identities", from), filepath.Join(dir, "identities", to)); err != nil {
		return err
	}
	metaPath := filepath.Join(dir, "identities", to, "identity.json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		return err
	}
	meta["id"] = to
	raw, err = json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, raw, 0o644)
}

func printSummary(m *certs.Manager, id string) {
	ident, err := m.Load(id)
	if err != nil {
		fail(err)
	}
	bits := 0
	if k, ok := ident.PrivateKey.(*rsa.PrivateKey); ok {
		bits = k.N.BitLen()
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]any{
		"id":          id,
		"name":        ident.Meta.Name,
		"cn":          ident.Certificate.Subject.CommonName,
		"email":       ident.Meta.Email,
		"key_bits":    bits,
		"dir":         filepath.Join(m.DataDir(), "identities", id),
		"cert":        filepath.Join(m.DataDir(), "identities", id, "cert.pem"),
		"key":         filepath.Join(m.DataDir(), "identities", id, "key.pem"),
		"fingerprint": certs.Fingerprint(ident.Certificate),
	})
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "genidentity: %v\n", err)
	os.Exit(1)
}
