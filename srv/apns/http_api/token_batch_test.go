package http_api //nolint:revive

import (
	"bytes"
	"crypto/ecdsa"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// Tests for what a batch does with its provider token: how often it resolves
// the signing key, and which bucket it credits when Apple refuses one.
//
// Both are invariants about work rather than about output. A push that reads
// the .p8 once and a push that reads it a hundred times send byte-identical
// requests, and crediting the wrong bucket still leaves the notification
// delivered. Neither shows up in an assertion about results, which is why both
// went wrong unnoticed and why these count instead.

// tokenBatchPSP builds a token-auth provider that Name() works on.
//
// Through the manager rather than by hand, because PushPeer.Name() dereferences
// the push service type and sendRequests reaches it via clientCacheKey.
func tokenBatchPSP(t *testing.T, authKeyPath string) *push.PushServiceProvider {
	t.Helper()

	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"service":         "tokenbatch",
		"pushservicetype": "apns",
		"cert":            "../apns-test/localhost.cert",
		"key":             "../apns-test/localhost.key",
		"addr":            "gateway.push.apple.com:2195",
		"skipverify":      "true",
		"bundleid":        "com.example.tokenbatch",
	})
	if err != nil {
		t.Fatalf("Could not build a provider: %v", err)
	}
	// Added after the build so the provider authenticates with the key rather
	// than the certificate. createTLSConfig then skips the keypair entirely.
	psp.VolatileData[common.AuthKeyKey] = authKeyPath
	psp.VolatileData[common.KeyIDKey] = testKeyID
	psp.VolatileData[common.TeamIDKey] = testTeamID
	return psp
}

// countAuthKeyReads swaps in a counting loadAuthKey for the duration of a test.
func countAuthKeyReads(t *testing.T) *atomic.Int64 {
	t.Helper()

	var reads atomic.Int64
	previous := loadAuthKey
	loadAuthKey = func(path string) (*ecdsa.PrivateKey, error) {
		reads.Add(1)
		return previous(path)
	}
	t.Cleanup(func() { loadAuthKey = previous })
	return &reads
}

// runBatch drives sendRequests to completion and returns the authorization
// header of every request the client saw, in order.
func runBatch(t *testing.T, processor *HTTPPushRequestProcessor, psp *push.PushServiceProvider,
	devices int, respond func(auth string) *http.Response) []string {
	t.Helper()

	var mutex struct {
		seen []string
	}
	var lock = make(chan struct{}, 1)
	lock <- struct{}{}

	mockAPNSRequest(processor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		auth := ""
		if values := r.Header["authorization"]; len(values) > 0 {
			auth = values[0]
		}
		<-lock
		mutex.seen = append(mutex.seen, auth)
		lock <- struct{}{}
		return respond(auth), nil, nil
	})

	tokens := make([][]byte, devices)
	for i := range tokens {
		tokens[i] = []byte{byte(i), 0x01}
	}

	errChan := make(chan push.Error, devices*2)
	request := &common.PushRequest{
		PSP:       psp,
		Devtokens: tokens,
		Payload:   []byte(`{"aps":{"alert":"batch"}}`),
		ErrChan:   errChan,
		ResChan:   make(chan *common.APNSResult, devices*2),
	}
	go processor.sendRequests(request)
	for range errChan { //nolint:revive // drained so sendRequests can finish
	}

	<-lock
	seen := mutex.seen
	lock <- struct{}{}
	return seen
}

func okResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil))}
}

func refusalResponse() *http.Response {
	body := []byte(`{"reason":"` + reasonTooManyProviderTokenUpdates + `"}`)
	return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(bytes.NewReader(body))}
}

