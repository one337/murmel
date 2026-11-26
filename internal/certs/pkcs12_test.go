package certs

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

const testPassword = "Murmel-Test-ä1"

// testCert returns a throwaway certificate for key, signed by parent
// (self-signed when parent is nil).
func testCert(t testing.TB, cn string, key crypto.Signer, parent *x509.Certificate, parentKey crypto.Signer, isCA bool) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  isCA,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, key.Public(), parentKey)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func encode(t *testing.T, enc *pkcs12.Encoder, key crypto.PrivateKey, c *x509.Certificate, chain []*x509.Certificate, pw string) []byte {
	t.Helper()
	data, err := enc.Encode(key, c, chain, pw)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A backup made by Murmel reads back as the same certificate, with its
// e-mail address and name, and the private key that belongs to it.
func TestP12ExportImportRoundtrip(t *testing.T) {
	m := newTestManager(t)
	orig, err := m.Generate("Roundtrip", "rt@example.com")
	if err != nil {
		t.Fatal(err)
	}
	data, err := m.ExportP12(orig.Meta.ID, testPassword)
	if err != nil {
		t.Fatalf("ExportP12: %v", err)
	}
	b, err := ReadIdentity(data, testPassword)
	if err != nil {
		t.Fatalf("ReadIdentity: %v", err)
	}
	if b.CommonName() != "Roundtrip" || b.Fingerprint() != Fingerprint(orig.Certificate) {
		t.Fatalf("read back %q %s, want Roundtrip %s", b.CommonName(), b.Fingerprint(), Fingerprint(orig.Certificate))
	}
	m2 := newTestManager(t)
	got, err := m2.Store("Restored", b)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if got.Meta.Email != "rt@example.com" || got.Meta.Name != "Restored" {
		t.Errorf("meta = %+v", got.Meta)
	}
	if string(got.TLSCertificate.Certificate[0]) != string(orig.Certificate.Raw) {
		t.Error("stored certificate differs from the original DER")
	}
	if err := ensureKeyMatches(got.Certificate, got.PrivateKey); err != nil {
		t.Error(err)
	}
}

// Backups use AES-256-CBC with PBKDF2-HMAC-SHA-256 and a SHA-256 MAC,
// 100 000 rounds each: what OpenSSL 3 and the official Mumble client read.
func TestP12ExportAlgorithms(t *testing.T) {
	m := newTestManager(t)
	orig, err := m.Generate("Algorithms", "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := m.ExportP12(orig.Meta.ID, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	var pfx p12PFX
	if err := unmarshalDER(data, &pfx); err != nil {
		t.Fatal(err)
	}
	sha256OID := asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	if !pfx.MacData.Mac.Algorithm.Algorithm.Equal(sha256OID) || pfx.MacData.Iterations.Int64() != exportIterations {
		t.Errorf("MAC = %v, %v rounds", pfx.MacData.Mac.Algorithm.Algorithm, pfx.MacData.Iterations)
	}
	var authSafe []byte
	var safes []p12ContentInfo
	if err := unmarshalDER(pfx.AuthSafe.Content.Bytes, &authSafe); err != nil {
		t.Fatal(err)
	}
	if err := unmarshalDER(authSafe, &safes); err != nil {
		t.Fatal(err)
	}
	var algs []pkix.AlgorithmIdentifier
	for _, ci := range safes {
		switch {
		case ci.ContentType.Equal(oidP12EncryptedData):
			var ed p12EncryptedData
			if err := unmarshalDER(ci.Content.Bytes, &ed); err != nil {
				t.Fatal(err)
			}
			algs = append(algs, ed.EncryptedContentInfo.ContentEncryptionAlgorithm)
		case ci.ContentType.Equal(oidP12Data):
			var raw []byte
			var bags []p12SafeBag
			if err := unmarshalDER(ci.Content.Bytes, &raw); err != nil {
				t.Fatal(err)
			}
			if err := unmarshalDER(raw, &bags); err != nil {
				t.Fatal(err)
			}
			for _, bag := range bags {
				if !bag.ID.Equal(oidP12ShroudedKey) {
					t.Errorf("unencrypted bag %v in a backup", bag.ID)
					continue
				}
				var epki p12EncryptedPrivateKeyInfo
				if err := unmarshalDER(bag.Value.Bytes, &epki); err != nil {
					t.Fatal(err)
				}
				algs = append(algs, epki.Algorithm)
			}
		}
	}
	if len(algs) != 2 {
		t.Fatalf("%d encrypted parts, want certificates and key", len(algs))
	}
	for _, alg := range algs {
		var p pbes2Params
		var k struct {
			Salt       []byte
			Iterations int
			PRF        pkix.AlgorithmIdentifier
		}
		var scheme pkix.AlgorithmIdentifier
		if !alg.Algorithm.Equal(oidPBES2) {
			t.Fatalf("encryption %v, want PBES2", alg.Algorithm)
		}
		if err := unmarshalDER(alg.Parameters.FullBytes, &p); err != nil {
			t.Fatal(err)
		}
		if err := unmarshalDER(p.KeyDerivationFunc.Parameters.FullBytes, &k); err != nil {
			t.Fatal(err)
		}
		if err := unmarshalDER(p.Scheme.FullBytes, &scheme); err != nil {
			t.Fatal(err)
		}
		hmacSHA256 := asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 9}
		aes256CBC := asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42}
		if !p.KeyDerivationFunc.Algorithm.Equal(oidPBKDF2) || k.Iterations != exportIterations ||
			!k.PRF.Algorithm.Equal(hmacSHA256) || !scheme.Algorithm.Equal(aes256CBC) {
			t.Errorf("KDF %v (%d rounds, PRF %v), cipher %v", p.KeyDerivationFunc.Algorithm, k.Iterations, k.PRF.Algorithm, scheme.Algorithm)
		}
	}
}

