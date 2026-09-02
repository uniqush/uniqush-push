package http_api //nolint:revive

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

const (
	testKeyID  = "ABCDE12345"
	testTeamID = "TEAM123456"
)

// writeSigningKey writes a P-256 key in the PEM/PKCS#8 form Apple issues.
func writeSigningKey(t *testing.T) (string, *ecdsa.PrivateKey) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Could not generate a key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("Could not marshal the key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "AuthKey_"+testKeyID+".p8")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatalf("Could not write the key: %v", err)
	}
	return path, key
}

// rsaKeyPEM builds a PKCS#8 RSA key, standing in for the private key of an APNs
// push certificate.
func rsaKeyPEM(t *testing.T) []byte {
	t.Helper()
	// 1024 bits: not a key anyone should use, and this one only has to parse.
	key, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // a test fixture that is never used for anything
	if err != nil {
		t.Fatalf("Could not generate an RSA key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("Could not marshal the RSA key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func tokenAuthPSP(t *testing.T, authKeyPath string) *push.PushServiceProvider {
	t.Helper()
	psp := push.NewEmptyPushServiceProvider()
	psp.FixedData["service"] = "tokentest"
	psp.VolatileData[common.AuthKeyKey] = authKeyPath
	psp.VolatileData[common.KeyIDKey] = testKeyID
	psp.VolatileData[common.TeamIDKey] = testTeamID
	return psp
}

func newTokenProcessor() *HTTPPushRequestProcessor {
	return NewRequestProcessor().(*HTTPPushRequestProcessor)
}

// TestProviderTokenIsAgedOnTheWallClock guards a class of outage that only
// appears after the host's clock is corrected.
//
// time.Now attaches a monotonic reading, and time.Time.Sub prefers it whenever
// both operands carry one. A monotonic clock counts evenly through exactly the
// two events that matter here -- a forward step from NTP, and a resume from
// suspend -- while Apple has no such reading: it ages a token by comparing the
// wall-clock second in the iat claim against its own wall clock.
//
// So if issuedAt kept its monotonic reading, a host that jumped forward an hour
// would leave uniqush convinced the token was minutes old while Apple treated it
// as long expired, and every push would fail with ExpiredProviderToken until the
// monotonic window elapsed.
//
// The jump itself cannot be staged in-process: Add moves the wall and monotonic
// readings together, and a monotonic reading cannot be synthesised. So this
// asserts the invariant that forecloses the bug instead. A Time is equal to its
// own Round(0) only when it carries no monotonic reading, and once issuedAt has
// none, every Sub against it falls back to wall-clock arithmetic whatever the
// caller passes in.
func TestProviderTokenIsAgedOnTheWallClock(t *testing.T) {
	path, _ := writeSigningKey(t)
	processor := newTokenProcessor()

	// time.Now rather than a constructed date, because a constructed time never
	// carries a monotonic reading and would make this pass for the wrong reason.
	if _, _, err := processor.getProviderToken(tokenAuthPSP(t, path), time.Now()); err != nil {
		t.Fatalf("Could not mint a provider token: %v", err)
	}

	processor.tokensLock.RLock()
	defer processor.tokensLock.RUnlock()

	if len(processor.tokens) != 1 {
		t.Fatalf("Expected one cached token, got %d", len(processor.tokens))
	}

	// The cache is keyed on the bucket's Unix second, and issuedAtBucket builds
	// its result with time.Unix, so a monotonic reading cannot survive into it.
	// Reconstructing the bucket and comparing against Round(0) proves that: a
	// Time equals its own Round(0) only when it carries no monotonic reading.
	for _, cached := range processor.tokens {
		cached.mutex.Lock()
		buckets := make([]int64, 0, len(cached.signed))
		for bucket := range cached.signed {
			buckets = append(buckets, bucket)
		}
		cached.mutex.Unlock()

		if len(buckets) == 0 {
			t.Fatal("Expected the mint to have cached a token")
		}
		for _, bucket := range buckets {
			issuedAt := time.Unix(bucket, 0).UTC()
			if issuedAt != issuedAt.Round(0) {
				t.Error("The cached bucket carries a monotonic reading, so the token's age is " +
					"measured on a clock Apple cannot see.\n" +
					"After a forward clock correction or a host resume, uniqush would go on " +
					"serving a token Apple already considers expired, failing every push until " +
					"the monotonic window ran out.")
			}
		}
	}
}

// TestProviderTokenIsSignedTheWayAppleExpects checks the JWT itself.
//
// Every field here produces the same 403 InvalidProviderToken when wrong, with
// nothing to say which one, so getting them individually under test is worth
// more than it usually would be.
func TestProviderTokenIsSignedTheWayAppleExpects(t *testing.T) {
	path, key := writeSigningKey(t)
	processor := newTokenProcessor()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	signed, _, err := processor.getProviderToken(tokenAuthPSP(t, path), now)
	if err != nil {
		t.Fatalf("Could not mint a provider token: %v", err)
	}

	parsed, err := jwt.Parse(signed, func(*jwt.Token) (interface{}, error) {
		return &key.PublicKey, nil
	}, jwt.WithoutClaimsValidation())
	if err != nil {
		t.Fatalf("The token does not verify against its own signing key: %v", err)
	}

	if parsed.Method != jwt.SigningMethodES256 {
		t.Errorf("Expected ES256, got %v. Apple accepts no other algorithm", parsed.Header["alg"])
	}
	// The key id belongs in the header, not the claims: Apple uses it to choose
	// which of the team's public keys to verify with.
	if kid, _ := parsed.Header["kid"].(string); kid != testKeyID {
		t.Errorf("Expected kid %q in the header, got %q", testKeyID, kid)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("Expected map claims")
	}
	if issuer, _ := claims["iss"].(string); issuer != testTeamID {
		t.Errorf("Expected iss %q (the team id), got %q", testTeamID, issuer)
	}
	issuedAt, hasIat := claims["iat"].(float64)
	if !hasIat {
		t.Fatal("Expected an iat claim; APNs rejects a token without one")
	}
	// iat is the *bucket*, not the moment the token was requested. That is the
	// whole point of the scheme: two instances asking at different instants
	// inside one bucket have to produce byte-identical tokens, which they cannot
	// do if the claim carries the caller's clock.
	if want := issuedAtBucket(now).Unix(); int64(issuedAt) != want {
		t.Errorf("Expected iat %d (the bucket containing %s), got %d",
			want, now.Format(time.TimeOnly), int64(issuedAt))
	}
	// Apple derives expiry from iat rather than reading an exp claim, and a
	// token carrying one has been reported to be rejected.
	if _, hasExp := claims["exp"]; hasExp {
		t.Error("Expected no exp claim: APNs expects expiry to be implied by iat")
	}
}

// TestProviderTokenCachesBothLiveBuckets is the regression test for a cache
// that spent its whole life thrashing.
//
// Every batch asks for two tokens: the current bucket's, and the previous
// bucket's to carry as a fallback. With a single slot the second call evicted
// what the first had just stored, so the next batch started with a miss and the
// pair fought each other forever -- two ECDSA signatures per batch, plus
// contention on the token mutex, from something that looked like a cache and
// reported no error.
//
// The CPU was the smaller half. srv/apns/es256 states plainly that its signer is
// not constant-time and rests on running about once per bucket; signing on every
// batch is precisely the usage that assumption excludes, so this was quietly
// undermining the security argument for the module's existence.
func TestProviderTokenCachesBothLiveBuckets(t *testing.T) {
	path, _ := writeSigningKey(t)
	processor := newTokenProcessor()
	psp := tokenAuthPSP(t, path)

	// Mid-bucket, so a previous bucket exists and has not expired.
	now := issuedAtBucket(time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)).Add(5 * time.Minute)

	// What one push batch does, twice over.
	for round := 0; round < 2; round++ {
		if _, _, err := processor.getProviderToken(psp, now); err != nil {
			t.Fatalf("round %d: could not get the current token: %v", round, err)
		}
		if _, err := processor.previousProviderToken(psp, now); err != nil {
			t.Fatalf("round %d: could not get the fallback: %v", round, err)
		}
	}

	processor.tokensLock.RLock()
	defer processor.tokensLock.RUnlock()

	for _, cached := range processor.tokens {
		cached.mutex.Lock()
		buckets := len(cached.signed)
		cached.mutex.Unlock()

		if buckets != 2 {
			t.Errorf("Expected both live buckets to be cached, got %d entry/entries.\n"+
				"With one slot the current and previous tokens evict each other, so every "+
				"batch re-signs both.", buckets)
		}
	}
}

// TestProviderTokenSignsOncePerBucket counts signatures rather than cache
// entries.
//
// srv/apns/es256 ships a signer that is deliberately not constant time, and the
// argument for that is entirely about rate: roughly one signature per key per
// bucket, on a server, over a message an attacker does not choose. Its package
// comment names this test as the guard.
//
// Cache entries cannot be that guard. Signing is deterministic, so re-signing
// on every call returns identical bytes and every assertion about token values
// passes either way; and a map that is written to but never read still ends up
// the right size. Only a count of the signatures themselves can tell.
func TestProviderTokenSignsOncePerBucket(t *testing.T) {
	path, _ := writeSigningKey(t)
	processor := newTokenProcessor()
	psp := tokenAuthPSP(t, path)

	var signatures atomic.Int64
	previous := signES256
	signES256 = func(key *ecdsa.PrivateKey, input []byte) ([]byte, error) {
		signatures.Add(1)
		return previous(key, input)
	}
	t.Cleanup(func() { signES256 = previous })

	now := issuedAtBucket(time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)).Add(5 * time.Minute)

	// Ten batches, each asking for the current token and then the fallback, the
	// way sendRequests does.
	for round := 0; round < 10; round++ {
		if _, _, err := processor.getProviderToken(psp, now); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if _, err := processor.previousProviderToken(psp, now); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}

	// Two: one for the current bucket, one for the previous. Not twenty.
	if got := signatures.Load(); got != 2 {
		t.Errorf("Ten batches produced %d ECDSA signatures, expected 2 (one per live bucket).\n"+
			"srv/apns/es256 is not constant time and rests on being called about once per "+
			"bucket; signing per batch moves it into a regime it was never justified for.", got)
	}

	// And crossing into the next bucket costs exactly one more.
	if _, _, err := processor.getProviderToken(psp, now.Add(TokenRefreshInterval)); err != nil {
		t.Fatalf("next bucket: %v", err)
	}
	if got := signatures.Load(); got != 3 {
		t.Errorf("Crossing a bucket boundary produced %d signatures in total, expected 3", got)
	}
}

// TestProviderTokenCacheDoesNotGrowWithoutBound is the other side of caching two
// buckets: entries that can never be presented again have to go, or a
// long-running process accumulates one per bucket for as long as it lives.
func TestProviderTokenCacheDoesNotGrowWithoutBound(t *testing.T) {
	path, _ := writeSigningKey(t)
	processor := newTokenProcessor()
	psp := tokenAuthPSP(t, path)

	now := issuedAtBucket(time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))

	// A day of pushes, one per bucket.
	for step := 0; step < 24*60/int(tokenRefreshInterval/time.Minute); step++ {
		if _, _, err := processor.getProviderToken(psp, now); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if _, err := processor.previousProviderToken(psp, now); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		now = now.Add(tokenRefreshInterval)
	}

	// At most one bucket per lifetime, plus the one being written.
	maxLive := int(tokenLifetime/tokenRefreshInterval) + 2

	processor.tokensLock.RLock()
	defer processor.tokensLock.RUnlock()

	for _, cached := range processor.tokens {
		cached.mutex.Lock()
		buckets := len(cached.signed)
		cached.mutex.Unlock()

		if buckets > maxLive {
			t.Errorf("The token cache holds %d buckets after a day, more than the %d that can "+
				"still be presented: expired entries are never dropped", buckets, maxLive)
		}
	}
}

