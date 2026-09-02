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
	"github.com/uniqush/uniqush-push/srv/apns/es256"
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
// The bucket length is not a free choice either, and 45 minutes was wrong.
// Apple's floor runs from when it *observes* a new token, not from the token's
// iat, and the first push of a bucket can land anywhere inside it. A provider
// whose first push in a bucket happens near the end presents that token late,
// then presents the next bucket's token moments later at the boundary: two
// tokens seconds apart, well inside the floor. That is not a rare startup race
// -- it is any first use during the final 20 minutes of a bucket.
//
// Recovery is to fall back to the previous bucket's token, which deterministic
// signing lets any instance recompute. For that to always be available, the
// older token must still be alive when the floor clears:
//
//	worst-case first use    u <= bucketStart + bucket
//	the floor clears at     u + floor
//	the older token dies at bucketStart + lifetime
//	so we need              bucket + floor <= lifetime
//
// which bounds the bucket at lifetime - floor. It must also exceed the floor, so
// that two promptly-observed consecutive tokens are far enough apart.
//
// Sitting *on* that upper bound would be wrong, because both sides of it are
// measured on Apple's clock rather than ours. The floor starts when Apple
// observes a token and the expiry is judged against the iat we wrote, so any
// skew between the two clocks -- plus the time a request spends in flight --
// eats directly into the gap. With uniqush a minute behind, a fallback first
// observed at local +39:30 can reach the end of the floor at local +59:00 while
// Apple already considers it over an hour old; the recovery then returns
// ExpiredProviderToken and the push is dropped as a credential failure, which is
// the outage this whole scheme exists to prevent.
//
// So the interval is the bound less an explicit margin:
//
//	bucket + floor + skew margin <= lifetime
//	35m    + 20m   + 5m          == 60m
//
// 35 minutes still comfortably exceeds the floor at the other end. The margin is
// deliberately a named constant rather than folded into the arithmetic, so that
// anyone widening the bucket has to decide what happens to it.
const (
	tokenLifetime  = time.Hour
	tokenMintFloor = 20 * time.Minute

	// tokenSkewMargin is the allowance for uniqush's clock disagreeing with
	// Apple's, and for a request being in flight, on the fallback path.
	tokenSkewMargin = 5 * time.Minute

	tokenRefreshInterval = tokenLifetime - tokenMintFloor - tokenSkewMargin
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

	// signed holds the JWT for each live bucket, keyed on the bucket's Unix
	// second. At most two are ever wanted: the current bucket, and the one
	// before it that the fallback presents.
	//
	// A single slot was not enough, and the way it failed was quiet. Every batch
	// asks for the current token and then for the fallback, so a one-entry cache
	// stored the current token, immediately evicted it for the previous one, and
	// started the next batch with a miss -- two ECDSA signatures per batch
	// forever, plus contention on this mutex, while looking like a cache.
	//
	// The cost is not only CPU. srv/apns/es256 is explicit that its signer is not
	// constant-time and rests on being called about once per bucket; signing on
	// every batch instead is exactly the usage pattern that assumption rules out.
	signed map[int64]string

	// The bucket Apple refused with TooManyProviderTokenUpdates, and when.
	//
	// Without this, the refusal is rediscovered on every push for the rest of
	// the floor: each batch offers the new token, spends a round trip to Apple
	// learning it is still too early, and falls back. One 429 is the
	// unavoidable cost of finding out; twenty minutes of them is a choice.
	// Remembering the refusal turns it into a single probe.
	//
	// refusedBucket is the bucket of the token Apple actually rejected, taken
	// from the request that was refused rather than from the clock at the time
	// the answer arrived. Those differ whenever a request is signed just before
	// a boundary and its 429 comes back just after one, and reading the clock
	// there would mark the wrong -- untried -- bucket as refused.
	refusedBucket time.Time
	refusedAt     time.Time

	// confirmedBucket is a bucket whose token Apple has accepted from this
	// process. Until a bucket is confirmed, a batch cannot know whether its
	// token will be taken, and sending the whole batch at once would turn one
	// unavoidable refusal into one per device. See probeBeforeReleasingBatch.
	confirmedBucket time.Time

	// probes holds a channel per bucket currently being probed, closed when the
	// answer is known.
	//
	// Without this the memo bounds nothing across batches. AddRequest starts
	// each batch in its own goroutine, so several can look at an unconfirmed
	// bucket before any of them has a response to record -- each then sends its
	// own probe, and a boundary that should cost one refusal costs one per
	// concurrent batch. The memo only ever suppressed the *second* probe a
	// batch would make, never the first one every other batch makes.
	//
	// So the first batch to arrive at an unconfirmed bucket probes, and the
	// rest wait for its answer and then re-read the token rather than asking
	// Apple the same question again.
	probes map[int64]*probeState
}

