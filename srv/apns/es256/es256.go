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

// Package es256 produces deterministic ECDSA P-256 signatures for JWS ES256.
//
// # Why this exists
//
// APNs rate-limits how often a provider may mint a new authentication token:
// roughly one per signing key per twenty minutes, answering anything faster
// with 429 TooManyProviderTokenUpdates. A token stays valid for an hour. So a
// uniqush process that restarts, or a second instance that starts up, must
// somehow arrive at the token its predecessor is already using -- it cannot
// mint its own without risking the limit, and it has no way to ask for the
// existing one.
//
// Signing deterministically dissolves that problem instead of coordinating
// around it. The JWT is already a pure function of the signing key, the key id,
// the team id and the issued-at time; the signature is the only part that
// varies, because ECDSA draws a random nonce. Remove that randomness and every
// process, on every restart, independently computes the same bytes for the same
// time bucket. Apple sees one token. There is nothing to share, nothing to
// lock, and no credential stored anywhere new.
//
// # Why it is not crypto/ecdsa
//
// It would be, if the standard library could do it. Go's ecdsa deliberately
// cannot: signatures are "hedged", mixing entropy into the nonce, and as of Go
// 1.26 the rand argument is ignored outright --
//
//	"The signature is randomized. Since Go 1.26, a secure source of random
//	 bytes is always used, and the Reader is ignored [...]"
//
// -- with the package documentation stating plainly that signatures are not
// deterministic. RFC 6979 does exist in the tree, at
// crypto/internal/fips140/ecdsa, but it is internal. The accepted proposal to
// expose it (golang/go#64802) did not ship, and the direction of travel is away
// from letting callers influence signing randomness at all.
//
// So the choice is between owning this and giving up determinism. The scope is
// deliberately minimal: the nonce comes from RFC 6979, which is a published
// standard with published test vectors, and the elliptic curve arithmetic comes
// from filippo.io/nistec, the same constant-time P-256 implementation the
// standard library uses internally. What is written here is the small amount of
// glue between them, and it is checked against the RFC's own vectors rather
// than against itself.
//
// # What is not claimed
//
// The modular arithmetic uses math/big, which is not constant time. The
// inversion of the nonce is blinded, which is the operation where a timing leak
// would recover the private key, but the remaining operations are not hardened.
// That is acceptable here and would not be in a general-purpose library:
// uniqush signs roughly one token per key per bucket -- currently thirty-five
// minutes, see http_api.TokenRefreshInterval -- on a server, over a message an
// attacker does not choose and cannot request on demand. A signing oracle this
// slow does not give a timing attack anything to work with.
//
// That rate is the whole of the argument, so it is worth saying what would
// invalidate it. Signing on every push, or on every batch, would not: the cache
// in http_api is what keeps the rate down, and a bug there that made it re-sign
// per request would quietly move this package into a regime it was never
// justified for. There is a test for exactly that
// (TestProviderTokenCachesBothLiveBuckets), because the failure is silent from
// here. Do not lift this package into a context where the rate is higher.
package es256

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"

	"filippo.io/nistec"
)

// SignatureSize is the JWS ES256 signature length: r and s, each padded to the
// 32-byte group size and concatenated. Not ASN.1 -- RFC 7515 section 3.4.
const SignatureSize = 64

// curveName describes a curve for an error message without trusting it.
//
// elliptic.Curve is an interface, so Params can return nil for anything not from
// the standard library. This is only ever called on a curve that has already
// been rejected, which is exactly the population most likely to be malformed.
func curveName(curve elliptic.Curve) string {
	if curve == nil {
		return "<nil>"
	}
	params := curve.Params()
	if params == nil || params.Name == "" {
		return "<unknown>"
	}
	return params.Name
}

