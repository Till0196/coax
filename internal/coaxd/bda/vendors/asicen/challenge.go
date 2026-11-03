// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
package asicen

// lastRound is the AES-128 initialization challenge's tenth round key. The
// driver's ABI carries the Rijndael schedule as little-endian DWORDs.
func lastRound(k [16]byte) [16]byte {
	rc := byte(1)
	for round := 0; round < 10; round++ {
		t := [4]byte{sub(k[13]) ^ rc, sub(k[14]), sub(k[15]), sub(k[12])}
		for i := 0; i < 16; i++ {
			k[i] ^= t[i%4]
			t[i%4] = k[i]
		}
		rc = mul(rc, 2)
	}
	for i := 0; i < 16; i += 4 {
		k[i], k[i+1], k[i+2], k[i+3] = k[i+3], k[i+2], k[i+1], k[i]
	}
	return k
}
func mul(a, b byte) byte {
	var r byte
	for b != 0 {
		if b&1 != 0 {
			r ^= a
		}
		hi := a & 0x80
		a <<= 1
		if hi != 0 {
			a ^= 0x1b
		}
		b >>= 1
	}
	return r
}
func sub(a byte) byte {
	v := byte(1)
	for i := 0; i < 254; i++ {
		v = mul(v, a)
	}
	if a == 0 {
		v = 0
	}
	r := v
	for i := 1; i < 5; i++ {
		r ^= v<<i | v>>(8-i)
	}
	return r ^ 0x63
}