// probeState is one bucket's probe: the channel waiters block on, and when it
// finished.
//
// Finished probes are remembered rather than forgotten, for as long as the
// refusal memo lasts. Deleting the record as soon as the prober was done left a
// gap that reopened the stampede: a batch that had already resolved its bucket
// before the refusal landed would find no probe in flight, claim a fresh one,
// and ask Apple the same question a second time. Keeping the record means that
// batch is told the answer already exists and simply re-reads it.
//
// The retention matches the memo deliberately. While the memo holds, the token
// for this bucket is not being sent at all, so there is nothing to probe; once
// it lapses the bucket is live again and genuinely does need one more.
type probeState struct {
	done       chan struct{}
	finished   bool
	finishedAt time.Time
}

// probeWaitLimit bounds how long a batch waits for another batch's probe.
//
// A bound rather than an open wait, because the prober is one HTTP request to
// Apple and anything can happen to it. The client's own timeout is 20 seconds,
// so this only expires when that has already gone wrong -- and when it does,
// the waiter proceeds with whatever the token cache currently says rather than
// holding the push indefinitely.
const probeWaitLimit = 30 * time.Second

// claimProbe asks for the right to probe this bucket.
//
// Returns true when the caller is the prober, in which case it must call
// finishProbe once the answer is known -- deferred, so a panic on the push path
// does not strand every waiter until the limit.
//
// Returns false when the question is already being asked, or has been asked
// recently, along with a channel that is closed once the answer exists. Waiters
// must re-read the token afterwards: the whole point of waiting is that the
// answer may have changed which bucket they should send.
func (t *providerToken) claimProbe(bucket time.Time, now time.Time) (bool, <-chan struct{}) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if t.probes == nil {
		t.probes = make(map[int64]*probeState, 1)
	}
	// Drop records whose retention has lapsed, so this map does not accumulate
	// one entry per bucket for the life of the process.
	for at, state := range t.probes {
		if state.finished && now.Sub(state.finishedAt) >= tokenMintFloor {
			delete(t.probes, at)
		}
	}

	if existing, known := t.probes[bucket.Unix()]; known {
		return false, existing.done
	}
	state := &probeState{done: make(chan struct{})}
	t.probes[bucket.Unix()] = state
	return true, state.done
}

// finishProbe publishes the answer to everyone waiting on this bucket.
//
// Idempotent: the prober defers it and also calls it as soon as the answer is
// in, so that waiters are released before the rest of the batch goes out rather
// than after.
func (t *providerToken) finishProbe(bucket time.Time, now time.Time) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	state, known := t.probes[bucket.Unix()]
	if !known || state.finished {
		return
	}
	state.finished = true
	state.finishedAt = now
	close(state.done)
}

// noteAccepted records that Apple took the token for a bucket.
func (t *providerToken) noteAccepted(bucket time.Time) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.confirmedBucket = bucket
}

// isConfirmed reports whether Apple has already accepted this bucket's token.
func (t *providerToken) isConfirmed(bucket time.Time) bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return !t.confirmedBucket.IsZero() && t.confirmedBucket.Equal(bucket)
}

// deterministicES256 signs JWTs with RFC 6979 instead of a random nonce.
//
// jwt.SigningMethodES256 calls ecdsa.Sign with crypto/rand, so two processes
// signing identical claims produce different tokens. That is exactly what must
// not happen here; see srv/apns/es256 for why. Verify is left to the standard
// method, since a deterministic signature is an ordinary ECDSA signature and
// verifies the same way -- only production differs.
type deterministicES256 struct{}

var _ jwt.SigningMethod = deterministicES256{}