// TestProviderTokenRefusalIsRecordedAgainstTheSignedBucket covers a
// misattribution that only shows up near a boundary.
//
// A request signed just before a boundary can have its 429 arrive just after
// one. Dating the refusal by the clock at that moment blames the bucket Apple
// has never been shown, and the memo then sends every later push straight to the
// previous bucket -- which is the token that was actually rejected. Both the
// primary and the fallback would then be known-bad for the whole memo window.
func TestProviderTokenRefusalIsRecordedAgainstTheSignedBucket(t *testing.T) {
	path, _ := writeSigningKey(t)
	processor := newTokenProcessor()
	psp := tokenAuthPSP(t, path)

	// Signed one second before a boundary; the answer comes back one second
	// after it.
	boundary := issuedAtBucket(time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)).Add(tokenRefreshInterval)
	signedAt := boundary.Add(-time.Second)
	observedAt := boundary.Add(time.Second)

	signed, signedBucket, err := processor.getProviderToken(psp, signedAt)
	if err != nil {
		t.Fatalf("Could not mint: %v", err)
	}
	if !signedBucket.Equal(issuedAtBucket(signedAt)) {
		t.Fatalf("Expected the reported bucket to be the one signed for")
	}

	processor.noteProviderTokenRefused(psp, signedBucket, observedAt)

	// The new bucket has not been refused, so it is still what gets offered.
	afterBoundary, bucket, err := processor.getProviderToken(psp, observedAt)
	if err != nil {
		t.Fatalf("Could not get a token after the boundary: %v", err)
	}
	if !bucket.Equal(boundary) {
		t.Errorf("Expected the untried bucket %s to be offered after the boundary, got %s.\n"+
			"Recording the refusal against the response clock blames a bucket Apple has never "+
			"seen, and skips straight to the token it actually rejected.",
			boundary.Format(time.TimeOnly), bucket.Format(time.TimeOnly))
	}
	if afterBoundary == signed {
		t.Error("The token Apple just refused was offered again as the primary")
	}

	// And the refused one is not offered as the fallback either.
	fallback, err := processor.previousProviderToken(psp, observedAt)
	if err != nil {
		t.Fatalf("Could not compute the fallback: %v", err)
	}
	if fallback == signed {
		t.Error("The refused token was offered as the fallback. Retrying with a token already " +
			"known to be rejected costs a second round trip and cannot succeed.")
	}
}

