package http_api //nolint:revive

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// canonicalUUID matches the form APNs requires for apns-id: 32 lowercase hex
// digits in 8-4-4-4-12 groups.
var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// newPushRequestWithType is newPushRequest with a push type set.
func newPushRequestWithType(pushType string) (*common.PushRequest, chan push.Error, chan *common.APNSResult) {
	request, errChan, resChan := newPushRequest()
	request.PushType = pushType
	return request, errChan, resChan
}

// TestPushTypeHeaderIsSent covers the header APNs has required since iOS 13.
// Without it, a background push gets a 200 from APNs and is then silently
// dropped, which is invisible in logs and was the motivation for this test.
func TestPushTypeHeaderIsSent(t *testing.T) {
	testCases := []struct {
		name             string
		pushType         string
		expectedType     string
		expectedPriority string
	}{
		{
			name:             "empty falls back to alert",
			pushType:         "",
			expectedType:     common.PushTypeAlert,
			expectedPriority: "10",
		},
		{
			name:             "alert is priority 10",
			pushType:         common.PushTypeAlert,
			expectedType:     common.PushTypeAlert,
			expectedPriority: "10",
		},
		{
			// Apple: "Always use priority 5. Using priority 10 is an error."
			name:             "background is forced to priority 5",
			pushType:         common.PushTypeBackground,
			expectedType:     common.PushTypeBackground,
			expectedPriority: "5",
		},
		{
			name:             "voip stays at priority 10",
			pushType:         common.PushTypeVoIP,
			expectedType:     common.PushTypeVoIP,
			expectedPriority: "10",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			requestProcessor := newHTTPRequestProcessor()
			request, errChan, resChan := newPushRequestWithType(testCase.pushType)

			mockAPNSRequest(requestProcessor, func(r *http.Request) (*http.Response, *mockResponse, error) {
				expectHeaderToHaveValue(t, r, "apns-push-type", testCase.expectedType)
				expectHeaderToHaveValue(t, r, "apns-priority", testCase.expectedPriority)
				expectHeaderToHaveValue(t, r, "apns-topic", bundleID)

				// HTTP/2 requires lowercase header names. If these were set via
				// http.Header.Set they would be canonicalised to "Apns-Topic"
				// and sent twice.
				for _, canonical := range []string{"Apns-Push-Type", "Apns-Priority", "Apns-Topic", "Apns-Id"} {
					if values, ok := r.Header[canonical]; ok {
						t.Errorf("Header %s should be lowercase, found canonicalised duplicate %v", canonical, values)
					}
				}

				body := newMockResponse([]byte{}, r)
				return &http.Response{StatusCode: http.StatusOK, Body: body}, body, nil
			})

			requestProcessor.AddRequest(request)
			handleAPNSResultOrEmitTestError(t, resChan, errChan, func(res *common.APNSResult) {
				if res.Status != common.Status0Success {
					t.Errorf("Expected success, got status %d", res.Status)
				}
			})
		})
	}
}

// TestAPNSIDHeaderIsSentAndUnique checks apns-id is present, well formed, and
// distinct per device token. Apple generates one if we omit it, but then it
// exists only in a response we do not persist.
func TestAPNSIDHeaderIsSentAndUnique(t *testing.T) {
	requestProcessor := newHTTPRequestProcessor()

	errChan := make(chan push.Error)
	resChan := make(chan *common.APNSResult, 3)
	request := &common.PushRequest{
		PSP:       pushServiceProvider,
		Devtokens: [][]byte{[]byte("token_one"), []byte("token_two"), []byte("token_three")},
		Payload:   payload,
		ErrChan:   errChan,
		ResChan:   resChan,
	}

	var mu sync.Mutex
	seen := map[string]bool{}
	mockAPNSRequest(requestProcessor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		// Indexed with the lowercase key on purpose. HTTP/2 requires lowercase
		// header names and the processor sets them that way; looking this up
		// canonically would silently find nothing.
		values := r.Header["apns-id"] //nolint:staticcheck // SA1008: lowercase is intentional for HTTP/2
		if len(values) != 1 {
			t.Errorf("Expected exactly one apns-id header, got %v", values)
		} else {
			if !canonicalUUID.MatchString(values[0]) {
				t.Errorf("apns-id %q is not a canonical lowercase UUID", values[0])
			}
			mu.Lock()
			if seen[values[0]] {
				t.Errorf("apns-id %q was reused across device tokens", values[0])
			}
			seen[values[0]] = true
			mu.Unlock()
		}
		body := newMockResponse([]byte{}, r)
		return &http.Response{StatusCode: http.StatusOK, Body: body}, body, nil
	})

	requestProcessor.AddRequest(request)
	for i := 0; i < 3; i++ {
		<-resChan
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Errorf("Expected 3 distinct apns-id values, got %d", len(seen))
	}
}

// awaitOutcome waits for a push to finish and reports whichever channel carried
// the real signal.
//
// Selecting over both channels directly is a coin flip, and it flips on loaded
// CI machines rather than on a quiet laptop. sendRequests does
// `defer close(request.ErrChan)`, so for a successful push there is a moment
// where a result is sitting in the buffered resChan *and* errChan has been
// closed. Go picks uniformly among ready cases, and a receive from a closed
// channel yields a nil push.Error -- which reads as "an error occurred, and it
// was nil".
//
// So a closed or nil errChan is not an outcome; it just means there was no
// error. Disable that case and keep waiting for the result.
func awaitOutcome(t *testing.T, resChan <-chan *common.APNSResult, errChan <-chan push.Error) (*common.APNSResult, push.Error) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case res := <-resChan:
			return res, nil
		case err, open := <-errChan:
			if open && err != nil {
				return nil, err
			}
			errChan = nil // a nil channel blocks forever, removing this case
		case <-deadline:
			t.Fatal("Timed out waiting for a result or an error")
			return nil, nil
		}
	}
}

