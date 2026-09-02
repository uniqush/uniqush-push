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
	if _, err := processor.getProviderToken(tokenAuthPSP(t, path), time.Now()); err != nil {
		t.Fatalf("Could not mint a provider token: %v", err)
	}

	processor.tokensLock.RLock()
	defer processor.tokensLock.RUnlock()

	if len(processor.tokens) != 1 {
		t.Fatalf("Expected one cached token, got %d", len(processor.tokens))
	}

	// The cached bucket is built by issuedAtBucket with time.Unix, so a
	// monotonic reading cannot survive into it. A Time equals its own Round(0)
	// only when it carries none, which is what this asserts.
	for _, cached := range processor.tokens {
		cached.mutex.Lock()
		bucket := cached.signedBucket
		signed := cached.signed
		cached.mutex.Unlock()

		if signed == "" {
			t.Fatal("Expected the mint to have cached a token")
		}
		if bucket != bucket.Round(0) {
			t.Error("The cached bucket carries a monotonic reading, so the token's age is " +
				"measured on a clock Apple cannot see.\n" +
				"After a forward clock correction or a host resume, uniqush would go on " +
				"serving a token Apple already considers expired, failing every push until " +
				"the monotonic window elapsed.")
		}
	}
}

func TestProviderTokenIsSignedTheWayAppleExpects(t *testing.T) {
	path, key := writeSigningKey(t)
	processor := newTokenProcessor()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	signed, err := processor.getProviderToken(tokenAuthPSP(t, path), now)
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

	first, err := processor.getProviderToken(psp, start)
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
			signed, err := processor.getProviderToken(psp, start.Add(testCase.after))
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
// TestProviderTokenRefreshIntervalRespectsApplesBounds pins the interval
// between Apple's two limits.
//
// Both edges take a provider completely offline rather than degrading it: a
// token older than an hour is refused with ExpiredProviderToken, and minting
// again inside 20 minutes is refused with TooManyProviderTokenUpdates. An
// interval outside either bound fails every push for the key, so the
// relationship is asserted rather than left to the comment on the constants.
func TestProviderTokenRefreshIntervalRespectsApplesBounds(t *testing.T) {
	if tokenRefreshInterval >= tokenLifetime {
		t.Errorf("The refresh interval (%v) must be shorter than Apple's token lifetime (%v), "+
			"or every push fails with ExpiredProviderToken before uniqush re-signs.",
			tokenRefreshInterval, tokenLifetime)
	}
	if tokenRefreshInterval <= tokenMintFloor {
		t.Errorf("The refresh interval (%v) must exceed Apple's mint floor (%v), or two "+
			"consecutive tokens arrive too close together and are refused with "+
			"TooManyProviderTokenUpdates.", tokenRefreshInterval, tokenMintFloor)
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

	firstToken, err := processor.getProviderToken(first, now)
	if err != nil {
		t.Fatalf("Could not mint a token: %v", err)
	}
	secondToken, err := processor.getProviderToken(second, now.Add(time.Minute))
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
	otherToken, err := processor.getProviderToken(tokenAuthPSP(t, otherPath), now.Add(2*time.Minute))
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

	first, err := processor.getProviderToken(tokenAuthPSP(t, path), now)
	if err != nil {
		t.Fatalf("Could not mint a token: %v", err)
	}
	for name, alias := range map[string]string{"a copy": copied, "a symlink": linked} {
		token, err := processor.getProviderToken(tokenAuthPSP(t, alias), now.Add(time.Minute))
		if err != nil {
			t.Fatalf("Could not get a token through %s: %v", name, err)
		}
		if token != first {
			t.Errorf("%s of the signing key got its own token; Apple's mint limit is per key, "+
				"so both would count against the same 20-minute floor", name)
		}
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

		if _, err := processor.getProviderToken(tokenAuthPSP(t, path), now); err == nil {
			t.Error("Expected a P-384 key to be rejected")
		} else if !strings.Contains(err.Error(), "P-256") {
			t.Errorf("Expected the error to name the curve APNs requires, got: %v", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		psp := tokenAuthPSP(t, filepath.Join(t.TempDir(), "absent.p8"))
		_, err := processor.getProviderToken(psp, now)
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
		_, err := processor.getProviderToken(tokenAuthPSP(t, path), now)
		if err == nil {
			t.Fatal("Expected an error for an RSA key")
		}
		if !strings.Contains(err.Error(), "ECDSA") {
			t.Errorf("Expected the error to explain an ECDSA key is needed, got: %v", err)
		}
	})
}