func (deterministicES256) Alg() string { return "ES256" }

// signES256 produces the signature. A package variable so that a test can count
// how many are produced.
//
// The count is the point. srv/apns/es256 justifies a signer that is not
// constant time on the grounds that uniqush signs about once per bucket, and
// says so in its package comment. Nothing else can check that: because signing
// is deterministic, re-signing on every call produces byte-identical output, so
// every assertion about tokens passes whether the cache works or not. See
// TestProviderTokenSignsOncePerBucket.
var signES256 = es256.Sign

func (deterministicES256) Sign(signingString string, key interface{}) ([]byte, error) {
	private, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, jwt.ErrInvalidKeyType
	}
	return signES256(private, []byte(signingString))
}

func (deterministicES256) Verify(signingString string, sig []byte, key interface{}) error {
	return jwt.SigningMethodES256.Verify(signingString, sig, key)
}

// issuedAtBucket quantises a time to the refresh interval.
//
// This is what makes two processes agree. Every instance rounds the clock down
// to the same boundary, so they build identical claims; combined with
// deterministic signing, they produce byte-identical tokens without exchanging
// anything. A restart lands in the same bucket and recomputes the same token,
// so it costs no mint at all.
//
// It also makes clock skew harmless *once the fleet is running* -- see the cold
// start caveat below. An instance running a few seconds behind
// simply stays in the previous bucket a little longer and keeps using the token
// the others are already using; when it crosses, it computes the one they have
// already computed. Skew delays adoption, it does not create a second token.
//
// The exception is a cold start. Reusing the previous bucket's token is only
// harmless because Apple has already seen it; before any instance has pushed,
// nothing has been seen, so two instances whose clocks straddle a boundary
// compute two unfamiliar tokens and Apple refuses the second. Neither has a
// usable predecessor. It is narrow, it clears when the floor passes, and it
// surfaces as a retryable error -- but bucketing does not remove it.
//
// Boundaries are aligned to the Unix epoch, not to the wall clock, so they do
// not fall on round times: with a 35-minute bucket they land at :00, :35, :10
// past successive hours. Tests that need a boundary must round one down rather
// than assume a tidy-looking instant is one.
//
// Bucketing also disposes of the clock-correction handling the previous release
// needed, rather than merely relocating it. That version compared the cached
// token's *age* against the interval, which meant carrying two clocks: a
// wall-clock age to match Apple's view of the iat, and a monotonic elapsed time
// so that a backward correction could not make the age negative and pin a stale
// token in the cache for hours.
//
// There is no age here. The bucket is a pure function of now, so a clock that
// moves -- in either direction, by any amount -- simply lands in a different
// bucket and produces the token for it. Nothing is cached against a duration,
// so nothing can be held past its usefulness by arithmetic going backwards.
//
// That also retires the suspend-inclusive clock the previous release needed.
// Distinguishing a resume from an NTP step mattered only because a *duration*
// was being measured; with no duration there is nothing for CLOCK_BOOTTIME to
// disambiguate, so srv/apns/http_api/bootclock_*.go goes with it rather than
// staying as machinery nothing calls.
//
// The residual limit is the same one no amount of local care can fix: while the
// host's clock is materially wrong, every token uniqush mints carries an iat
// Apple judges against the correct time, so token auth stays broken until the
// clock is corrected.
//
// What it does not fix is the gap between minting a token and Apple seeing it.
// The floor is measured from observation, so a bucket whose first push lands in
// its final 20 minutes will present the next bucket's token shortly after, and
// Apple will refuse it. That is handled by falling back to the previous
// bucket's token rather than by the bucketing itself; see previousToken.
func issuedAtBucket(now time.Time) time.Time {
	interval := int64(tokenRefreshInterval / time.Second)
	return time.Unix(now.UTC().Unix()/interval*interval, 0).UTC()
}