// TestProviderTokenRefreshWindow is the test for the constraint that makes this
// awkward: uniqush must re-sign often enough to stay inside Apple's one-hour
// expiry, and rarely enough to stay outside its one-per-20-minutes mint limit.
//
// Both edges fail as a 4xx on every push, so a refresh interval that drifts to
// either side is an outage rather than a degradation.
func TestProviderTokenRefreshWindow(t *testing.T) {
	path, _ := writeSigningKey(t)
	processor := newTokenProcessor()
	psp := tokenAuthPSP(t, path)
	// Rounded down to a real boundary. The offsets below are relative to the
	// start of a bucket, so a start that is already partway into one would put
	// "just before the refresh interval" on the far side of the next boundary.
	// Buckets align to the Unix epoch, so a round wall-clock time is not one.
	start := issuedAtBucket(time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))

	first, _, err := processor.getProviderToken(psp, start)
	if err != nil {
		t.Fatalf("Could not mint the first token: %v", err)
	}

	cases := []struct {
		name    string
		after   time.Duration
		refresh bool
	}{
		{name: "immediately", after: 0},
		{name: "after a minute", after: time.Minute},
		// Past Apple's mint floor, but re-signing now would waste the
		// allowance for no reason.
		{name: "after 21 minutes", after: 21 * time.Minute},
		{name: "just before the refresh interval", after: tokenRefreshInterval - time.Second},
		{name: "at the refresh interval", after: tokenRefreshInterval, refresh: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			signed, _, err := processor.getProviderToken(psp, start.Add(testCase.after))
			if err != nil {
				t.Fatalf("Could not get a token: %v", err)
			}
			if testCase.refresh && signed == first {
				t.Errorf("Expected a fresh token %s after the first", testCase.after)
			}
			if !testCase.refresh && signed != first {
				t.Errorf("Expected the cached token to be reused %s after the first", testCase.after)
			}
		})
	}
}