func TestP12ExportPasswordRules(t *testing.T) {
	m := newTestManager(t)
	orig, err := m.Generate("Rules", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ExportP12(orig.Meta.ID, ""); !errors.Is(err, ErrEmptyPassword) {
		t.Errorf("empty password: %v, want ErrEmptyPassword", err)
	}
	if _, err := m.ExportP12(orig.Meta.ID, "geheim🔑"); !errors.Is(err, ErrPasswordCharacters) {
		t.Errorf("emoji password: %v, want ErrPasswordCharacters", err)
	}
}

// Files written with old and new algorithms, with or without password,
// all open.
func TestReadIdentityP12Variants(t *testing.T) {
	key := rsaKey(t)
	c := testCert(t, "Variants", key, nil, nil, false)
	for _, tc := range []struct {
		name string
		enc  *pkcs12.Encoder
		pw   string
	}{
		{"legacy RC2", pkcs12.LegacyRC2, testPassword},
		{"legacy 3DES", pkcs12.LegacyDES, testPassword},
		{"modern 2023", pkcs12.Modern2023, testPassword},
		{"modern 2026 (PBMAC1)", pkcs12.Modern2026, testPassword},
		{"modern 2023, empty password", pkcs12.Modern2023, ""},
		{"no encryption, no MAC", pkcs12.Passwordless, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ReadIdentity(encode(t, tc.enc, key, c, nil, tc.pw), tc.pw)
			if err != nil {
				t.Fatal(err)
			}
			if b.Fingerprint() != Fingerprint(c) {
				t.Error("wrong certificate")
			}
		})
	}
}