// token returns a currently valid JWT, signing one if the bucket has changed.
//
// now is a parameter rather than a call to time.Now so the refresh window is
// testable without waiting three quarters of an hour.
//
// The cache is now only an optimisation: because the token is a pure function
// of the key and the bucket, recomputing it would give the same bytes. It saves
// a signature per push, not correctness.
// It also reports which bucket the returned token belongs to. The caller needs
// that to record a refusal against the bucket Apple actually rejected, and it is
// not always issuedAtBucket(now) -- when the memo below is in force the token
// returned is the previous bucket's.
func (t *providerToken) token(now time.Time) (signed string, bucket time.Time, err error) {
	bucket = issuedAtBucket(now)

	// If Apple has already refused this bucket's token, keep presenting the one
	// it accepted until its floor has passed. Offering the refused token again
	// would only buy another 429 and another fallback.
	t.mutex.Lock()
	refused := t.refusedBucket.Equal(bucket) && now.Sub(t.refusedAt) < tokenMintFloor
	t.mutex.Unlock()

	if refused {
		if previous, previousBucket, previousErr := t.previousToken(now); previousErr == nil && previous != "" {
			return previous, previousBucket, nil
		}
	}

	signed, err = t.tokenForBucket(bucket)
	return signed, bucket, err
}

// noteRefused records that Apple rejected the token for a specific bucket.
//
// The bucket comes from the request that was refused, not from the clock now.
// A request signed just before a boundary can have its 429 arrive just after
// one, and reading the clock here would mark the *new* bucket as refused -- a
// bucket Apple has never been shown. Every push for the rest of the memo window
// would then skip straight to the previous bucket, which is the token that was
// actually rejected, so both the primary and the fallback would be known-bad.
//
// observedAt is still the response time, because that is when the floor that
// this refusal implies started running.
func (t *providerToken) noteRefused(bucket, observedAt time.Time) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.refusedBucket = bucket
	t.refusedAt = observedAt
}

// previousToken returns the token for the bucket before this one, or "" if there
// is no usable one.
//
// This is the recovery path for TooManyProviderTokenUpdates. Apple refusing the
// current bucket's token means it saw a different one too recently; the
// previous bucket's token is the one it saw, it is still valid, and because
// signing is deterministic this process can reproduce it even if a different
// instance was the one that presented it. The bucket length is chosen so this
// is always still alive when the floor clears -- see the constants above.
//
// Returns "" if the previous bucket is itself the one Apple just refused.
// Offering a token already known to be rejected is worse than offering none: the
// push fails either way, but the retry costs a second round trip and teaches
// Apple nothing.
func (t *providerToken) previousToken(now time.Time) (string, time.Time, error) {
	previous := issuedAtBucket(now).Add(-tokenRefreshInterval)
	if now.Sub(previous) >= tokenLifetime {
		return "", time.Time{}, nil
	}

	t.mutex.Lock()
	knownBad := t.refusedBucket.Equal(previous) && now.Sub(t.refusedAt) < tokenMintFloor
	t.mutex.Unlock()
	if knownBad {
		return "", time.Time{}, nil
	}

	signed, err := t.tokenForBucket(previous)
	return signed, previous, err
}

func (t *providerToken) tokenForBucket(issuedAt time.Time) (string, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	// Bucket equality rather than an elapsed-time comparison, which also
	// disposes of the monotonic-clock hazard the age-based version had.
	//
	// time.Time.Sub prefers the monotonic readings time.Now attaches, and a
	// monotonic clock counts evenly through the two events that matter here: a
	// forward step from NTP, and a resume from suspend. Apple has neither
	// reading -- it ages a token by comparing the wall-clock second in iat
	// against its own wall clock -- so measuring the age monotonically meant a
	// host that jumped forward kept serving a token Apple already considered
	// expired.
	//
	// Both sides here come from issuedAtBucket, which builds its result with
	// time.Unix and so never carries a monotonic reading. The comparison is
	// therefore wall-clock by construction, on exactly the value written into
	// the claim, and there is no elapsed time being measured at all.
	if cached, ok := t.signed[issuedAt.Unix()]; ok {
		return cached, nil
	}

	claims := jwt.MapClaims{
		"iss": t.teamID,
		"iat": issuedAt.Unix(),
	}
	unsigned := jwt.NewWithClaims(deterministicES256{}, claims)
	// The key id goes in the header, not the claims. Apple uses it to pick
	// which of the team's public keys to verify with, so a wrong one fails as
	// InvalidProviderToken rather than as a signature mismatch.
	//
	// Both the header and the claims are Go maps, and encoding/json sorts map
	// keys, so the bytes being signed are themselves deterministic. That is
	// load-bearing, not incidental.
	unsigned.Header["kid"] = t.keyID

	signed, err := unsigned.SignedString(t.key)
	if err != nil {
		return "", fmt.Errorf("could not sign the APNs provider token: %v", err)
	}

	if t.signed == nil {
		t.signed = make(map[int64]string, 2)
	}
	t.signed[issuedAt.Unix()] = signed
	t.pruneExpired(issuedAt)
	return signed, nil
}