// Sign returns the deterministic JWS ES256 signature over signingInput.
//
// signingInput is the JWT's "<header>.<claims>" string, exactly as it will be
// transmitted; this hashes it with SHA-256 itself.
//
// The result depends only on the key and the input. Calling it twice, in
// different processes, on different machines, returns identical bytes.
func Sign(key *ecdsa.PrivateKey, signingInput []byte) ([]byte, error) {
	// Every field is checked before it is used, because this function reports
	// failure by returning an error and must not panic instead.
	//
	// An *ecdsa.PrivateKey is a struct a caller can assemble by hand, and each
	// way it can be malformed is reachable: the zero value has a nil Curve and a
	// nil D, and unmarshalling something unexpected can leave D outside the
	// group. All of them reach arithmetic or a FillBytes below that would panic,
	// taking the process down rather than failing one push. The first version of
	// this function even dereferenced a nil Curve while formatting the error
	// meant to reject a wrong one.
	if key == nil {
		return nil, errors.New("es256: nil signing key")
	}
	if key.Curve == nil {
		return nil, errors.New("es256: signing key has no curve")
	}
	if key.Curve != elliptic.P256() {
		// Named without dereferencing the rejected curve. Params() is an
		// interface method: an implementation uniqush has never seen may return
		// nil, and reading .Name off it would panic inside the guard whose whole
		// job is to turn a bad key into an error. curveName falls back to a
		// description rather than risking that.
		return nil, fmt.Errorf("es256: key uses curve %s, but ES256 is P-256 only", curveName(key.Curve))
	}

	n := key.Curve.Params().N
	if key.D == nil {
		return nil, errors.New("es256: signing key has no private scalar")
	}
	// A private scalar lies in [1, n-1]. Zero and negative values are not keys
	// at all, and one at or above the group order is not either -- int2octets
	// would quietly truncate it to something the size of a key, and everything
	// downstream would proceed as though it were one.
	if key.D.Sign() <= 0 || key.D.Cmp(n) >= 0 {
		return nil, errors.New("es256: private scalar is out of range for P-256")
	}
	// The public half is checked too, because Sign verifies its own output
	// before returning it and crypto/ecdsa.Verify dereferences X and Y without
	// guarding them. A key carrying a valid D and no public point -- which is
	// what an *ecdsa.PrivateKey assembled field by field looks like -- would
	// otherwise reach that call and panic, taking the process down inside the
	// guards whose whole job is to turn a bad key into an error.
	//
	// Only presence and range are checked here. Whether the point actually
	// corresponds to D is not something to decide by re-deriving it: the
	// verification at the end of Sign already answers that, and answers it for
	// the signature actually produced.
	if key.X == nil || key.Y == nil {
		return nil, errors.New("es256: signing key has no public point")
	}
	p := key.Curve.Params().P
	if key.X.Sign() < 0 || key.X.Cmp(p) >= 0 || key.Y.Sign() < 0 || key.Y.Cmp(p) >= 0 {
		return nil, errors.New("es256: public point is out of range for P-256")
	}

	digest := sha256.Sum256(signingInput)

	// e is the digest as an integer, per ECDSA. For SHA-256 and P-256 the
	// digest is exactly the group size, so no truncation happens; bits2int
	// still handles it rather than assuming.
	e := bits2int(digest[:])
	d := key.D

	nonces := newNonceGenerator(int2octets(d), digest[:], n)

	// RFC 6979 section 3.2 step h: retry while r or s is zero. Both are
	// astronomically unlikely; the loop is bounded so that a bug cannot spin
	// forever holding the caller.
	for attempt := 0; attempt < 8; attempt++ {
		k := nonces.next()

		r, err := scalarBaseMultX(k, n)
		if err != nil {
			return nil, err
		}
		if r.Sign() == 0 {
			continue
		}

		s := new(big.Int).Mul(r, d)
		s.Add(s, e)
		kInv, err := blindedInverse(k, n)
		if err != nil {
			return nil, err
		}
		s.Mul(s, kInv)
		s.Mod(s, n)
		if s.Sign() == 0 {
			continue
		}

		// Verify before returning. This is cheap at roughly one signature per
		// bucket, and it turns an implementation mistake, or a bit flipped in
		// memory, into an error instead of an authentication failure that looks
		// like a credential problem at three in the morning.
		if !ecdsa.Verify(&key.PublicKey, digest[:], r, s) {
			return nil, errors.New("es256: produced a signature that does not verify")
		}

		signature := make([]byte, SignatureSize)
		r.FillBytes(signature[:scalarBytes])
		s.FillBytes(signature[scalarBytes:])
		return signature, nil
	}

	return nil, errors.New("es256: exhausted nonce candidates, which should be impossible")
}

// scalarBaseMultX returns the x coordinate of k*G, reduced into the group.
//
// nistec is the constant-time P-256 implementation from the Go crypto
// maintainer, and the same code the standard library vendors internally as
// crypto/internal/fips140/nistec. Using it directly rather than
// elliptic.P256().ScalarBaseMult avoids a deprecated API whose replacement
// (crypto/ecdh) cannot sign.
func scalarBaseMultX(k *big.Int, n *big.Int) (*big.Int, error) {
	point, err := nistec.NewP256Point().ScalarBaseMult(int2octets(k))
	if err != nil {
		return nil, fmt.Errorf("es256: scalar multiplication failed: %v", err)
	}
	// Uncompressed SEC1: 0x04 || X || Y.
	encoded := point.Bytes()
	if len(encoded) != 1+2*scalarBytes {
		return nil, fmt.Errorf("es256: unexpected point encoding of %d bytes", len(encoded))
	}
	x := new(big.Int).SetBytes(encoded[1 : 1+scalarBytes])
	return x.Mod(x, n), nil
}

// blindedInverse returns k^-1 mod n without leaking k through timing.
//
// math/big's ModInverse is not constant time, and inverting the nonce directly
// is the one operation in ECDSA where a timing leak recovers the private key.
// Blinding sidesteps that: with a random b, k^-1 = b * (k*b)^-1, so the value
// actually inverted is uniformly random and independent of k. The result is
// unchanged, so the signature stays deterministic even though the computation
// is not.
func blindedInverse(k *big.Int, n *big.Int) (*big.Int, error) {
	blind, err := rand.Int(rand.Reader, new(big.Int).Sub(n, big.NewInt(1)))
	if err != nil {
		return nil, fmt.Errorf("es256: could not generate a blinding factor: %v", err)
	}
	blind.Add(blind, big.NewInt(1)) // [1, n-1]

	blinded := new(big.Int).Mul(k, blind)
	blinded.Mod(blinded, n)

	inverse := new(big.Int).ModInverse(blinded, n)
	if inverse == nil {
		return nil, errors.New("es256: nonce is not invertible")
	}
	return inverse.Mul(inverse, blind).Mod(inverse, n), nil
}