// TestProviderTokenRefreshIntervalRespectsApplesBounds pins the constant itself.
//
// The value is only correct relative to two numbers Apple publishes, and a
// plausible-looking change to it -- rounding to half an hour, say, or matching
// the one-hour lifetime -- lands outside them. Reading it back from the
// constants is the only way to make that a test failure rather than a
// production incident.
func TestProviderTokenRefreshIntervalRespectsApplesBounds(t *testing.T) {
	if tokenRefreshInterval <= tokenMintFloor {
		t.Errorf("A refresh interval of %s is inside Apple's %s minimum between tokens; "+
			"a busy provider would get TooManyProviderTokenUpdates", tokenRefreshInterval, tokenMintFloor)
	}
	if tokenRefreshInterval >= tokenLifetime {
		t.Errorf("A refresh interval of %s does not renew before Apple's %s expiry; "+
			"every push would fail with ExpiredProviderToken", tokenRefreshInterval, tokenLifetime)
	}
	// A margin below the ceiling, since the expiry is judged by Apple's clock
	// rather than ours and the two need not agree.
	if margin := tokenLifetime - tokenRefreshInterval; margin < 10*time.Minute {
		t.Errorf("Only %s of margin before expiry; too little to absorb clock skew", margin)
	}

	// The binding constraint, and the one a 45-minute interval quietly failed.
	//
	// Recovery from TooManyProviderTokenUpdates means presenting the previous
	// bucket's token, so that token has to outlive the refusal. Worst case is a
	// first use at the very end of a bucket: the floor clears at bucketStart +
	// interval + floor, while the previous token expires at bucketStart +
	// lifetime. Widen the interval past lifetime - floor and there is a window
	// with no usable token at all -- neither the refused one nor the expired one.
	if tokenRefreshInterval+tokenMintFloor > tokenLifetime {
		t.Errorf("A %s interval leaves the previous token expired before Apple's %s floor clears "+
			"(%s + %s > %s), so a late first use would have no usable token at all",
			tokenRefreshInterval, tokenMintFloor,
			tokenRefreshInterval, tokenMintFloor, tokenLifetime)
	}

	// And strictly inside it, with room for the clocks to disagree.
	//
	// Both ends of the constraint above are measured on Apple's clock, not ours:
	// the floor starts when Apple observes a token, and the expiry is judged
	// against the iat we wrote. Sitting exactly on the bound leaves nothing for
	// that difference, nor for the time a request spends in flight. With uniqush
	// a minute behind, a fallback first observed at local +39:30 reaches the end
	// of the floor at local +59:00 while Apple already calls it expired -- so the
	// recovery returns ExpiredProviderToken and the push is dropped as a
	// credential failure, which is the outage the fallback exists to prevent.
	if tokenRefreshInterval+tokenMintFloor+tokenSkewMargin > tokenLifetime {
		t.Errorf("A %s interval leaves only %s of slack against Apple's clock (%s + %s + %s > %s). "+
			"The fallback can expire before the floor clears, turning a recoverable refusal into "+
			"a dropped push.",
			tokenRefreshInterval, tokenLifetime-tokenRefreshInterval-tokenMintFloor,
			tokenRefreshInterval, tokenMintFloor, tokenSkewMargin, tokenLifetime)
	}
	if tokenSkewMargin <= 0 {
		t.Error("The skew margin must be positive; it is the only allowance for uniqush's clock " +
			"and Apple's disagreeing")
	}
}

