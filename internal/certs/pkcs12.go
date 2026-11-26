package certs

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1" //nolint:gosec // PKCS#12 files of the official client use SHA-1 for the MAC and key derivation
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"hash"
	"strings"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

// Errors reading or writing an identity file. Their texts name no file
// content, password or key.
var (
	// ErrWrongPassword: the password does not open the file (a damaged
	// file fails the same check).
	ErrWrongPassword = errors.New("certs: wrong password")
	// ErrUnsupportedFile: neither PEM nor a PKCS#12 file Murmel can read
	// (BER encoding among others).
	ErrUnsupportedFile = errors.New("certs: unsupported file format")
	// ErrDamagedFile: the file is cut off or not well-formed.
	ErrDamagedFile = errors.New("certs: damaged file")
	// ErrTooManyIterations: the file asks for more key derivation rounds
	// than any real backup uses.
	ErrTooManyIterations = errors.New("certs: too many key derivation iterations")
	// ErrNoCertificate and ErrNoKey: the file lacks the certificate or the
	// private key.
	ErrNoCertificate = errors.New("certs: no certificate in the file")
	ErrNoKey         = errors.New("certs: no private key in the file")
	// ErrKeyMismatch: no certificate in the file belongs to the key.
	ErrKeyMismatch = errors.New("certs: private key does not match certificate")
	// ErrUnreadableCertificate: a PKCS#12 file with a certificate Go cannot
	// parse, encrypted with a legacy scheme.
	ErrUnreadableCertificate = errors.New("certs: certificate cannot be read")
	// ErrEmptyPassword: backups are never written without a password.
	ErrEmptyPassword = errors.New("certs: backup password is empty")
	// ErrPasswordCharacters: the password has characters a PKCS#12 file
	// cannot hold (outside the Basic Multilingual Plane, such as emoji).
	ErrPasswordCharacters = errors.New("certs: password has unsupported characters")
)

// exportIterations is the PBKDF2 round count for backups: 50 times
// OpenSSL's default, still quick to open in the official Mumble client,
// which tries the password on every key press.
const exportIterations = 100_000

// maxIterations bounds the key derivation rounds a file may ask for
// (a few seconds of work); the library itself has no limit.
const maxIterations = 10_000_000

// ExportP12 returns identity id as a PKCS#12 file protected by password:
// certificate chain and private key encrypted with AES-256-CBC, keys
// derived with PBKDF2-HMAC-SHA-256, SHA-256 MAC. OpenSSL 1.1.1 and later
// read it, so does the official Mumble client.
func (m *Manager) ExportP12(id, password string) ([]byte, error) {
	if password == "" {
		return nil, ErrEmptyPassword
	}
	ident, err := m.Load(id)
	if err != nil {
		return nil, err
	}
	var chain []*x509.Certificate
	for _, der := range ident.TLSCertificate.Certificate[1:] {
		c, err := parseOwnCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("certs: identity %s: parsing chain: %w", id, err)
		}
		chain = append(chain, c)
	}
	data, err := pkcs12.Modern2023.WithIterations(exportIterations).Encode(ident.PrivateKey, ident.Certificate, chain, password)
	if err != nil {
		if isPasswordEncodingError(err) {
			return nil, ErrPasswordCharacters
		}
		return nil, fmt.Errorf("certs: encoding backup: %w", err)
	}
	return data, nil
}

// decodeP12 reads a PKCS#12 file: the certificates (DER, in file order)
// and the private key.
func decodeP12(data []byte, password string) (certDERs [][]byte, key crypto.PrivateKey, err error) {
	if err := checkP12Iterations(data); err != nil {
		return nil, nil, err
	}
	key, leaf, chain, err := pkcs12.DecodeChain(data, password)
	if err != nil {
		if strings.HasPrefix(err.Error(), "x509: ") {
			// DecodeChain checked the MAC and decrypted the file before it
			// parsed the certificates, so the password is right. Go's parser
			// refuses certificates the official Mumble client writes with a
			// UTF-8 e-mail address: such files are read here.
			return readP12Raw(data, password)
		}
		return nil, nil, p12Error(err)
	}
	certDERs = append(certDERs, leaf.Raw)
	for _, c := range chain {
		certDERs = append(certDERs, c.Raw)
	}
	return certDERs, key, nil
}

