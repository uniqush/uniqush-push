package http_api

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/conf"
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// configureTimeout applies an [apns] section holding request_timeout, or one
// holding nothing when present is false.
func configureTimeout(t *testing.T, processor *HTTPPushRequestProcessor, value string, present bool) {
	t.Helper()
	file := conf.NewConfigFile()
	if present {
		file.AddOption("apns", "request_timeout", value)
	}
	processor.SetPushServiceConfig(push.NewPushServiceConfig(file, "apns"))
}

// deadlineOfNextPush sends one push and reports how long the request it made
// had left to run.
func deadlineOfNextPush(t *testing.T, processor *HTTPPushRequestProcessor) time.Duration {
	t.Helper()
	var remaining time.Duration
	var seen bool

	request, errChan, resChan := newPushRequest()
	mockAPNSRequest(processor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		if deadline, ok := r.Context().Deadline(); ok {
			remaining = time.Until(deadline)
			seen = true
		}
		body := newMockResponse([]byte{}, r)
		return &http.Response{StatusCode: http.StatusOK, Body: body}, body, nil
	})

	processor.AddRequest(request)
	handleAPNSResultOrEmitTestError(t, resChan, errChan, func(*common.APNSResult) {})

	if !seen {
		t.Fatal("The request carried no deadline, so nothing bounds a push that APNs never answers")
	}
	return remaining
}

// deadlineSkew is how much of a configured timeout may have elapsed between the
// deadline being set and a test reading it.
//
// Both happen inside one synchronous request against a mocked transport, so the
// real figure is microseconds and a second is three orders of magnitude of slack
// for a loaded CI runner. It was five seconds, which for a five-second timeout
// put the lower bound at zero: the assertion said a deadline existed, not that
// it was the one configured, and a push running on the one-second minimum would
// have passed it.
const deadlineSkew = time.Second

func expectAbout(t *testing.T, got, want time.Duration) {
	t.Helper()
	if got > want || got < want-deadlineSkew {
		t.Errorf("Expected a deadline of about %v (within %v), got %v", want, deadlineSkew, got)
	}
}

// TestRequestTimeoutIsConfigurable covers the [apns] half of #272.
func TestRequestTimeoutIsConfigurable(t *testing.T) {
	processor := newHTTPRequestProcessor()
	configureTimeout(t, processor, "5", true)
	expectAbout(t, deadlineOfNextPush(t, processor), 5*time.Second)
}

// TestRequestTimeoutFallsBackToTheDefault covers the reconfiguration that
// removes the option, alongside the values that cannot be used.
//
// The default stays at the 20 seconds this backend has always used, rather than
// moving to the 30 the other two default to. Nobody asked for their pushes to
// start waiting longer.
func TestRequestTimeoutFallsBackToTheDefault(t *testing.T) {
	testCases := []struct {
		name string
		// preconfigure applies request_timeout=5 first, so the case has a
		// value to undo rather than merely never setting one.
		preconfigure bool
		// apply says whether the service is handed a configuration at all.
		// Without one it has never seen SetPushServiceConfig, which is its own
		// path: an embedder that registers nothing, or any reader of the
		// timeout before the first configuration arrives.
		apply   bool
		value   string
		present bool
	}{
		{name: "never configured"},
		{name: "configured without the option", apply: true},
		{name: "removed after being set", preconfigure: true, apply: true},
		{name: "not a number", preconfigure: true, apply: true, value: "quickly", present: true},
		{name: "below the minimum", preconfigure: true, apply: true, value: "0", present: true},
		{name: "beyond the maximum", preconfigure: true, apply: true, value: "3600", present: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// A processor per case, so that "never configured" means one that
			// has never been configured rather than one the previous case left
			// behind.
			processor := newHTTPRequestProcessor()

			if testCase.preconfigure {
				configureTimeout(t, processor, "5", true)
			}
			if testCase.apply {
				configureTimeout(t, processor, testCase.value, testCase.present)
			}
			expectAbout(t, deadlineOfNextPush(t, processor), defaultRequestTimeout)
		})
	}
}

// TestTheDeadlineIsPerAttempt guards what a client-level Timeout used to give
// the token retry for free.
//
// A 429 carrying TooManyProviderTokenUpdates is retried with the previous
// signing bucket's token, and that retry is a second request to Apple. One
// deadline shared across both would mean a slow first attempt leaving the retry
// no time to run, turning a recoverable refusal into a failed push -- and it
// would do so only under load, which is exactly when the fallback matters.
func TestTheDeadlineIsPerAttempt(t *testing.T) {
	path, _ := writeSigningKey(t)
	psp := tokenBatchPSP(t, path)
	processor := newTokenProcessor()
	configureTimeout(t, processor, "5", true)

	// Five minutes into a bucket, so both it and its predecessor are live and
	// there is a previous token to fall back to.
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

	var lock sync.Mutex
	var deadlines []time.Time
	mockAPNSRequest(processor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		lock.Lock()
		if deadline, ok := r.Context().Deadline(); ok {
			deadlines = append(deadlines, deadline)
		}
		lock.Unlock()

		//nolint:staticcheck // SA1008: HTTP/2 field names are lowercase on the wire
		values := r.Header["authorization"]
		if len(values) > 0 && values[0] == authorizationHeader(current) {
			return refusalResponse(), nil, nil
		}
		return okResponse(), nil, nil
	})

	errChan := make(chan push.Error, 4)
	request := &common.PushRequest{
		PSP:       psp,
		Devtokens: [][]byte{{0x01, 0x02}},
		Payload:   []byte(`{"aps":{"alert":"deadline"}}`),
		ErrChan:   errChan,
		ResChan:   make(chan *common.APNSResult, 4),
	}
	go processor.sendRequests(request)
	for range errChan { //nolint:revive // drained so sendRequests can finish
	}

	lock.Lock()
	defer lock.Unlock()
	if len(deadlines) != 2 {
		t.Fatalf("Expected a refusal and one fallback retry, got %d request(s)", len(deadlines))
	}
	for _, deadline := range deadlines {
		expectAbout(t, time.Until(deadline), 5*time.Second)
	}
	// The precise assertion, and the one a mock that answers instantly can
	// still make: two deadlines set at two moments cannot be the same instant,
	// while a retry that inherited the first request's context would carry
	// exactly the deadline the first one had.
	if !deadlines[1].After(deadlines[0]) {
		t.Errorf("The retry carried the first attempt's deadline (%v), rather than one of its own.\n"+
			"Sharing a deadline means a slow first attempt leaves the fallback no time to run, "+
			"which turns a recoverable 429 into a failed push under exactly the load that causes it.",
			deadlines[0])
	}
}
