package certs

import (
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"math/big"
)

// The parts of a PKCS#12 file (RFC 7292) that carry key derivation
// parameters, read before the file is opened: the library derives keys
// with whatever round count a file states.

type p12PFX struct {
	Version  int
	AuthSafe p12ContentInfo
	MacData  p12MacData `asn1:"optional"`
}

type p12ContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"tag:0,explicit,optional"`
}

type p12MacData struct {
	Mac struct {
		Algorithm pkix.AlgorithmIdentifier
		Digest    []byte
	}
	MacSalt    []byte
	Iterations *big.Int `asn1:"optional"`
}

type p12EncryptedData struct {
	Version              int
	EncryptedContentInfo struct {
		ContentType                asn1.ObjectIdentifier
		ContentEncryptionAlgorithm pkix.AlgorithmIdentifier
		EncryptedContent           asn1.RawValue `asn1:"tag:0,optional"`
	}
}

type p12SafeBag struct {
	ID         asn1.ObjectIdentifier
	Value      asn1.RawValue `asn1:"tag:0,explicit"`
	Attributes asn1.RawValue `asn1:"optional"`
}

type p12EncryptedPrivateKeyInfo struct {
	Algorithm     pkix.AlgorithmIdentifier
	EncryptedData []byte
}

// pkcs12PBEParams: salt and round count of the PKCS#12 PBE schemes.
type pkcs12PBEParams struct {
	Salt       []byte
	Iterations *big.Int
}

// pbes2Params and pbkdf2Params (RFC 8018); PBMAC1 shares the layout.
type pbes2Params struct {
	KeyDerivationFunc pkix.AlgorithmIdentifier
	Scheme            asn1.RawValue
}

type pbkdf2Params struct {
	Salt       asn1.RawValue
	Iterations *big.Int
	Rest       asn1.RawValue `asn1:"optional"`
}

var (
	oidP12Data          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidP12EncryptedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 6}
	oidP12ShroudedKey   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 2}
	oidP12PBEPrefix     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 1}
	oidPBES2            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 13}
	oidPBMAC1           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 14}
	oidPBKDF2           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 12}
)

// checkP12Iterations rejects a file whose MAC or encryption asks for more
// than maxIterations rounds, reading every place a file can state them
// without decrypting anything: the MAC, encrypted safes, and shrouded key
// bags in unencrypted safes (where OpenSSL, Go and Windows put them). It
// also rejects files that are not DER (BER) or not well-formed.
func checkP12Iterations(data []byte) error {
	var pfx p12PFX
	if err := unmarshalDER(data, &pfx); err != nil {
		return err
	}
	if err := checkCount(pfx.MacData.Iterations); err != nil {
		return err
	}
	if pfx.MacData.Mac.Algorithm.Algorithm.Equal(oidPBMAC1) {
		if err := checkPBES2Like(pfx.MacData.Mac.Algorithm.Parameters.FullBytes); err != nil {
			return err
		}
	}
	if !pfx.AuthSafe.ContentType.Equal(oidP12Data) {
		return nil // the library refuses other content types
	}
	var authSafe []byte
	if err := unmarshalDER(pfx.AuthSafe.Content.Bytes, &authSafe); err != nil {
		return err
	}
	var safes []p12ContentInfo
	if err := unmarshalDER(authSafe, &safes); err != nil {
		return err
	}
	for _, ci := range safes {
		switch {
		case ci.ContentType.Equal(oidP12EncryptedData):
			var ed p12EncryptedData
			if err := unmarshalDER(ci.Content.Bytes, &ed); err != nil {
				return err
			}
			if err := checkAlgorithm(ed.EncryptedContentInfo.ContentEncryptionAlgorithm); err != nil {
				return err
			}
		case ci.ContentType.Equal(oidP12Data):
			var raw []byte
			if err := unmarshalDER(ci.Content.Bytes, &raw); err != nil {
				return err
			}
			var bags []p12SafeBag
			if err := unmarshalDER(raw, &bags); err != nil {
				return err
			}
			for _, bag := range bags {
				if !bag.ID.Equal(oidP12ShroudedKey) {
					continue
				}
				var epki p12EncryptedPrivateKeyInfo
				if err := unmarshalDER(bag.Value.Bytes, &epki); err != nil {
					return err
				}
				if err := checkAlgorithm(epki.Algorithm); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// checkAlgorithm checks the round count of a PKCS#12 PBE or PBES2
// algorithm; others are left to the library.
func checkAlgorithm(alg pkix.AlgorithmIdentifier) error {
	switch {
	case alg.Algorithm.Equal(oidPBES2):
		return checkPBES2Like(alg.Parameters.FullBytes)
	case len(alg.Algorithm) == len(oidP12PBEPrefix)+1 && alg.Algorithm[:len(oidP12PBEPrefix)].Equal(oidP12PBEPrefix):
		var p pkcs12PBEParams
		if err := unmarshalDER(alg.Parameters.FullBytes, &p); err != nil {
			return err
		}
		return checkCount(p.Iterations)
	}
	return nil
}

// checkPBES2Like checks the PBKDF2 round count in PBES2 or PBMAC1
// parameters.
func checkPBES2Like(params []byte) error {
	var p pbes2Params
	if err := unmarshalDER(params, &p); err != nil {
		return err
	}
	if !p.KeyDerivationFunc.Algorithm.Equal(oidPBKDF2) {
		return nil
	}
	var k pbkdf2Params
	if err := unmarshalDER(p.KeyDerivationFunc.Parameters.FullBytes, &k); err != nil {
		return err
	}
	return checkCount(k.Iterations)
}

func checkCount(n *big.Int) error {
	if n != nil && n.Cmp(big.NewInt(maxIterations)) > 0 {
		return ErrTooManyIterations
	}
	return nil
}

// unmarshalDER decodes exactly one DER value; BER encodings count as an
// unsupported format, anything else malformed as a damaged file.
func unmarshalDER(data []byte, v any) error {
	rest, err := asn1.Unmarshal(data, v)
	switch {
	case err != nil && isBER(err):
		return ErrUnsupportedFile
	case err != nil:
		return fmt.Errorf("%w (%v)", ErrDamagedFile, err)
	case len(rest) > 0:
		return fmt.Errorf("%w (trailing data)", ErrDamagedFile)
	}
	return nil
}