// p12Error maps the library's errors to the ones above.
func p12Error(err error) error {
	var notImpl pkcs12.NotImplementedError
	switch {
	case errors.Is(err, pkcs12.ErrIncorrectPassword), errors.Is(err, pkcs12.ErrDecryption):
		return ErrWrongPassword
	case isPasswordEncodingError(err):
		return ErrWrongPassword
	case errors.As(err, &notImpl):
		return ErrUnsupportedFile
	case strings.Contains(err.Error(), "certificate missing"):
		return ErrNoCertificate
	case strings.Contains(err.Error(), "private key missing"):
		return ErrNoKey
	case isBER(err):
		return ErrUnsupportedFile
	}
	return fmt.Errorf("%w (%v)", ErrDamagedFile, err)
}

func isPasswordEncodingError(err error) bool {
	return strings.Contains(err.Error(), "cannot be encoded in UCS-2")
}

func isBER(err error) bool {
	return strings.Contains(err.Error(), "indefinite length")
}

var (
	oidP12KeyBag  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 1}
	oidP12CertBag = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 3}
	oidX509Cert   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 22, 1}
)

type p12CertBag struct {
	ID    asn1.ObjectIdentifier
	Value asn1.RawValue `asn1:"tag:0,explicit"`
}

// readP12Raw reads the certificates (unparsed) and the private key of a
// PKCS#12 file whose MAC and password the caller has checked. It reads
// what the official Mumble client writes (no encryption) and what Murmel
// and OpenSSL 3 write (PBES2); legacy schemes fail with
// ErrUnreadableCertificate.
func readP12Raw(data []byte, password string) (certDERs [][]byte, key crypto.PrivateKey, err error) {
	var pfx p12PFX
	if err := unmarshalDER(data, &pfx); err != nil {
		return nil, nil, err
	}
	var authSafe []byte
	var safes []p12ContentInfo
	if err := unmarshalDER(pfx.AuthSafe.Content.Bytes, &authSafe); err != nil {
		return nil, nil, err
	}
	if err := unmarshalDER(authSafe, &safes); err != nil {
		return nil, nil, err
	}
	for _, ci := range safes {
		var raw []byte
		switch {
		case ci.ContentType.Equal(oidP12Data):
			if err := unmarshalDER(ci.Content.Bytes, &raw); err != nil {
				return nil, nil, err
			}
		case ci.ContentType.Equal(oidP12EncryptedData):
			var ed p12EncryptedData
			if err := unmarshalDER(ci.Content.Bytes, &ed); err != nil {
				return nil, nil, err
			}
			eci := ed.EncryptedContentInfo
			if raw, err = decryptPBES2(eci.ContentEncryptionAlgorithm, password, eci.EncryptedContent.Bytes); err != nil {
				return nil, nil, err
			}
		default:
			return nil, nil, ErrUnreadableCertificate
		}
		var bags []p12SafeBag
		if err := unmarshalDER(raw, &bags); err != nil {
			return nil, nil, err
		}
		for _, bag := range bags {
			switch {
			case bag.ID.Equal(oidP12CertBag):
				var cb p12CertBag
				if err := unmarshalDER(bag.Value.Bytes, &cb); err != nil {
					return nil, nil, err
				}
				if !cb.ID.Equal(oidX509Cert) {
					continue
				}
				var der []byte
				if err := unmarshalDER(cb.Value.Bytes, &der); err != nil {
					return nil, nil, err
				}
				certDERs = append(certDERs, der)
			case bag.ID.Equal(oidP12KeyBag):
				if key != nil {
					return nil, nil, fmt.Errorf("%w (more than one key)", ErrDamagedFile)
				}
				if key, err = x509.ParsePKCS8PrivateKey(bag.Value.Bytes); err != nil {
					return nil, nil, fmt.Errorf("%w (private key: %v)", ErrDamagedFile, err)
				}
			case bag.ID.Equal(oidP12ShroudedKey):
				if key != nil {
					return nil, nil, fmt.Errorf("%w (more than one key)", ErrDamagedFile)
				}
				var epki p12EncryptedPrivateKeyInfo
				if err := unmarshalDER(bag.Value.Bytes, &epki); err != nil {
					return nil, nil, err
				}
				plain, err := decryptPBES2(epki.Algorithm, password, epki.EncryptedData)
				if err != nil {
					return nil, nil, err
				}
				if key, err = x509.ParsePKCS8PrivateKey(plain); err != nil {
					return nil, nil, fmt.Errorf("%w (private key: %v)", ErrDamagedFile, err)
				}
			}
		}
	}
	return certDERs, key, nil
}

