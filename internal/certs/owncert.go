package certs

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"unicode/utf8"
)

// parseOwnCertificate parses a certificate of one of the user's own
// identities, or of its chain. Never use it for a server's certificate.
//
// Besides what x509.ParseCertificate accepts, it takes a certificate whose
// subjectAltName holds an e-mail address (rfc822Name) in UTF-8 rather than
// ASCII: the official Mumble client writes addresses with letters such as
// ä that way. Such a certificate is parsed from a copy without those
// entries. Raw is then set back to the original DER, so the fingerprint,
// the certificate sent to servers and exported backups stay exactly the
// original, and EmailAddresses holds the addresses as written. The other
// raw fields (RawTBSCertificate, Signature, Extensions) describe the copy;
// nothing reads them for an identity. Any other parse error, including
// e-mail addresses that are not valid UTF-8, stays an error.
func parseOwnCertificate(der []byte) (*x509.Certificate, error) {
	cert, err := x509.ParseCertificate(der)
	if err == nil {
		return cert, nil
	}
	cleaned, addrs, ok := withoutUTF8Emails(der)
	if !ok {
		return nil, err
	}
	cert, cerr := x509.ParseCertificate(cleaned)
	if cerr != nil {
		return nil, err
	}
	cert.Raw = der
	cert.EmailAddresses = append(addrs, cert.EmailAddresses...)
	return cert, nil
}

const tagExtensions = 3 // [3] EXPLICIT Extensions in TBSCertificate

// withoutUTF8Emails returns a copy of the certificate der whose
// subjectAltName lacks the rfc822Name entries that are UTF-8 but not
// ASCII, and those addresses. ok is false when there is no such entry,
// when one is not valid UTF-8 or holds control characters, or when der is
// not a well-formed certificate.
func withoutUTF8Emails(der []byte) (cleaned []byte, addrs []string, ok bool) {
	var certSeq []asn1.RawValue // tbsCertificate, signatureAlgorithm, signature
	if rest, err := asn1.Unmarshal(der, &certSeq); err != nil || len(rest) > 0 || len(certSeq) != 3 {
		return nil, nil, false
	}
	var tbs []asn1.RawValue
	if rest, err := asn1.Unmarshal(certSeq[0].FullBytes, &tbs); err != nil || len(rest) > 0 {
		return nil, nil, false
	}
	for i, field := range tbs {
		if field.Class != asn1.ClassContextSpecific || field.Tag != tagExtensions {
			continue
		}
		var exts []asn1.RawValue
		if rest, err := asn1.Unmarshal(field.Bytes, &exts); err != nil || len(rest) > 0 {
			return nil, nil, false
		}
		for j, raw := range exts {
			var ext pkix.Extension
			if rest, err := asn1.Unmarshal(raw.FullBytes, &ext); err != nil || len(rest) > 0 {
				return nil, nil, false
			}
			if !ext.Id.Equal(oidSubjectAltName) {
				continue
			}
			var names []asn1.RawValue
			if rest, err := asn1.Unmarshal(ext.Value, &names); err != nil || len(rest) > 0 {
				return nil, nil, false
			}
			var kept [][]byte
			for _, n := range names {
				if n.Class != asn1.ClassContextSpecific || n.Tag != 1 || isASCII(n.Bytes) {
					kept = append(kept, n.FullBytes)
					continue
				}
				if !utf8.Valid(n.Bytes) || hasControl(n.Bytes) {
					return nil, nil, false
				}
				addrs = append(addrs, string(n.Bytes))
			}
			if len(addrs) == 0 {
				return nil, nil, false
			}
			var newExts [][]byte
			for k, other := range exts {
				if k != j {
					newExts = append(newExts, other.FullBytes)
				}
			}
			if len(kept) > 0 {
				ext.Value = sequence(kept)
				b, err := asn1.Marshal(ext)
				if err != nil {
					return nil, nil, false
				}
				newExts = append(newExts[:j], append([][]byte{b}, newExts[j:]...)...)
			}
			var newTBS [][]byte
			for k, f := range tbs {
				switch {
				case k != i:
					newTBS = append(newTBS, f.FullBytes)
				case len(newExts) > 0:
					wrapped, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: tagExtensions, IsCompound: true, Bytes: sequence(newExts)})
					if err != nil {
						return nil, nil, false
					}
					newTBS = append(newTBS, wrapped)
				}
			}
			return sequence([][]byte{sequence(newTBS), certSeq[1].FullBytes, certSeq[2].FullBytes}), addrs, true
		}
	}
	return nil, nil, false
}

// sequence encodes elems (each complete DER) as a SEQUENCE.
func sequence(elems [][]byte) []byte {
	b, _ := asn1.Marshal(asn1.RawValue{Tag: asn1.TagSequence, IsCompound: true, Bytes: bytes.Join(elems, nil)})
	return b
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func hasControl(b []byte) bool {
	for _, r := range string(b) {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return true
		}
	}
	return false
}
