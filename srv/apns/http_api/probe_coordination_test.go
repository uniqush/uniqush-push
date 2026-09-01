package http_api //nolint:revive

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// Tests for what a boundary costs when more than one batch hits it.
//
// The probe exists so that a bucket Apple is going to refuse costs one refusal
// rather than one per device. Both of these are about that bound holding when
// the code is used the way it is actually used: several batches at once, and a
// memo that has already moved the batch onto the previous bucket.

// TestConcurrentBatchesShareOneProbe holds the probe's bound across batches.
//
// AddRequest starts every batch in its own goroutine, so several can read the
// same unconfirmed bucket before any of them has an answer to record. Checking
// isConfirmed alone does not stop that: each batch sees "not confirmed", each
// sends its own probe, and a boundary that should cost one refusal costs one
// per concurrent batch. The memo only ever suppressed the *second* probe a
// batch would make -- never the first one every other batch makes.
func TestConcurrentBatchesShareOneProbe(t *testing.T) {
	path, _ := writeSigningKey(t)
	psp := tokenBatchPSP(t, path)
	processor := newTokenProcessor()

	now := issuedAtBucket(time.Now().UTC()).Add(5 * time.Minute)
	processor.SetClock(func() time.Time { return now })

	entry, err := processor.providerTokenFor(psp)
	if err != nil {
		t.Fatalf("Could not resolve the signing key: %v", err)
	}
	current, _, err := entry.token(now)
	if err != nil {
		t.Fatalf("Could not mint the current token: %v", err)
	}

	// Apple refuses this bucket and accepts anything else, which is what the
	// mint floor looks like from here.
	var refusals atomic.Int64
	currentHeader := authorizationHeader(current)
	processor.clientFactory = func(*http.Transport) HTTPClient {
		return &probeClient{respond: func(auth string) *http.Response {
			if auth == currentHeader {
				refusals.Add(1)
				return refusalResponse()
			}
			return okResponse()
		}}
	}

	const batches = 8
	const devicesPerBatch = 4

	var wg sync.WaitGroup
	for b := 0; b < batches; b++ {
		wg.Add(1)
		go func(b int) {
			defer wg.Done()
			tokens := make([][]byte, devicesPerBatch)
			for i := range tokens {
				tokens[i] = []byte{byte(b), byte(i)}
			}
			errChan := make(chan push.Error, devicesPerBatch*2)
			request := &common.PushRequest{
				PSP:       psp,
				Devtokens: tokens,
				Payload:   []byte(`{"aps":{"alert":"probe"}}`),
				ErrChan:   errChan,
				ResChan:   make(chan *common.APNSResult, devicesPerBatch*2),
			}
			go processor.sendRequests(request)
			for range errChan { //nolint:revive // drained so sendRequests can finish
			}
		}(b)
	}
	wg.Wait()

	// One. Not one per batch, and emphatically not one per device: without the
	// coordination this is `batches`, and without the probe at all it is
	// batches*devicesPerBatch.
	if got := refusals.Load(); got != 1 {
		t.Errorf("%d concurrent batches cost %d refusals at the boundary, expected 1.\n"+
			"Each batch checked isConfirmed and found the bucket unconfirmed, because none of "+
			"them had an answer yet -- so each asked Apple the same question. One batch should "+
			"probe while the rest wait for its result.", batches, got)
	}
}

// TestAFallbackIsNeverTheTokenBeingSent covers the degenerate retry.
//
// Once the memo is in force, token() returns the previous bucket -- and
// previousToken() returns that same bucket. Arming it as the fallback means a
// 429 is retried with byte-identical bytes: the same request, refused for the
// same reason, once per device. The retry cannot teach Apple anything it did
// not just say.
func TestAFallbackIsNeverTheTokenBeingSent(t *testing.T) {
	path, _ := writeSigningKey(t)
	psp := tokenBatchPSP(t, path)
	processor := newTokenProcessor()

	now := issuedAtBucket(time.Now().UTC()).Add(5 * time.Minute)
	processor.SetClock(func() time.Time { return now })

	entry, err := processor.providerTokenFor(psp)
	if err != nil {
		t.Fatalf("Could not resolve the signing key: %v", err)
	}
	// Apple has already refused the current bucket, so the memo steers every
	// push in this batch onto the previous one.
	current := issuedAtBucket(now)
	entry.noteRefused(current, now)

	// Everything is refused, which is the skewed-cold-start case: neither the
	// current bucket nor its predecessor is one Apple has accepted.
	var seen []string
	var lock sync.Mutex
	processor.clientFactory = func(*http.Transport) HTTPClient {
		return &probeClient{respond: func(auth string) *http.Response {
			lock.Lock()
			seen = append(seen, auth)
			lock.Unlock()
			return refusalResponse()
		}}
	}

	errChan := make(chan push.Error, 8)
	request := &common.PushRequest{
		PSP:       psp,
		Devtokens: [][]byte{{0x01}},
		Payload:   []byte(`{"aps":{}}`),
		ErrChan:   errChan,
		ResChan:   make(chan *common.APNSResult, 8),
	}
	go processor.sendRequests(request)
	for range errChan { //nolint:revive // drained so sendRequests can finish
	}

	lock.Lock()
	defer lock.Unlock()
	if len(seen) != 1 {
		t.Fatalf("Expected one request for one device, got %d: the refusal was retried with the "+
			"same token it was refused for.", len(seen))
	}
}

