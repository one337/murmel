package crypt

import "bytes"

// OCB2 parameters: AES-128 with 16-byte blocks, exactly as in
// CryptStateOCB2.cpp (AES_BLOCK_SIZE / AES_KEY_SIZE_BYTES).
const blockSize = 16

// s2 doubles a 16-byte block in GF(2^128) (big-endian), reducing with 0x87
// on overflow. Port of the LP64 S2() in CryptStateOCB2.cpp, which operates
// on two big-endian uint64 subblocks; in byte terms both variants are a
// straight big-endian shift over the whole block.
func s2(block *[blockSize]byte) {
	carry := block[0] >> 7
	for i := 0; i < blockSize-1; i++ {
		block[i] = block[i]<<1 | block[i+1]>>7
	}
	block[blockSize-1] = block[blockSize-1]<<1 ^ carry*0x87
}

// s3 computes block ^= block<<1 ("triple") in GF(2^128).
// Port of S3() in CryptStateOCB2.cpp.
func s3(block *[blockSize]byte) {
	carry := block[0] >> 7
	for i := 0; i < blockSize-1; i++ {
		block[i] ^= block[i]<<1 | block[i+1]>>7
	}
	block[blockSize-1] ^= block[blockSize-1]<<1 ^ carry*0x87
}

// xorBlock sets dst = a ^ b for three 16-byte buffers. Buffers may alias.
func xorBlock(dst, a, b []byte) {
	for i := 0; i < blockSize; i++ {
		dst[i] = a[i] ^ b[i]
	}
}

// ocbEncrypt encrypts plain under nonce with OCB2-AES128, returning the
// ciphertext (same length as plain) and the 16-byte authentication tag.
//
// This is a direct port of CryptStateOCB2::ocb_encrypt. modifyPlain mirrors
// the C++ modifyPlainOnXEXStarAttack parameter of the XEX* attack
// countermeasure (section 9 of https://eprint.iacr.org/2019/311): when the
// second-to-last block is all zeros except the last byte, either one bit of
// the first block is flipped in the encrypted domain (modifyPlain, keeping
// the audio payload intact) or the encryption is rejected with ok == false.
func (cs *CryptState) ocbEncrypt(plain, nonce []byte, modifyPlain bool) (ciphertext, tag []byte, ok bool) {
	if !cs.init {
		return nil, nil, false
	}
	ok = true

	var delta, checksum, tmp, pad [blockSize]byte

	// delta = AES(nonce); checksum = 0.
	cs.cipher.Encrypt(delta[:], nonce)

	ciphertext = make([]byte, len(plain))
	p, c := plain, ciphertext

	for len(p) > blockSize {
		// Counter-cryptanalysis: for the attack to work, the second-to-last
		// block (the last iteration of this loop) must be all zero except
		// for the last byte.
		flipABit := false
		if len(p)-blockSize <= blockSize {
			var sum byte
			for i := 0; i < blockSize-1; i++ {
				sum |= p[i]
			}
			if sum == 0 {
				if modifyPlain {
					flipABit = true
				} else {
					ok = false
				}
			}
		}

		s2(&delta)
		xorBlock(tmp[:], delta[:], p[:blockSize])
		if flipABit {
			tmp[0] ^= 1
		}
		cs.cipher.Encrypt(tmp[:], tmp[:])
		xorBlock(c[:blockSize], delta[:], tmp[:])
		xorBlock(checksum[:], checksum[:], p[:blockSize])
		if flipABit {
			checksum[0] ^= 1
		}

		p = p[blockSize:]
		c = c[blockSize:]
	}

	// Final partial (or full) block: pad = AES(len_block ^ delta).
	s2(&delta)
	tmp = [blockSize]byte{}
	tmp[blockSize-1] = byte(len(p) * 8) // be64(len*8) in the second subblock
	xorBlock(tmp[:], tmp[:], delta[:])
	cs.cipher.Encrypt(pad[:], tmp[:])
	copy(tmp[:], p)
	copy(tmp[len(p):], pad[len(p):])
	xorBlock(checksum[:], checksum[:], tmp[:])
	xorBlock(tmp[:], pad[:], tmp[:])
	copy(c, tmp[:len(p)])

	// tag = AES(S3(delta) ^ checksum).
	s3(&delta)
	xorBlock(tmp[:], delta[:], checksum[:])
	tag = make([]byte, blockSize)
	cs.cipher.Encrypt(tag, tmp[:])
	return ciphertext, tag, ok
}

// ocbDecrypt decrypts ciphertext under nonce with OCB2-AES128, returning
// the plaintext (same length as ciphertext) and the 16-byte tag that a
// genuine sender would have produced.
//
// Direct port of CryptStateOCB2::ocb_decrypt, including the XEX* attack
// detection: if the decrypted final block matches delta on the first 15
// bytes, the input was crafted and ok is false.
func (cs *CryptState) ocbDecrypt(ciphertext, nonce []byte) (plain, tag []byte, ok bool) {
	if !cs.init {
		return nil, nil, false
	}
	ok = true

	var delta, checksum, tmp, pad [blockSize]byte

	cs.cipher.Encrypt(delta[:], nonce)

	plain = make([]byte, len(ciphertext))
	p, e := plain, ciphertext

	for len(e) > blockSize {
		s2(&delta)
		xorBlock(tmp[:], delta[:], e[:blockSize])
		cs.cipher.Decrypt(tmp[:], tmp[:])
		xorBlock(p[:blockSize], delta[:], tmp[:])
		xorBlock(checksum[:], checksum[:], p[:blockSize])
		p = p[blockSize:]
		e = e[blockSize:]
	}

	s2(&delta)
	tmp = [blockSize]byte{}
	tmp[blockSize-1] = byte(len(e) * 8)
	xorBlock(tmp[:], tmp[:], delta[:])
	cs.cipher.Encrypt(pad[:], tmp[:])
	tmp = [blockSize]byte{}
	copy(tmp[:], e)
	xorBlock(tmp[:], tmp[:], pad[:])
	xorBlock(checksum[:], checksum[:], tmp[:])
	copy(p, tmp[:len(e)])

	// Counter-cryptanalysis: in an attack the decrypted last block equals
	// delta ^ len(128); since len only ever modifies the last byte, it
	// suffices to compare the first 15 bytes.
	if bytes.Equal(tmp[:blockSize-1], delta[:blockSize-1]) {
		ok = false
	}

	s3(&delta)
	xorBlock(tmp[:], delta[:], checksum[:])
	tag = make([]byte, blockSize)
	cs.cipher.Encrypt(tag, tmp[:])
	return plain, tag, ok
}