// TestPermanentTokenFailuresUnsubscribe covers the reasons that mean a device
// token is dead forever. Before this change only BadDeviceToken and a bare 410
// were handled, so Unregistered, ExpiredToken and DeviceTokenNotForTopic leaked
// subscriptions that could never be delivered to again.
func TestPermanentTokenFailuresUnsubscribe(t *testing.T) {
	testCases := []struct {
		name              string
		statusCode        int
		reason            string
		expectUnsubscribe bool
	}{
		{name: "400 BadDeviceToken", statusCode: 400, reason: "BadDeviceToken", expectUnsubscribe: true},
		{name: "400 DeviceTokenNotForTopic", statusCode: 400, reason: "DeviceTokenNotForTopic", expectUnsubscribe: true},
		{name: "410 Unregistered", statusCode: 410, reason: "Unregistered", expectUnsubscribe: true},
		{name: "410 ExpiredToken", statusCode: 410, reason: "ExpiredToken", expectUnsubscribe: true},

		// These are provider or payload problems. Unsubscribing on them would
		// destroy perfectly good subscriptions because of our own bug.
		{name: "413 PayloadTooLarge is not the token's fault", statusCode: 413, reason: "PayloadTooLarge"},
		{name: "400 BadTopic is not the token's fault", statusCode: 400, reason: "BadTopic"},
		{name: "403 BadCertificate is not the token's fault", statusCode: 403, reason: "BadCertificate"},
		{name: "429 TooManyRequests is transient", statusCode: 429, reason: "TooManyRequests"},
		{name: "503 ServiceUnavailable is transient", statusCode: 503, reason: "ServiceUnavailable"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			requestProcessor := newHTTPRequestProcessor()
			request, errChan, resChan := newPushRequest()

			mockAPNSRequest(requestProcessor, func(r *http.Request) (*http.Response, *mockResponse, error) {
				responseBody, err := json.Marshal(map[string]interface{}{
					"reason":    testCase.reason,
					"timestamp": 1234567890000,
				})
				if err != nil {
					t.Fatalf("Could not build mock response: %v", err)
				}
				body := newMockResponse(responseBody, r)
				return &http.Response{StatusCode: testCase.statusCode, Body: body}, body, nil
			})

			requestProcessor.AddRequest(request)

			res, err := awaitOutcome(t, resChan, errChan)
			if testCase.expectUnsubscribe {
				if err != nil {
					t.Fatalf("Expected an unsubscribe for reason %q, got error: %v", testCase.reason, err)
				}
				if res.Status != common.Status8Unsubscribe {
					t.Errorf("Expected unsubscribe status %d, got %d", common.Status8Unsubscribe, res.Status)
				}
				return
			}
			if err == nil {
				t.Fatalf("Expected an error for reason %q, got result status %d", testCase.reason, res.Status)
			}
			if !strings.Contains(err.Error(), testCase.reason) {
				t.Errorf("Expected error to mention reason %q, got: %v", testCase.reason, err)
			}
		})
	}
}

// TestGoneWithEmptyBodyUnsubscribes covers a 410 that arrives without a JSON
// body. Apple always sends a reason, but leaking an undeliverable subscription
// because of a truncated response would be the wrong way to be wrong.
func TestGoneWithEmptyBodyUnsubscribes(t *testing.T) {
	requestProcessor := newHTTPRequestProcessor()
	request, errChan, resChan := newPushRequest()

	mockAPNSRequest(requestProcessor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		body := newMockResponse([]byte{}, r)
		return &http.Response{StatusCode: http.StatusGone, Body: body}, body, nil
	})

	requestProcessor.AddRequest(request)

	res, err := awaitOutcome(t, resChan, errChan)
	if err != nil {
		t.Fatalf("Expected an unsubscribe, got error: %v", err)
	}
	if res.Status != common.Status8Unsubscribe {
		t.Errorf("Expected unsubscribe status %d, got %d", common.Status8Unsubscribe, res.Status)
	}
}

// TestUnparseableErrorBodyReportsAnError makes sure a non-JSON body (an HTML
// error page from a proxy, say) surfaces as an error rather than being treated
// as an empty reason.
func TestUnparseableErrorBodyReportsAnError(t *testing.T) {
	requestProcessor := newHTTPRequestProcessor()
	request, errChan, resChan := newPushRequest()

	mockAPNSRequest(requestProcessor, func(r *http.Request) (*http.Response, *mockResponse, error) {
		body := newMockResponse([]byte("<html>502 Bad Gateway</html>"), r)
		return &http.Response{StatusCode: http.StatusBadGateway, Body: body}, body, nil
	})

	requestProcessor.AddRequest(request)

	res, err := awaitOutcome(t, resChan, errChan)
	if err == nil {
		t.Fatalf("Expected an error, got result status %d", res.Status)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("Expected the error to mention the status code, got: %v", err)
	}
}