// The files in testdata/p12 come from OpenSSL (see gen.sh).
func TestReadIdentityOpenSSLFiles(t *testing.T) {
	for _, tc := range []struct {
		file, pw string
		want     error
	}{
		{"openssl-default.p12", testPassword, nil},
		{"openssl-legacy.p12", testPassword, nil},
		{"openssl-nomac.p12", "", nil},
		{"mumble-format.p12", "", nil},
		{"openssl-default.p12", "", ErrWrongPassword},
		{"openssl-default.p12", "wrong", ErrWrongPassword},
		{"mumble-format-ber.p12", "", ErrUnsupportedFile},
		{"mumble-format-iterations.p12", "", ErrTooManyIterations},
	} {
		t.Run(tc.file+"/"+tc.pw, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "p12", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			b, err := ReadIdentity(data, tc.pw)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.want == nil && b.CommonName() != "Test OpenSSL" {
				t.Errorf("CN = %q", b.CommonName())
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Errorf("took %v", d)
			}
		})
	}
}

func TestReadIdentityWrongPassword(t *testing.T) {
	key := rsaKey(t)
	c := testCert(t, "Locked", key, nil, nil, false)
	data := encode(t, pkcs12.Modern2023, key, c, nil, testPassword)
	for _, pw := range []string{"", "wrong", "Murmel-Test-a1", "🔑"} {
		if _, err := ReadIdentity(data, pw); !errors.Is(err, ErrWrongPassword) {
			t.Errorf("password %q: %v, want ErrWrongPassword", pw, err)
		}
	}
}

// Damaged files fail with an error, never a panic or a stored identity.
func TestReadIdentityDamaged(t *testing.T) {
	key := rsaKey(t)
	c := testCert(t, "Damaged", key, nil, nil, false)
	data := encode(t, pkcs12.Modern2023, key, c, nil, testPassword)
	for i := 1; i < len(data); i += 97 {
		if _, err := ReadIdentity(data[:i], testPassword); err == nil {
			t.Fatalf("file cut to %d bytes opened", i)
		} else if !errors.Is(err, ErrDamagedFile) && !errors.Is(err, ErrUnsupportedFile) {
			t.Fatalf("cut to %d bytes: %v", i, err)
		}
	}
	for i := 0; i < len(data); i += 13 {
		flipped := append([]byte(nil), data...)
		flipped[i] ^= 0x40
		if _, err := ReadIdentity(flipped, testPassword); err == nil {
			t.Fatalf("file with byte %d changed opened", i)
		}
	}
	for _, junk := range [][]byte{nil, []byte("hello"), {0x30}, {0x30, 0x03, 0x02, 0x01, 0x03}} {
		if _, err := ReadIdentity(junk, ""); err == nil {
			t.Errorf("%q opened", junk)
		}
	}
}

