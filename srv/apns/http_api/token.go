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

package http_api //nolint:revive

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// Apple's two bounds on a provider token, and the window uniqush refreshes in.
//
// These are not a tuning choice. Apple rejects a token whose iat is more than
// an hour old with 403 ExpiredProviderToken, and rejects *minting* more than
// one token per 20 minutes for the same key with 429
// TooManyProviderTokenUpdates. A refresh interval therefore has to sit strictly
// between the two, and both edges are unforgiving: too slow and every push
// fails, too eager and every push fails differently.
//
// 45 minutes is comfortably inside both, and the relationship is asserted in
// TestProviderTokenRefreshIntervalRespectsApplesBounds rather than left to a
// comment.
const (
	tokenLifetime  = time.Hour
	tokenMintFloor = 20 * time.Minute

	tokenRefreshInterval = 45 * time.Minute
)

// TokenRefreshInterval is how often uniqush signs a new APNs provider token,
// and therefore the width of the buckets every instance agrees on.
//
// Exported because it is part of the observable schedule -- it is what decides
// how many distinct tokens Apple sees per hour -- and because tests outside this
// package need to compute bucket boundaries without hardcoding a number that has
// already changed twice.
const TokenRefreshInterval = tokenRefreshInterval

// providerToken caches one signed JWT for one signing key.
//
// A token authenticates the *team*, not an app: it is valid for every topic the
// key covers, so one of these is shared by every provider using the same key,
// however many services they serve.
type providerToken struct {
	key    *ecdsa.PrivateKey
	keyID  string
	teamID string

	mutex sync.Mutex

	// The cached JWT and the bucket it was signed for. One bucket is all that
	// is ever wanted here: a token is either the current one or it is not.
	signed       string
	signedBucket time.Time
}

// issuedAtBucket quantises a time to the refresh interval.
//
// Quantising rather than measuring an age is what removes every clock problem
// from this file. The bucket is a pure function of now, so a clock that moves
// -- in either direction, by any amount -- simply lands in a different bucket
// and signs the token for it.
//
// An age-based cache cannot say that. It has to carry two clocks: a wall-clock
// age, because that is what Apple compares the iat against, and a monotonic
// elapsed time, so that a backward correction cannot make the age negative and
// pin a stale token for hours. It also has to tell a resume from suspend apart
// from an NTP step, which needs a suspend-inclusive clock the standard library
// does not expose. None of that machinery is needed here, because no duration
// is being measured at all.
//
// Boundaries are aligned to the Unix epoch, not to the wall clock, so they do
// not fall on round times: with a 35-minute bucket they land at :00, :35, :10
// past successive hours. Tests that need a boundary must round one down rather
// than assume a tidy-looking instant is one.
//
// The residual limit is the same one no amount of local care can fix: while the
// host's clock is materially wrong, every token uniqush mints carries an iat
// Apple judges against the correct time, so token auth stays broken until the
// clock is corrected.
//
// What it does not fix is the gap between minting a token and Apple seeing it.
// The floor runs from when Apple *observes* a token, not from its iat, so a
// bucket whose first push lands late can be followed by a boundary inside the
// floor. That is a real exposure and it is not addressed here.
func issuedAtBucket(now time.Time) time.Time {
	interval := int64(tokenRefreshInterval / time.Second)
	return time.Unix(now.UTC().Unix()/interval*interval, 0).UTC()
}

// token returns a currently valid JWT, signing one if the bucket has changed.
//
// now is a parameter rather than a call to time.Now so the refresh window is
// testable without waiting three quarters of an hour.
func (t *providerToken) token(now time.Time) (string, error) {
	bucket := issuedAtBucket(now)

	t.mutex.Lock()
	defer t.mutex.Unlock()

	// Bucket equality rather than an elapsed-time comparison. Both sides come
	// from issuedAtBucket, which builds its result with time.Unix and so never
	// carries a monotonic reading, making this wall-clock arithmetic by
	// construction -- on exactly the value written into the claim.
	if t.signed != "" && t.signedBucket.Equal(bucket) {
		return t.signed, nil
	}

	claims := jwt.MapClaims{
		"iss": t.teamID,
		"iat": bucket.Unix(),
	}
	unsigned := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	// The key id goes in the header, not the claims. Apple uses it to pick
	// which of the team's public keys to verify with, so a wrong one fails as
	// InvalidProviderToken rather than as a signature mismatch.
	unsigned.Header["kid"] = t.keyID

	signed, err := unsigned.SignedString(t.key)
	if err != nil {
		return "", fmt.Errorf("could not sign the APNs provider token: %v", err)
	}

	t.signed = signed
	t.signedBucket = bucket
	return signed, nil
}

