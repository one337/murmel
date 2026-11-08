package certs

import (
	"bytes"
	"crypto/sha256"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportImportRoundtripConcatenated(t *testing.T) {
	m := newTestManager(t)

	id, err := m.Generate("ExportMe", "export@example.com")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	blob, err := m.ExportPEM(id.Meta.ID)
	if err != nil {
		t.Fatalf("ExportPEM: %v", err)
	}
	if !bytes.Contains(blob, []byte("BEGIN CERTIFICATE")) {
		t.Error("export missing CERTIFICATE block")
	}
	if !bytes.Contains(blob, []byte("PRIVATE KEY")) {
		t.Error("export missing PRIVATE KEY block")
	}

	m2 := newTestManager(t)
	got, err := m2.ImportPEM("Imported", blob)
	if err != nil {
		t.Fatalf("ImportPEM: %v", err)
	}
	if got.Certificate.Subject.CommonName != "ExportMe" {
		t.Errorf("imported CN = %q, want ExportMe (cert content preserved)", got.Certificate.Subject.CommonName)
	}
	if got.Meta.Name != "Imported" {
		t.Errorf("imported Meta.Name = %q, want Imported (local name may differ from CN)", got.Meta.Name)
	}
	wantSum := sha256.Sum256(id.Certificate.Raw)
	gotSum := sha256.Sum256(got.Certificate.Raw)
	if wantSum != gotSum {
		t.Error("imported cert DER differs from exported cert DER")
	}
}

func TestImportSeparateCertAndKeyFiles(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Generate("SepFiles", "")

	blob, err := m.ExportPEM(id.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Split the combined export to exercise the second import layout:
	// separate cert/key files, as the official Mumble client stores them.
	var certPEM, keyPEM []byte
	rest := blob
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			certPEM = pem.EncodeToMemory(block)
		} else {
			keyPEM = pem.EncodeToMemory(block)
		}
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	os.WriteFile(certPath, certPEM, 0o600)
	os.WriteFile(keyPath, keyPEM, 0o600)

	m2 := newTestManager(t)
	got, err := m2.ImportFiles("FromFiles", certPath, keyPath)
	if err != nil {
		t.Fatalf("ImportFiles: %v", err)
	}
	if got.Certificate.Subject.CommonName != "SepFiles" {
		t.Errorf("CN = %q", got.Certificate.Subject.CommonName)
	}
}

func TestImportConcatenatedFileViaImportFiles(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Generate("OneFile", "")
	blob, _ := m.ExportPEM(id.Meta.ID)

	dir := t.TempDir()
	combined := filepath.Join(dir, "mumble-export.pem")
	if err := os.WriteFile(combined, blob, 0o600); err != nil {
		t.Fatal(err)
	}

	m2 := newTestManager(t)
	got, err := m2.ImportFiles("Combined", combined, "")
	if err != nil {
		t.Fatalf("ImportFiles with single combined file: %v", err)
	}
	if got.Certificate.Subject.CommonName != "OneFile" {
		t.Errorf("CN = %q", got.Certificate.Subject.CommonName)
	}
}

func TestImportRejectsKeyMismatch(t *testing.T) {
	m := newTestManager(t)
	a, _ := m.Generate("CertOwner", "")
	b, _ := m.Generate("KeyOwner", "")

	certBlock := &pem.Block{Type: "CERTIFICATE", Bytes: a.Certificate.Raw}
	keyDER, err := x509MarshalPrivateKey(b.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyBlock := &pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}
	blob := append(pem.EncodeToMemory(certBlock), pem.EncodeToMemory(keyBlock)...)

	m2 := newTestManager(t)
	if _, err := m2.ImportPEM("Mismatch", blob); err == nil {
		t.Fatal("import of cert with non-matching key must fail")
	} else if !strings.Contains(err.Error(), "match") {
		t.Errorf("error should mention key/cert mismatch, got: %v", err)
	}
}

func TestImportRejectsMissingKey(t *testing.T) {
	m := newTestManager(t)
	id, _ := m.Generate("NoKey", "")

	certOnly := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.Certificate.Raw})
	m2 := newTestManager(t)
	if _, err := m2.ImportPEM("NoKey", certOnly); err == nil {
		t.Fatal("import without a private key must fail")
	}
}
