package es256

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

// The published test vectors from RFC 6979 appendix A.2.5, "ECDSA, 256 Bits
// (Prime Field)", for curve NIST P-256.
//
// These are the reason this package can be trusted rather than merely reviewed.
// A hand-written deterministic signer that only agrees with itself proves
// nothing: it would produce stable, verifiable, entirely non-standard nonces,
// and every test built on its own output would pass. Reproducing the RFC's
// numbers exactly is independent evidence that the derivation is the one the
// standard describes.
const (
	rfc6979PrivateKey = "C9AFA9D845BA75166B5C215767B1D6934E50C3DB36E89B127B8A622B120F6721"
	rfc6979PublicX    = "60FED4BA255A9D31C961EB74C6356D68C049B8923B61FA6CE669622E60F29FB6"
	rfc6979PublicY    = "7903FE1008B8BC99A41AE9E95628BC64F2F1B20C2D7E9F5177A3C294D4462299"
)

// rfc6979Vector is one row of appendix A.2.5, for SHA-256.
type rfc6979Vector struct {
	message string
	k, r, s string
}

var rfc6979SHA256Vectors = []rfc6979Vector{
	{
		message: "sample",
		k:       "A6E3C57DD01ABE90086538398355DD4C3B17AA873382B0F24D6129493D8AAD60",
		r:       "EFD48B2AACB6A8FD1140DD9CD45E81D69D2C877B56AAF991C34D0EA84EAF3716",
		s:       "F7CB1C942D657C41D436C7A1B6E29F65F3E900DBB9AFF4064DC4AB2F843ACDA8",
	},
	{
		message: "test",
		k:       "D16B6AE827F17175E040871A1C7EC3500192C4C92677336EC2537ACAEE0008E0",
		r:       "F1ABB023518351CD71D881567B1EA663ED3EFCF6C5132B354F28D3B0B7D38367",
		s:       "019F4113742A2B14BD25926B49C649155F267E60D3814B4C0CC84250E46F0083",
	},
}

func mustHexInt(t *testing.T, value string) *big.Int {
	t.Helper()
	parsed, ok := new(big.Int).SetString(value, 16)
	if !ok {
		t.Fatalf("Could not parse %q as hex", value)
	}
	return parsed
}

// rfc6979Key builds the appendix A.2.5 key pair.
func rfc6979Key(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key := &ecdsa.PrivateKey{
		D: mustHexInt(t, rfc6979PrivateKey),
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     mustHexInt(t, rfc6979PublicX),
			Y:     mustHexInt(t, rfc6979PublicY),
		},
	}
	// The vectors give the public key explicitly; check it really is this
	// private key's, so a typo above cannot silently weaken every test here.
	x, y := elliptic.P256().ScalarBaseMult(key.D.Bytes()) //nolint:staticcheck // the deprecated call is the check
	if x.Cmp(key.X) != 0 || y.Cmp(key.Y) != 0 {
		t.Fatal("The RFC 6979 public key vector does not match its private key")
	}
	return key
}

// TestNonceMatchesRFC6979Vectors checks the derivation in isolation.
//
// Separated from signing on purpose: if a signature disagreed with the RFC, the
// cause could be the nonce or the curve arithmetic, and this says which.
func TestNonceMatchesRFC6979Vectors(t *testing.T) {
	key := rfc6979Key(t)
	n := elliptic.P256().Params().N

	for _, vector := range rfc6979SHA256Vectors {
		t.Run(vector.message, func(t *testing.T) {
			digest := sha256.Sum256([]byte(vector.message))
			got := newNonceGenerator(int2octets(key.D), digest[:], n).next()

			if want := mustHexInt(t, vector.k); got.Cmp(want) != 0 {
				t.Errorf("k mismatch\n got %X\nwant %X", got, want)
			}
		})
	}
}