// pruneExpired drops buckets that can no longer be presented, so the cache stays
// at the two entries it actually needs rather than growing for the life of the
// process.
//
// Anything older than the lifetime is unusable by definition: Apple would answer
// ExpiredProviderToken. Keying on the bucket makes this a straight comparison
// rather than an eviction policy.
//
// Callers must hold the mutex.
func (t *providerToken) pruneExpired(newest time.Time) {
	oldestUsable := newest.Add(-tokenLifetime).Unix()
	for bucket := range t.signed {
		if bucket < oldestUsable {
			delete(t.signed, bucket)
		}
	}
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
// The cache is now only an optimisation. Because the token is a pure function
// of the signing key and the bucket, a process that has just started -- or a
// second instance that shares the key -- recomputes exactly the same bytes, so
// nothing is lost by an empty cache beyond one signature. That is the whole
// point of signing deterministically; see srv/apns/es256.
//
// The key is read on every call rather than cached by path, which buys two
// things: the cache can be keyed on the key's own fingerprint rather than on a
// pathname, and rotating a key in place takes effect without a restart.
//
// The cost is one file read and one PKCS#8 parse per call, so callers on the
// push path resolve the entry once per batch and pass it down rather than
// calling back here per device. That is not a style preference -- doing it per
// device is a read, a PEM decode, a parse and a point validation for every
// device in the batch, and TestTheSigningKeyIsReadOncePerBatch counts it,
// because nothing else about a push changes when it goes wrong.
func (prp *HTTPPushRequestProcessor) getProviderToken(psp *push.PushServiceProvider, now time.Time) (string, time.Time, error) {
	cached, err := prp.providerTokenFor(psp)
	if err != nil {
		return "", time.Time{}, err
	}
	return cached.token(now)
}

// previousProviderToken returns the token for the bucket before the current
// one, or "" once that token has expired. See providerToken.previousToken.
// The bucket is deliberately not returned. Only the primary token's bucket is
// ever recorded against a refusal, because a fallback that is itself refused
// leaves nothing better to fall back to -- previousToken already declines to
// offer a bucket known to be bad, so there is nothing further to remember.
func (prp *HTTPPushRequestProcessor) previousProviderToken(psp *push.PushServiceProvider, now time.Time) (string, error) {
	cached, err := prp.providerTokenFor(psp)
	if err != nil {
		return "", err
	}
	signed, _, err := cached.previousToken(now)
	return signed, err
}

// noteProviderTokenRefused records that Apple answered this provider's current
// token with TooManyProviderTokenUpdates. Best effort: if the key can no longer
// be read there is nothing to remember it against, and the push has already
// been dealt with by the caller.
func (prp *HTTPPushRequestProcessor) noteProviderTokenRefused(psp *push.PushServiceProvider, bucket, observedAt time.Time) {
	if cached, err := prp.providerTokenFor(psp); err == nil {
		cached.noteRefused(bucket, observedAt)
	}
}

// loadAuthKey reads and parses a signing key.
//
// A package variable so that a test can count the reads. The count is the point
// of the seam: "the key is read once per batch, not once per device" is a claim
// about work that leaves no other trace, and it was wrong once already -- every
// response used to resolve this entry again, so a hundred-device batch read and
// parsed the .p8 a hundred and five times. Nothing failed, because nothing was
// counting. See TestTheSigningKeyIsReadOncePerBatch.
var loadAuthKey = common.LoadAuthKey

// providerTokenFor returns the cache entry for this provider's signing key.
func (prp *HTTPPushRequestProcessor) providerTokenFor(psp *push.PushServiceProvider) (*providerToken, error) {
	key, err := loadAuthKey(psp.VolatileData[common.AuthKeyKey])
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