// probeClient answers according to the authorization header it is given.
type probeClient struct {
	respond func(auth string) *http.Response
}

func (c *probeClient) Do(request *http.Request) (*http.Response, error) {
	auth := ""
	// Lowercase, not http.CanonicalHeaderKey: HTTP/2 requires lowercase field
	// names and the processor writes the map key that way.
	if values := request.Header["authorization"]; len(values) > 0 { //nolint:staticcheck // SA1008: lowercase is intentional for HTTP/2
		auth = values[0]
	}
	return c.respond(auth), nil
}

var _ HTTPClient = &probeClient{}

// TestTheProbedDeviceIsSentExactlyOnce pins the probe's bookkeeping.
//
// Device 0 of a batch is the probe: it goes out inline, ahead of the rest, so
// its answer is known before the others are released. The loop that follows has
// to start at device 1, because device 0 has already been dealt with -- and the
// WaitGroup was sized once, for every device, before any of this.
//
// Get that wrong and device 0 is processed twice: two requests for one
// notification, and two Done calls for one Add. The counter then reaches zero a
// device early and goes negative on the last one, and a WaitGroup that goes
// negative panics -- taking the process down rather than failing a push.
func TestTheProbedDeviceIsSentExactlyOnce(t *testing.T) {
	path, _ := writeSigningKey(t)
	psp := tokenBatchPSP(t, path)
	processor := newTokenProcessor()

	now := issuedAtBucket(time.Now().UTC()).Add(5 * time.Minute)
	processor.SetClock(func() time.Time { return now })

	// Count the requests per device, by the token in the path.
	var lock sync.Mutex
	perDevice := map[string]int{}
	processor.clientFactory = func(*http.Transport) HTTPClient {
		return &countingByPathClient{onPath: func(path string) {
			lock.Lock()
			perDevice[path]++
			lock.Unlock()
		}}
	}

	// More than one device and a bucket nothing has confirmed, so this batch
	// takes the probe path.
	const devices = 4
	tokens := make([][]byte, devices)
	for i := range tokens {
		tokens[i] = []byte{byte(i + 1), 0x01}
	}

	errChan := make(chan push.Error, devices*2)
	request := &common.PushRequest{
		PSP:       psp,
		Devtokens: tokens,
		Payload:   []byte(`{"aps":{}}`),
		ErrChan:   errChan,
		ResChan:   make(chan *common.APNSResult, devices*2),
	}
	go processor.sendRequests(request)
	for range errChan { //nolint:revive // drained so sendRequests can finish
	}

	lock.Lock()
	defer lock.Unlock()
	if len(perDevice) != devices {
		t.Fatalf("Expected %d devices to be pushed to, got %d", devices, len(perDevice))
	}
	for path, count := range perDevice {
		if count != 1 {
			t.Errorf("Device %s received %d requests, expected 1.\n"+
				"The probe sends device 0 inline and the loop must start after it; sending it "+
				"again means two Done calls for one Add, and the WaitGroup panics when the "+
				"counter goes negative on the batch's last device.", path, count)
		}
	}
}

// countingByPathClient records the request path of everything it is asked to
// send.
type countingByPathClient struct {
	onPath func(path string)
}

func (c *countingByPathClient) Do(request *http.Request) (*http.Response, error) {
	c.onPath(request.URL.Path)
	return okResponse(), nil
}

var _ HTTPClient = &countingByPathClient{}