// TestSignMatchesRFC6979Vectors is the end-to-end check against the standard.
func TestSignMatchesRFC6979Vectors(t *testing.T) {
	key := rfc6979Key(t)

	for _, vector := range rfc6979SHA256Vectors {
		t.Run(vector.message, func(t *testing.T) {
			signature, err := Sign(key, []byte(vector.message))
			if err != nil {
				t.Fatalf("Sign failed: %v", err)
			}
			if len(signature) != SignatureSize {
				t.Fatalf("Expected %d bytes, got %d", SignatureSize, len(signature))
			}

			r := new(big.Int).SetBytes(signature[:scalarBytes])
			s := new(big.Int).SetBytes(signature[scalarBytes:])

			if want := mustHexInt(t, vector.r); r.Cmp(want) != 0 {
				t.Errorf("r mismatch\n got %X\nwant %X", r, want)
			}
			if want := mustHexInt(t, vector.s); s.Cmp(want) != 0 {
				t.Errorf("s mismatch\n got %X\nwant %X", s, want)
			}
		})
	}
}

// TestSignIsDeterministic is the property the whole package exists for.
//
// Repeated in a loop because the blinding factor in the inversion is random:
// the computation differs every time and the output must not.
func TestSignIsDeterministic(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Could not generate a key: %v", err)
	}
	input := []byte("eyJhbGciOiJFUzI1NiJ9.eyJpc3MiOiJURUFNMTIzNDU2In0")

	first, err := Sign(key, input)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	for i := 0; i < 32; i++ {
		again, err := Sign(key, input)
		if err != nil {
			t.Fatalf("Sign failed on attempt %d: %v", i, err)
		}
		if !bytesEqual(first, again) {
			t.Fatalf("Signature %d differs from the first; the blinding factor is leaking into the output", i)
		}
	}
}

// TestSignDiffersPerMessage is the safety property on the other side.
//
// A "deterministic" signer that reused one nonce across different messages
// would be stable, would verify, and would hand out the private key to anyone
// who collected two signatures. This is the cheapest possible check that the
// nonce is derived from the message and not merely from the key.
func TestSignDiffersPerMessage(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Could not generate a key: %v", err)
	}

	seen := make(map[string]string, 64)
	for i := 0; i < 64; i++ {
		input := []byte(strings.Repeat("a", i) + ".claims")
		signature, err := Sign(key, input)
		if err != nil {
			t.Fatalf("Sign failed: %v", err)
		}
		// r is a function of the nonce alone, so a repeated r is a repeated
		// nonce -- the fatal case.
		r := hex.EncodeToString(signature[:scalarBytes])
		if previous, repeated := seen[r]; repeated {
			t.Fatalf("Messages %q and %q produced the same nonce; this leaks the private key", previous, input)
		}
		seen[r] = string(input)
	}
}

// TestSignProducesVerifiableSignatures checks the output against the standard
// library's verifier rather than against our own arithmetic.
func TestSignProducesVerifiableSignatures(t *testing.T) {
	for i := 0; i < 16; i++ {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("Could not generate a key: %v", err)
		}
		input := []byte(strings.Repeat("x", i))

		signature, err := Sign(key, input)
		if err != nil {
			t.Fatalf("Sign failed: %v", err)
		}
		digest := sha256.Sum256(input)
		r := new(big.Int).SetBytes(signature[:scalarBytes])
		s := new(big.Int).SetBytes(signature[scalarBytes:])
		if !ecdsa.Verify(&key.PublicKey, digest[:], r, s) {
			t.Fatalf("crypto/ecdsa rejected our signature for key %d", i)
		}
	}
}

// TestSignRejectsTheWrongCurve keeps the package honest about its name.
func TestSignRejectsTheWrongCurve(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("Could not generate a key: %v", err)
	}
	if _, err := Sign(key, []byte("anything")); err == nil {
		t.Error("Expected a P-384 key to be rejected")
	} else if !strings.Contains(err.Error(), "P-256") {
		t.Errorf("Expected the error to name the curve, got: %v", err)
	}
}

func TestSignRejectsANilKey(t *testing.T) {
	if _, err := Sign(nil, []byte("anything")); err == nil {
		t.Error("Expected a nil key to be rejected")
	}
}

