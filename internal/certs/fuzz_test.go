package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

// fuzzSeedFiles are identity files to start from: PEM made here, PKCS#12
// from testdata.
func fuzzSeedFiles(f *testing.F) [][]byte {
	f.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		f.Fatal(err)
	}
	c := testCert(f, "Fuzz", key, nil, nil, false)
	pemFile := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	pemFile = append(pemFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})...)
	files := [][]byte{pemFile}
	for _, name := range []string{"openssl-nomac.p12", "mumble-format.p12", "openssl-default.p12"} {
		data, err := os.ReadFile(filepath.Join("testdata", "p12", name))
		if err != nil {
			f.Fatal(err)
		}
		files = append(files, data)
	}
	return files
}

// FuzzReadIdentity feeds arbitrary files and passwords to ReadIdentity;
// it may reject them but must not panic.
func FuzzReadIdentity(f *testing.F) {
	for _, data := range fuzzSeedFiles(f) {
		f.Add(data, "")
		f.Add(data, testPassword)
	}
	f.Fuzz(func(t *testing.T, data []byte, password string) {
		_, _ = ReadIdentity(data, password)
	})
}

// FuzzParsePEMBundle feeds arbitrary PEM to parsePEMBundle; it may reject
// it but must not panic.
func FuzzParsePEMBundle(f *testing.F) {
	f.Add(fuzzSeedFiles(f)[0])
	f.Add([]byte("-----BEGIN RSA PRIVATE KEY-----\nAA==\n-----END RSA PRIVATE KEY-----\n"))
	f.Add([]byte("-----BEGIN EC PRIVATE KEY-----\nAA==\n-----END EC PRIVATE KEY-----\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _ = parsePEMBundle(data)
	})
}

// FuzzCheckP12Iterations feeds arbitrary files to the round-count check
// that runs before a PKCS#12 file is opened; it must not panic.
func FuzzCheckP12Iterations(f *testing.F) {
	for _, data := range fuzzSeedFiles(f)[1:] {
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = checkP12Iterations(data)
	})
}