// TestProviderTokenIsSharedAcrossProviders checks the cache is keyed on the
// signing key rather than on the provider.
//
// Apple's mint limit is per key, so two services sharing a .p8 must share the
// token and its schedule. Refreshing independently would trip the limit exactly
// when both are busy, which is the worst time to discover it.
func TestProviderTokenIsSharedAcrossProviders(t *testing.T) {
	path, _ := writeSigningKey(t)
	processor := newTokenProcessor()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	first := tokenAuthPSP(t, path)
	second := tokenAuthPSP(t, path)
	second.FixedData["service"] = "anotherservice"

	firstToken, _, err := processor.getProviderToken(first, now)
	if err != nil {
		t.Fatalf("Could not mint a token: %v", err)
	}
	secondToken, _, err := processor.getProviderToken(second, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Could not mint a token: %v", err)
	}
	if firstToken != secondToken {
		t.Error("Two services sharing a signing key each minted their own token; " +
			"Apple's rate limit is per key, so they must share one")
	}

	// A different key must not share, or rotating one service's key would
	// silently keep signing with the other's.
	otherPath, _ := writeSigningKey(t)
	otherToken, _, err := processor.getProviderToken(tokenAuthPSP(t, otherPath), now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Could not mint a token: %v", err)
	}
	if otherToken == firstToken {
		t.Error("A provider with a different signing key reused another key's token")
	}
}