// TestSignRejectsMalformedKeysWithoutPanicking is the regression test for a
// function that reported failure by returning an error and could still bring
// the process down.
//
// An *ecdsa.PrivateKey is an ordinary struct, so a caller can hand over any of
// these: the zero value has a nil Curve and a nil D, and a key unmarshalled from
// something unexpected can carry a D outside the group. Every one of them used
// to reach arithmetic or a FillBytes that panics -- and the curve check itself
// dereferenced the nil Curve while formatting the error meant to reject it, so
// the guard was the crash.
//
// A panic here is much worse than an error. Signing happens on the push path, so
// one malformed provider would take down a daemon serving every other service on
// the box, rather than failing that provider's pushes.
func TestSignRejectsMalformedKeysWithoutPanicking(t *testing.T) {
	n := elliptic.P256().Params().N

	valid, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Could not generate a key: %v", err)
	}

	// withD returns a P-256 key whose private scalar is d and whose public half
	// is the real one, so nothing but D is out of the ordinary.
	withD := func(d *big.Int) *ecdsa.PrivateKey {
		return &ecdsa.PrivateKey{PublicKey: valid.PublicKey, D: d}
	}

	cases := []struct {
		name string
		key  *ecdsa.PrivateKey
	}{
		{name: "the zero value", key: &ecdsa.PrivateKey{}},
		{
			name: "no curve",
			key:  &ecdsa.PrivateKey{D: new(big.Int).Set(valid.D)},
		},
		{
			name: "no private scalar",
			key:  &ecdsa.PrivateKey{PublicKey: valid.PublicKey},
		},
		{
			// A curve that is neither nil nor P-256, and whose Params returns
			// nil. The rejection path formatted its error by reading .Name off
			// exactly this value, so the guard meant to turn a bad key into an
			// error was itself the crash. elliptic.Curve is an interface, so
			// anything the standard library did not make can do this.
			name: "a curve whose Params is nil",
			key:  &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: paramlessCurve{}}, D: big.NewInt(1)},
		},
		{name: "a zero private scalar", key: withD(big.NewInt(0))},
		{name: "a negative private scalar", key: withD(big.NewInt(-1))},
		{
			// int2octets would truncate this into something exactly the size of
			// a key, and everything downstream would carry on as though it were
			// one.
			name: "a private scalar at the group order",
			key:  withD(new(big.Int).Set(n)),
		},
		{
			name: "a private scalar far above the group order",
			key:  withD(new(big.Int).Lsh(n, 64)),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// A panic fails the subtest with the stack rather than taking the
			// whole run down, so every case still gets reported.
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("Sign panicked instead of returning an error: %v", recovered)
				}
			}()

			if _, err := Sign(testCase.key, []byte("anything")); err == nil {
				t.Error("Expected a malformed key to be rejected")
			}
		})
	}
}

// TestSignAcceptsTheEdgesOfTheValidRange checks the range check did not become
// so eager that it rejects real keys. 1 and n-1 are both legitimate scalars, and
// an off-by-one in either bound would turn a working key into a refused one.
//
// The public halves are written down rather than computed. d=1 gives the
// generator G, and d=n-1 gives -G, which on a curve over a prime field is
// (Gx, p-Gy). Both come straight from the curve parameters, which avoids
// elliptic.ScalarBaseMult -- deprecated since Go 1.21 -- and states the
// arithmetic plainly instead of hiding it behind a call.
func TestSignAcceptsTheEdgesOfTheValidRange(t *testing.T) {
	params := elliptic.P256().Params()
	n := params.N

	cases := []struct {
		name string
		d    *big.Int
		x, y *big.Int
	}{
		{
			name: "the smallest valid scalar",
			d:    big.NewInt(1),
			x:    new(big.Int).Set(params.Gx),
			y:    new(big.Int).Set(params.Gy),
		},
		{
			name: "the largest valid scalar",
			d:    new(big.Int).Sub(n, big.NewInt(1)),
			x:    new(big.Int).Set(params.Gx),
			y:    new(big.Int).Sub(params.P, params.Gy),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			key := &ecdsa.PrivateKey{
				PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: testCase.x, Y: testCase.y},
				D:         testCase.d,
			}

			// Sign verifies its own output against this public key before
			// returning, so a success here also confirms the point above is the
			// right one.
			signature, err := Sign(key, []byte("anything"))
			if err != nil {
				t.Fatalf("A valid scalar at the edge of the range was rejected: %v", err)
			}
			if len(signature) != SignatureSize {
				t.Errorf("Expected %d bytes, got %d", SignatureSize, len(signature))
			}
		})
	}
}

