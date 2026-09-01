/*
 * Copyright 2013-2026 Uniqush Contributors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package es256

import (
	"crypto/hmac"
	"crypto/sha256"
	"math/big"
)

// RFC 6979 nonce derivation, specialised to P-256 and SHA-256.
//
// The nonce k in ECDSA must never repeat for two different messages under one
// key: two signatures sharing a k reveal the private key by simple algebra.
// Normally k comes from a random source. RFC 6979 instead derives it from the
// key and the message with HMAC-SHA256, which gives the same guarantee -- a
// different message yields a different k -- without needing randomness, and
// makes the whole signature a pure function of (key, message).
//
// That last property is the entire reason this file exists. See docs/adr and the
// package comment in es256.go.

// qlen and rlen for P-256: the group order is 256 bits, so both the integer
// conversions and the HMAC output work in 32-byte units.
const (
	scalarBytes = 32
	scalarBits  = 256
)

// nonceGenerator yields successive RFC 6979 candidates for k.
//
// A generator rather than a single value because the caller may have to reject
// one: ECDSA requires r != 0 and s != 0, and RFC 6979 section 3.2 step h
// specifies exactly how to advance the DRBG when that happens. Rejection is
// vanishingly rare, but "vanishingly rare" is where signature schemes hide
// their key-recovery bugs, so the path exists and is tested.
type nonceGenerator struct {
	k []byte // HMAC key, 32 bytes
	v []byte // HMAC value, 32 bytes
	n *big.Int
	// primed is false until the first candidate has been produced, because the
	// DRBG advances differently before and after.
	primed bool
}

// newNonceGenerator performs RFC 6979 section 3.2 steps b through g.
func newNonceGenerator(privateKey, digest []byte, n *big.Int) *nonceGenerator {
	// b. V = 0x01 0x01 ... 0x01
	v := make([]byte, scalarBytes)
	for i := range v {
		v[i] = 0x01
	}
	// c. K = 0x00 0x00 ... 0x00
	k := make([]byte, scalarBytes)

	x := int2octets(new(big.Int).SetBytes(privateKey))
	h := bits2octets(digest, n)

	// d. K = HMAC_K(V || 0x00 || int2octets(x) || bits2octets(h1))
	k = mac(k, v, []byte{0x00}, x, h)
	// e. V = HMAC_K(V)
	v = mac(k, v)
	// f. K = HMAC_K(V || 0x01 || int2octets(x) || bits2octets(h1))
	k = mac(k, v, []byte{0x01}, x, h)
	// g. V = HMAC_K(V)
	v = mac(k, v)

	return &nonceGenerator{k: k, v: v, n: n}
}

// next returns the next candidate k in [1, n-1].
func (g *nonceGenerator) next() *big.Int {
	if g.primed {
		// h.3: on retry, advance the DRBG before generating again.
		g.k = mac(g.k, g.v, []byte{0x00})
		g.v = mac(g.k, g.v)
	}
	g.primed = true

	for {
		// h.1 and h.2: T = V || V || ... until it is at least qlen bits.
		var t []byte
		for len(t)*8 < scalarBits {
			g.v = mac(g.k, g.v)
			t = append(t, g.v...)
		}

		candidate := bits2int(t)
		// h.3: accept only 1 <= k <= n-1.
		if candidate.Sign() > 0 && candidate.Cmp(g.n) < 0 {
			return candidate
		}
		g.k = mac(g.k, g.v, []byte{0x00})
		g.v = mac(g.k, g.v)
	}
}

// mac is HMAC-SHA256 over the concatenation of parts.
func mac(key []byte, parts ...[]byte) []byte {
	h := hmac.New(sha256.New, key)
	for _, part := range parts {
		h.Write(part)
	}
	return h.Sum(nil)
}

// bits2int is RFC 6979 section 2.3.2: take the leftmost qlen bits of b.
//
// For our sizes -- a 32-byte SHA-256 digest and a 256-bit group -- the input is
// exactly qlen bits and this is a plain big-endian read. The shift is kept
// because the DRBG in next() can hand over more than 32 bytes, and silently
// reading the wrong end of that would produce a valid-looking signature with a
// nonce nobody else derives.
func bits2int(b []byte) *big.Int {
	value := new(big.Int).SetBytes(b)
	if excess := len(b)*8 - scalarBits; excess > 0 {
		value.Rsh(value, uint(excess))
	}
	return value
}

// int2octets is RFC 6979 section 2.3.3: the integer as exactly rlen bytes.
func int2octets(value *big.Int) []byte {
	out := make([]byte, scalarBytes)
	value.FillBytes(out)
	return out
}

// bits2octets is RFC 6979 section 2.3.4: reduce the digest into the group, then
// render it as rlen bytes.
func bits2octets(digest []byte, n *big.Int) []byte {
	z1 := bits2int(digest)
	return int2octets(z1.Mod(z1, n))
}