// tokenCacheKey identifies the signing key a provider authenticates with.
//
// Keyed on the credential rather than on the provider, because a token is valid
// for the whole team: two services sharing a key should share a token, and must
// share the mint interval, since Apple's 20-minute floor is per key and not per
// caller. Two providers refreshing independently off the same key would trip it
// exactly when both are busy.
//
// Keyed on a fingerprint of the key itself rather than on the path it was read
// from, because a path is not an identity. The same .p8 reached by an absolute
// and a relative path, through a symlink, or copied into two directories, is one
// key as far as Apple's limit is concerned. Keying on the pathname would give
// those separate entries and separate mint schedules -- precisely the failure
// this cache exists to prevent, arrived at by a different route. The public half
// identifies the key and is not a secret.
func tokenCacheKey(key *ecdsa.PublicKey, keyID, teamID string) (string, error) {
	encoded, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", fmt.Errorf("could not fingerprint the APNs signing key: %v", err)
	}
	fingerprint := sha256.Sum256(encoded)
	return strings.Join([]string{hex.EncodeToString(fingerprint[:]), keyID, teamID}, "\x00"), nil
}

// currentTime reads the clock, which tests replace via SetClock.
func (prp *HTTPPushRequestProcessor) currentTime() time.Time {
	prp.tokensLock.RLock()
	now := prp.now
	prp.tokensLock.RUnlock()
	if now == nil {
		return time.Now()
	}
	return now()
}

// getProviderToken returns a signed provider token for this provider.
//
// The cache lives on the processor rather than on the provider because a
// PushServiceProvider is rebuilt from its serialized form on every request, so
// anything cached on it is discarded immediately -- which here would mean
// signing a fresh JWT for every push, and tripping Apple's mint limit as soon
// as pushes were more than three an hour.
//
// It is required for correctness, not an optimisation. jwt.SigningMethodES256
// draws a random nonce, so two signatures over identical claims are different
// tokens as far as Apple is concerned -- and Apple counts tokens, not requests.
// Removing this cache, or letting it miss, means minting on every push and
// being refused with TooManyProviderTokenUpdates as soon as pushes exceed three
// an hour. The same reasoning is why a restart or a second instance still costs
// a mint: neither can reach the token the other is using.
//
// The key is read on every call rather than cached by path, which buys two
// things: the cache can be keyed on the key's own fingerprint rather than on a
// pathname, and rotating a key in place takes effect without a restart. The
// cost is one file read and one PKCS#8 parse per call, so callers on the push
// path resolve this once per batch rather than per device.
func (prp *HTTPPushRequestProcessor) getProviderToken(psp *push.PushServiceProvider, now time.Time) (string, error) {
	cached, err := prp.providerTokenFor(psp)
	if err != nil {
		return "", err
	}
	return cached.token(now)
}

// providerTokenFor returns the cache entry for this provider's signing key.
func (prp *HTTPPushRequestProcessor) providerTokenFor(psp *push.PushServiceProvider) (*providerToken, error) {
	key, err := common.LoadAuthKey(psp.VolatileData[common.AuthKeyKey])
	if err != nil {
		return nil, err
	}
	keyID := psp.VolatileData[common.KeyIDKey]
	teamID := psp.VolatileData[common.TeamIDKey]

	name, err := tokenCacheKey(&key.PublicKey, keyID, teamID)
	if err != nil {
		return nil, err
	}

	prp.tokensLock.RLock()
	cached, ok := prp.tokens[name]
	prp.tokensLock.RUnlock()

	if !ok {
		candidate := &providerToken{key: key, keyID: keyID, teamID: teamID}

		prp.tokensLock.Lock()
		if existing, raced := prp.tokens[name]; raced {
			// Another goroutine won. Prefer its entry, so there is only ever
			// one mint schedule per key.
			cached = existing
		} else {
			prp.tokens[name] = candidate
			cached = candidate
		}
		prp.tokensLock.Unlock()
	}

	return cached, nil
}

// authorizationHeader returns the value for the authorization header.
//
// Lowercase "bearer" matches Apple's own examples. The header name is
// case-insensitive but the scheme token is compared case-insensitively too, so
// this is presentation rather than protocol -- worth matching anyway, since the
// alternative is wondering about it during an outage.
func authorizationHeader(signed string) string {
	return "bearer " + signed
}