// TestBlindedInverseAgreesWithTheUnblindedOne checks the countermeasure did not
// change the answer, only how it is reached.
func TestBlindedInverseAgreesWithTheUnblindedOne(t *testing.T) {
	n := elliptic.P256().Params().N
	for i := 0; i < 64; i++ {
		k, err := rand.Int(rand.Reader, new(big.Int).Sub(n, big.NewInt(1)))
		if err != nil {
			t.Fatalf("Could not pick a nonce: %v", err)
		}
		k.Add(k, big.NewInt(1))

		got, err := blindedInverse(k, n)
		if err != nil {
			t.Fatalf("blindedInverse failed: %v", err)
		}
		want := new(big.Int).ModInverse(k, n)
		if got.Cmp(want) != 0 {
			t.Fatalf("Blinded inverse of %X disagrees with the direct one", k)
		}
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// paramlessCurve is an elliptic.Curve whose Params returns nil.
//
// Not a realistic curve -- it is a realistic *caller mistake*, and the only way
// to reach the branch that formats the "wrong curve" error. Every method panics
// except Params, because Sign must reject this key before reaching any of them.
type paramlessCurve struct{}

func (paramlessCurve) Params() *elliptic.CurveParams { return nil }

func (paramlessCurve) IsOnCurve(*big.Int, *big.Int) bool {
	panic("es256 reached curve arithmetic on a curve it should have rejected")
}

func (paramlessCurve) Add(*big.Int, *big.Int, *big.Int, *big.Int) (*big.Int, *big.Int) {
	panic("es256 reached curve arithmetic on a curve it should have rejected")
}

func (paramlessCurve) Double(*big.Int, *big.Int) (*big.Int, *big.Int) {
	panic("es256 reached curve arithmetic on a curve it should have rejected")
}

func (paramlessCurve) ScalarMult(*big.Int, *big.Int, []byte) (*big.Int, *big.Int) {
	panic("es256 reached curve arithmetic on a curve it should have rejected")
}

func (paramlessCurve) ScalarBaseMult([]byte) (*big.Int, *big.Int) {
	panic("es256 reached curve arithmetic on a curve it should have rejected")
}

// TestSignRejectsAKeyWithNoPublicPoint covers the guard between a malformed key
// and Sign's own verification.
//
// Sign checks its output with crypto/ecdsa before returning it, and
// ecdsa.Verify dereferences the public X and Y without guarding them. An
// *ecdsa.PrivateKey is a struct a caller can assemble field by field, so one
// carrying a valid scalar and no public point is reachable -- and it used to
// reach that call and panic, taking the process down inside the guards whose
// entire purpose is to turn a bad key into an error.
//
// Sign promises an error for every malformed key. This is the shape that broke
// the promise.
func TestSignRejectsAKeyWithNoPublicPoint(t *testing.T) {
	valid := rfc6979Key(t)

	for name, key := range map[string]*ecdsa.PrivateKey{
		"no public point": {
			PublicKey: ecdsa.PublicKey{Curve: elliptic.P256()},
			D:         valid.D,
		},
		"only X": {
			PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: valid.X},
			D:         valid.D,
		},
		"only Y": {
			PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), Y: valid.Y},
			D:         valid.D,
		},
		"X out of range": {
			PublicKey: ecdsa.PublicKey{
				Curve: elliptic.P256(),
				X:     new(big.Int).Add(elliptic.P256().Params().P, big.NewInt(1)),
				Y:     valid.Y,
			},
			D: valid.D,
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Any panic fails the test rather than the run: a panic here is the
			// bug, not a test error.
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("Sign panicked instead of returning an error: %v", recovered)
				}
			}()

			signature, err := Sign(key, []byte("sample"))
			if err == nil {
				t.Fatalf("Expected a key with %s to be refused, got a %d-byte signature",
					name, len(signature))
			}
		})
	}
}