var (
	oidHMACSHA1   = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 7}
	oidHMACSHA256 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 9}
	oidHMACSHA512 = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 11}
	oidAES128CBC  = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 2}
	oidAES192CBC  = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 22}
	oidAES256CBC  = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42}
)

// decryptPBES2 decrypts data encrypted with PBES2 (RFC 8018): PBKDF2 with
// HMAC-SHA-1, -256 or -512 and AES-CBC, the password as UTF-8 (RFC 9579).
// Other schemes fail with ErrUnreadableCertificate.
func decryptPBES2(alg pkix.AlgorithmIdentifier, password string, data []byte) ([]byte, error) {
	if !alg.Algorithm.Equal(oidPBES2) {
		return nil, ErrUnreadableCertificate
	}
	var params struct {
		KDF    pkix.AlgorithmIdentifier
		Scheme pkix.AlgorithmIdentifier
	}
	var kdf struct {
		Salt       []byte
		Iterations int
		KeyLength  int                      `asn1:"optional"`
		PRF        pkix.AlgorithmIdentifier `asn1:"optional"`
	}
	if err := unmarshalDER(alg.Parameters.FullBytes, &params); err != nil {
		return nil, err
	}
	if !params.KDF.Algorithm.Equal(oidPBKDF2) {
		return nil, ErrUnreadableCertificate
	}
	if err := unmarshalDER(params.KDF.Parameters.FullBytes, &kdf); err != nil {
		return nil, err
	}
	if kdf.Iterations < 1 || kdf.Iterations > maxIterations {
		return nil, ErrTooManyIterations
	}
	var prf func() hash.Hash
	switch {
	case kdf.PRF.Algorithm == nil, kdf.PRF.Algorithm.Equal(oidHMACSHA1):
		prf = sha1.New
	case kdf.PRF.Algorithm.Equal(oidHMACSHA256):
		prf = sha256.New
	case kdf.PRF.Algorithm.Equal(oidHMACSHA512):
		prf = sha512.New
	default:
		return nil, ErrUnreadableCertificate
	}
	var keyLen int
	switch {
	case params.Scheme.Algorithm.Equal(oidAES128CBC):
		keyLen = 16
	case params.Scheme.Algorithm.Equal(oidAES192CBC):
		keyLen = 24
	case params.Scheme.Algorithm.Equal(oidAES256CBC):
		keyLen = 32
	default:
		return nil, ErrUnreadableCertificate
	}
	var iv []byte
	if err := unmarshalDER(params.Scheme.Parameters.FullBytes, &iv); err != nil {
		return nil, err
	}
	key, err := pbkdf2.Key(prf, password, kdf.Salt, kdf.Iterations, keyLen)
	if err != nil {
		return nil, fmt.Errorf("%w (%v)", ErrDamagedFile, err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != block.BlockSize() || len(data) == 0 || len(data)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("%w (encrypted data)", ErrDamagedFile)
	}
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, data)
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > block.BlockSize() || !bytes.Equal(plain[len(plain)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) { //nolint:gosec // pad came from a byte
		return nil, ErrWrongPassword
	}
	return plain[:len(plain)-pad], nil
}