// TestProviderTokenIsSharedAcrossPathsToOneKey is the same rule stated against
// the thing Apple actually counts.
//
// Apple's limit is per signing key. A pathname is not a key: the same .p8
// reached by an absolute and a relative path, through a symlink, or copied into
// two directories, is one key to Apple and would be two entries to a cache keyed
// on the path -- each with its own mint schedule, each mint counting against the
// same 20-minute floor. The failure surfaces only when both services are busy,
// which is the worst time to discover it.
func TestProviderTokenIsSharedAcrossPathsToOneKey(t *testing.T) {
	path, _ := writeSigningKey(t)
	processor := newTokenProcessor()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Could not read the signing key: %v", err)
	}

	// The same key, saved somewhere else under a different name.
	copied := filepath.Join(t.TempDir(), "copy-of-the-same-key.p8")
	if err = os.WriteFile(copied, contents, 0600); err != nil {
		t.Fatalf("Could not copy the signing key: %v", err)
	}

	// And reached through a symlink, which is how a deployment that keeps
	// credentials in one place and links them per service looks.
	linked := filepath.Join(t.TempDir(), "linked.p8")
	if err = os.Symlink(path, linked); err != nil {
		t.Skipf("This filesystem does not support symlinks: %v", err)
	}

	first, _, err := processor.getProviderToken(tokenAuthPSP(t, path), now)
	if err != nil {
		t.Fatalf("Could not mint a token: %v", err)
	}
	for name, alias := range map[string]string{"a copy": copied, "a symlink": linked} {
		token, _, err := processor.getProviderToken(tokenAuthPSP(t, alias), now.Add(time.Minute))
		if err != nil {
			t.Fatalf("Could not get a token through %s: %v", name, err)
		}
		if token != first {
			t.Errorf("%s of the signing key got its own token; Apple's mint limit is per key, "+
				"so both would count against the same 20-minute floor", name)
		}
	}
}

// TestProviderTokenIsIdenticalAcrossProcessors is the property the whole
// deterministic-signing exercise exists to produce.
//
// Two independent processors stand in for two uniqush instances, or for the
// same instance before and after a restart. Neither shares state with the
// other, and they must still present Apple with byte-identical tokens -- that
// is what makes a restart cost no mint and a second instance cost none either.
//
// With jwt.SigningMethodES256 this fails, because ECDSA draws a random nonce
// and the two signatures differ even though the claims are identical.
func TestProviderTokenIsIdenticalAcrossProcessors(t *testing.T) {
	path, _ := writeSigningKey(t)
	psp := tokenAuthPSP(t, path)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	first, _, err := newTokenProcessor().getProviderToken(psp, now)
	if err != nil {
		t.Fatalf("Could not mint a token in the first processor: %v", err)
	}
	// A second processor with an empty cache, a few minutes later in the same
	// bucket -- the situation after a restart.
	second, _, err := newTokenProcessor().getProviderToken(psp, now.Add(7*time.Minute))
	if err != nil {
		t.Fatalf("Could not mint a token in the second processor: %v", err)
	}

	if first != second {
		t.Error("Two processors produced different tokens for the same bucket; " +
			"a restart or a second instance would count as a new mint against Apple's 20-minute floor")
	}
}