// A file that asks for more key derivation rounds than any real backup is
// refused before any key is derived.
func TestReadIdentityTooManyIterations(t *testing.T) {
	key := rsaKey(t)
	c := testCert(t, "Rounds", key, nil, nil, false)
	data := encode(t, pkcs12.Modern2023, key, c, nil, testPassword)
	var pfx p12PFX
	if err := unmarshalDER(data, &pfx); err != nil {
		t.Fatal(err)
	}
	pfx.MacData.Iterations = big.NewInt(1<<31 - 1)
	raised, err := asn1.Marshal(pfx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadIdentity(raised, testPassword); !errors.Is(err, ErrTooManyIterations) {
		t.Fatalf("raised MAC rounds: %v", err)
	}

	tooMany := big.NewInt(maxIterations + 1)
	kdf, _ := asn1.Marshal(struct {
		Salt       []byte
		Iterations *big.Int
	}{[]byte("saltsalt"), tooMany})
	pbes2, _ := asn1.Marshal(struct {
		KDF    pkix.AlgorithmIdentifier
		Scheme pkix.AlgorithmIdentifier
	}{pkix.AlgorithmIdentifier{Algorithm: oidPBKDF2, Parameters: asn1.RawValue{FullBytes: kdf}}, pkix.AlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42}}})
	pbe, _ := asn1.Marshal(pkcs12PBEParams{Salt: []byte("saltsalt"), Iterations: tooMany})
	for name, alg := range map[string]pkix.AlgorithmIdentifier{
		"PBES2":       {Algorithm: oidPBES2, Parameters: asn1.RawValue{FullBytes: pbes2}},
		"PKCS#12 PBE": {Algorithm: append(append(asn1.ObjectIdentifier{}, oidP12PBEPrefix...), 3), Parameters: asn1.RawValue{FullBytes: pbe}},
	} {
		if err := checkAlgorithm(alg); !errors.Is(err, ErrTooManyIterations) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReadIdentityECAndChain(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ec := testCert(t, "EC", ecKey, nil, nil, false)
	b, err := ReadIdentity(encode(t, pkcs12.Modern2023, ecKey, ec, nil, testPassword), testPassword)
	if err != nil || b.Fingerprint() != Fingerprint(ec) {
		t.Fatalf("EC: %v", err)
	}

	caKey, leafKey := rsaKey(t), rsaKey(t)
	ca := testCert(t, "CA", caKey, nil, nil, true)
	leaf := testCert(t, "Leaf", leafKey, ca, caKey, false)
	b, err = ReadIdentity(encode(t, pkcs12.Modern2023, leafKey, leaf, []*x509.Certificate{ca}, testPassword), testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if b.CommonName() != "Leaf" || len(b.certs) != 2 || string(b.certs[1]) != string(ca.Raw) {
		t.Fatalf("chain read as %q with %d certificates", b.CommonName(), len(b.certs))
	}
	// The certificate that belongs to the key comes first, wherever it is
	// in the file.
	b, err = newBundle([][]byte{ca.Raw, leaf.Raw}, leafKey)
	if err != nil || b.CommonName() != "Leaf" || string(b.certs[1]) != string(ca.Raw) {
		t.Fatalf("CA first: %v", err)
	}
	m := newTestManager(t)
	ident, err := m.Store("Chain", b)
	if err != nil {
		t.Fatal(err)
	}
	if len(ident.TLSCertificate.Certificate) != 2 {
		t.Errorf("stored chain has %d certificates", len(ident.TLSCertificate.Certificate))
	}
}

func TestReadIdentityKeyMismatch(t *testing.T) {
	key, other := rsaKey(t), rsaKey(t)
	c := testCert(t, "Mismatch", key, nil, nil, false)
	if _, err := ReadIdentity(encode(t, pkcs12.Modern2023, other, c, nil, testPassword), testPassword); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("%v, want ErrKeyMismatch", err)
	}
}

func TestReadIdentityPEM(t *testing.T) {
	key := rsaKey(t)
	c := testCert(t, "PEM", key, nil, nil, false)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	// Key before certificate: the order in the file does not matter.
	data := append(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	b, err := ReadIdentity(data, "ignored")
	if err != nil || b.CommonName() != "PEM" {
		t.Fatalf("PEM: %v", err)
	}
	if _, err := ReadIdentity(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}), ""); !errors.Is(err, ErrNoKey) {
		t.Errorf("certificate only: %v", err)
	}
	if _, err := ReadIdentity([]byte("-----BEGIN NOTHING-----\n"), ""); !errors.Is(err, ErrUnsupportedFile) {
		t.Errorf("no PEM blocks: %v", err)
	}
}

// Importing a certificate that is already stored adds no second copy.
func TestStoreDuplicate(t *testing.T) {
	key := rsaKey(t)
	c := testCert(t, "Twice", key, nil, nil, false)
	data := encode(t, pkcs12.Modern2023, key, c, nil, testPassword)
	m := newTestManager(t)
	b, err := ReadIdentity(data, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Store("First", b)
	if err != nil {
		t.Fatal(err)
	}
	var dup *DuplicateError
	if _, err := m.Store("Second", b); !errors.As(err, &dup) || dup.Meta.ID != first.Meta.ID {
		t.Fatalf("second import: %v", err)
	}
	if list, _ := m.List(); len(list) != 1 {
		t.Errorf("%d identities stored", len(list))
	}
}
