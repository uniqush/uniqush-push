package es256

import (
	"crypto/elliptic"
	"crypto/sha256"
	"testing"
)

// TestNonceGeneratorAdvancesTheDRBGOnRetry covers RFC 6979 section 3.2 step h's
// rejection path.
//
// ECDSA requires r != 0 and s != 0, and when Sign rejects a candidate for
// either it asks the generator for another. The RFC is specific about how the
// generator moves on -- K = HMAC_K(V || 0x00), then V = HMAC_K(V) -- and
// getting it wrong would not be visible from anywhere else: the second nonce
// would still be a uniform-looking value in range, and every signature made
// with it would still verify. It would simply be a nonce nobody else derives,
// which defeats the whole reason for deriving it from a published standard.
//
// The path is astronomically rare in practice, which is exactly why it needs a
// test rather than exercise. rfc6979.go says it "is tested"; nothing called
// next() twice, so neither this transition nor the primed flag that
// distinguishes the first candidate from the rest was covered at all.
//
// The expected state is computed here from the RFC's own steps rather than
// captured from the implementation, so this cannot pass by agreeing with a bug.
func TestNonceGeneratorAdvancesTheDRBGOnRetry(t *testing.T) {
	key := rfc6979Key(t)
	digest := sha256.Sum256([]byte("sample"))
	n := elliptic.P256().Params().N

	generator := newNonceGenerator(int2octets(key.D), digest[:], n)

	first := generator.next()
	if expected := mustHexInt(t, rfc6979SHA256Vectors[0].k); first.Cmp(expected) != 0 {
		t.Fatalf("The first candidate is not the RFC's k for \"sample\".\ngot      %x\nexpected %x",
			first, expected)
	}

	// Step h's rejection branch, applied to the state the generator is in now.
	expectedK := mac(generator.k, generator.v, []byte{0x00})
	expectedV := mac(expectedK, generator.v)
	var expectedT []byte
	v := expectedV
	for len(expectedT)*8 < scalarBits {
		v = mac(expectedK, v)
		expectedT = append(expectedT, v...)
	}
	expected := bits2int(expectedT)

	second := generator.next()
	if second.Cmp(expected) != 0 {
		t.Errorf("The second candidate does not follow RFC 6979 section 3.2 step h.\n"+
			"got      %x\nexpected %x\n"+
			"A different advance still produces nonces that verify, and that nobody else "+
			"derives -- which is the one failure determinism cannot survive.", second, expected)
	}
	if second.Cmp(first) == 0 {
		t.Error("The generator returned the same candidate twice; a repeated nonce reveals the key")
	}
	if second.Sign() <= 0 || second.Cmp(n) >= 0 {
		t.Errorf("The second candidate is out of range for P-256: %x", second)
	}
}

// TestNonceGeneratorIsDeterministicAcrossRetries checks the retry path keeps the
// property the whole package exists for.
//
// Two processes that both reject a first candidate must still arrive at the
// same second one, or a rejection would reintroduce exactly the divergence
// deterministic signing removes.
func TestNonceGeneratorIsDeterministicAcrossRetries(t *testing.T) {
	key := rfc6979Key(t)
	digest := sha256.Sum256([]byte("test"))
	n := elliptic.P256().Params().N

	candidates := func() []string {
		generator := newNonceGenerator(int2octets(key.D), digest[:], n)
		var out []string
		for i := 0; i < 4; i++ {
			out = append(out, generator.next().Text(16))
		}
		return out
	}

	first, second := candidates(), candidates()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("Candidate %d differs between two generators built from the same inputs:\n%s\n%s",
				i, first[i], second[i])
		}
	}
	// Distinct, too: a generator that got stuck would be "deterministic" and
	// catastrophic.
	seen := map[string]bool{}
	for i, candidate := range first {
		if seen[candidate] {
			t.Errorf("Candidate %d repeats an earlier one: %s", i, candidate)
		}
		seen[candidate] = true
	}
}