// TestProviderTokenBucketing pins the schedule the agreement depends on.
func TestProviderTokenBucketing(t *testing.T) {
	// A real bucket boundary, derived from the interval rather than hardcoded.
	// This previously rounded to a literal 2700 seconds and kept doing so after
	// the interval changed, which left it aligning to a boundary that no longer
	// existed.
	step := int64(tokenRefreshInterval / time.Second)
	base := time.Unix(time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC).Unix()/step*step, 0).UTC()
	if !issuedAtBucket(base).Equal(base) {
		t.Fatalf("%s is not a bucket boundary, so the cases below prove nothing", base)
	}

	cases := []struct {
		name       string
		offset     time.Duration
		sameBucket bool
	}{
		{name: "at the boundary", offset: 0, sameBucket: true},
		{name: "one second in", offset: time.Second, sameBucket: true},
		{name: "halfway", offset: tokenRefreshInterval / 2, sameBucket: true},
		{name: "one second before the next", offset: tokenRefreshInterval - time.Second, sameBucket: true},
		{name: "the next boundary", offset: tokenRefreshInterval, sameBucket: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := issuedAtBucket(base.Add(testCase.offset))
			if same := got.Equal(base); same != testCase.sameBucket {
				t.Errorf("Expected sameBucket=%v at %s, got bucket %s", testCase.sameBucket, testCase.offset, got)
			}
		})
	}

	// The bucket has to sit inside Apple's window from both sides, or the
	// agreement is worthless: a token is at most one interval old when used.
	if tokenRefreshInterval >= tokenLifetime {
		t.Errorf("A %s bucket can serve a token older than Apple's %s expiry", tokenRefreshInterval, tokenLifetime)
	}
	if tokenRefreshInterval <= tokenMintFloor {
		t.Errorf("A %s bucket mints faster than Apple's %s floor", tokenRefreshInterval, tokenMintFloor)
	}
}

// TestProviderTokenReportsBadKeysUsefully covers the failures that happen at
// push time rather than at /addpsp time -- a key that was readable when the
// provider was registered and is not any more.
func TestProviderTokenReportsBadKeysUsefully(t *testing.T) {
	processor := newTokenProcessor()
	now := time.Now()

	t.Run("an ECDSA key on the wrong curve", func(t *testing.T) {
		// A P-384 key parses as a perfectly good ECDSA key, so nothing before
		// the curve check notices. ES256 means P-256, and ES256 is the only
		// algorithm APNs accepts, so without the check the mistake passes
		// /addpsp and is caught by Apple on the first push instead.
		key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err != nil {
			t.Fatalf("Could not generate a P-384 key: %v", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatalf("Could not marshal the key: %v", err)
		}
		path := filepath.Join(t.TempDir(), "AuthKey_P384.p8")
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
			t.Fatalf("Could not write the key: %v", err)
		}

		if _, _, err := processor.getProviderToken(tokenAuthPSP(t, path), now); err == nil {
			t.Error("Expected a P-384 key to be rejected")
		} else if !strings.Contains(err.Error(), "P-256") {
			t.Errorf("Expected the error to name the curve APNs requires, got: %v", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		psp := tokenAuthPSP(t, filepath.Join(t.TempDir(), "absent.p8"))
		_, _, err := processor.getProviderToken(psp, now)
		if err == nil {
			t.Fatal("Expected an error for a missing signing key")
		}
		if !strings.Contains(err.Error(), "authkey") {
			t.Errorf("Expected the error to name the setting, got: %v", err)
		}
	})

	t.Run("a certificate's RSA key", func(t *testing.T) {
		// The realistic mistake: pointing authkey at the private key of an APNs
		// push certificate. It is a valid PEM private key and useless here, so
		// the message has to say why rather than just failing to parse.
		path := filepath.Join(t.TempDir(), "rsa.p8")
		if err := os.WriteFile(path, rsaKeyPEM(t), 0600); err != nil {
			t.Fatalf("Could not write the fixture: %v", err)
		}
		_, _, err := processor.getProviderToken(tokenAuthPSP(t, path), now)
		if err == nil {
			t.Fatal("Expected an error for an RSA key")
		}
		if !strings.Contains(err.Error(), "ECDSA") {
			t.Errorf("Expected the error to explain an ECDSA key is needed, got: %v", err)
		}
	})
}