// TestTheSigningKeyIsReadOncePerBatch guards a cost that scales with the batch.
//
// Resolving a provider's token entry reads the .p8 off disk, PEM-decodes it,
// parses PKCS#8 and validates the point -- and it used to happen on every
// response, because recording the outcome went through a helper that looked the
// entry up again by provider. A hundred-device batch did it a hundred and five
// times, on the push path, for something that cannot change within a batch.
//
// Invisible from anywhere else: the pushes all succeeded, the tokens were
// identical, and the only symptom was the work. token.go promises "one small
// file read per push batch -- not per device", so this is that promise, counted.
func TestTheSigningKeyIsReadOncePerBatch(t *testing.T) {
	path, _ := writeSigningKey(t)
	psp := tokenBatchPSP(t, path)
	processor := newTokenProcessor()
	reads := countAuthKeyReads(t)

	const devices = 50
	if seen := runBatch(t, processor, psp, devices, func(string) *http.Response { return okResponse() }); len(seen) != devices {
		t.Fatalf("Expected %d requests, got %d", devices, len(seen))
	}

	// One for the batch. Not "at most devices": the point is that the count does
	// not scale, and a bound that permits scaling would have passed before.
	if got := reads.Load(); got != 1 {
		t.Errorf("A %d-device batch read the signing key %d times, expected 1.\n"+
			"Reading it per device puts an os.ReadFile, a PEM decode, a PKCS#8 parse and a "+
			"P-256 validation on the push path for every device in the batch. Resolve the "+
			"providerToken once in sendRequests and hand it to sendRequest.", devices, got)
	}
}

// TestFallbackCreditsTheBucketAppleAccepted guards the probe against being
// disarmed by its own recovery.
//
// When Apple refuses a bucket with TooManyProviderTokenUpdates the push is
// retried with the previous bucket's token, and it succeeds. The response that
// comes back is then an observation about the *previous* bucket -- but it used
// to be recorded against the one that had just been refused, because the retry
// overwrote the response and the bucket variable was never updated with it.
//
// The push still succeeded, so nothing failed. What broke was later: once the
// refusal memo lapsed inside that same bucket, the current bucket looked already
// confirmed, so the next batch skipped its probe and went out in full against a
// token Apple had never accepted -- turning one refusal into one per device,
// which is the exact outcome the probe exists to prevent.
func TestFallbackCreditsTheBucketAppleAccepted(t *testing.T) {
	path, _ := writeSigningKey(t)
	psp := tokenBatchPSP(t, path)
	processor := newTokenProcessor()

	// Five minutes into a bucket, so both it and its predecessor are live.
	now := issuedAtBucket(time.Now().UTC()).Add(5 * time.Minute)
	processor.SetClock(func() time.Time { return now })

	entry, err := processor.providerTokenFor(psp)
	if err != nil {
		t.Fatalf("Could not resolve the signing key: %v", err)
	}
	current, currentBucket, err := entry.token(now)
	if err != nil {
		t.Fatalf("Could not mint the current token: %v", err)
	}
	previous, previousBucket, err := entry.previousToken(now)
	if err != nil || previous == "" {
		t.Fatalf("Expected a previous-bucket token to fall back to, got %q (%v)", previous, err)
	}

	// Refuse the current bucket's token and accept anything else, which is what
	// Apple does inside the mint floor.
	seen := runBatch(t, processor, psp, 1, func(auth string) *http.Response {
		if auth == authorizationHeader(current) {
			return refusalResponse()
		}
		return okResponse()
	})

	if len(seen) != 2 {
		t.Fatalf("Expected a refusal and one fallback retry, got %d request(s): %v", len(seen), seen)
	}
	if seen[1] != authorizationHeader(previous) {
		t.Fatalf("The retry did not present the previous bucket's token")
	}

	if !entry.isConfirmed(previousBucket) {
		t.Errorf("Apple accepted the previous bucket's token, but that bucket is not confirmed.\n" +
			"The confirmation has to follow the token that was actually accepted, or the next " +
			"batch in this bucket cannot tell that it still needs to probe.")
	}
	if entry.isConfirmed(currentBucket) {
		t.Errorf("The bucket Apple refused is recorded as confirmed.\n" +
			"Once the refusal memo lapses, isConfirmed will answer yes for a token Apple has " +
			"never taken, the next batch will skip its probe, and every device in it will be " +
			"refused instead of one.")
	}
}
