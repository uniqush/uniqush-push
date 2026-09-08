package webpush

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/uniqush/goconf/conf"

	"github.com/uniqush/uniqush-push/push"
)

// newSubscriptionKeys returns a p256dh/auth pair that is cryptographically
// valid, so webpush-go's ECDH actually succeeds rather than erroring out.
func newSubscriptionKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Could not generate a P-256 key: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatalf("Could not generate an auth secret: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(authSecret)
}

// roundTripFunc lets a test stand in for the network while still exercising the
// real *http.Client, including its redirect policy and timeout.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newResponse(statusCode int, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: statusCode,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

// newTestService builds a service whose network calls are captured and whose
// SSRF policy permits the fake endpoint.
func newTestService(t *testing.T, handler roundTripFunc) *pushService {
	t.Helper()
	service := NewPushService("webpush").(*pushService)
	service.policy.AllowPrivateAddresses = true
	// http.RoundTripper's contract is that the transport closes the request
	// body, including on error. Every round tripper in these tests goes through
	// here, so do it once rather than in each of them.
	service.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Body != nil {
			defer request.Body.Close()
		}
		return handler(request)
	})
	return service
}

func newTestPSP(t *testing.T, service *pushService) *push.PushServiceProvider {
	t.Helper()
	privateKey, publicKey, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("Could not generate VAPID keys: %v", err)
	}
	psp := push.NewEmptyPushServiceProvider()
	if err := service.BuildPushServiceProviderFromMap(map[string]string{
		"service":         "testservice",
		"pushservicetype": "webpush",
		"vapidpublickey":  publicKey,
		"vapidprivatekey": privateKey,
		"subscriber":      "admin@example.org",
	}, psp); err != nil {
		t.Fatalf("Could not build push service provider: %v", err)
	}
	return psp
}

// testEndpoint is a private address; tests that use it set
// AllowPrivateAddresses so the SSRF policy does not reject it.
const testEndpoint = "https://10.0.0.1/up?id=abc"

func newTestDP(t *testing.T, service *pushService) *push.DeliveryPoint {
	t.Helper()
	endpoint := testEndpoint
	p256dh, auth := newSubscriptionKeys(t)
	dp := push.NewEmptyDeliveryPoint()
	if err := service.BuildDeliveryPointFromMap(map[string]string{
		"service":         "testservice",
		"subscriber":      "testsubscriber",
		"pushservicetype": "webpush",
		"endpoint":        endpoint,
		"p256dh":          p256dh,
		"auth":            auth,
	}, dp); err != nil {
		t.Fatalf("Could not build delivery point: %v", err)
	}
	return dp
}

func TestBuildPushServiceProviderFromMap(t *testing.T) {
	service := NewPushService("webpush").(*pushService)
	privateKey, publicKey, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("Could not generate VAPID keys: %v", err)
	}

	base := map[string]string{
		"service":         "testservice",
		"vapidpublickey":  publicKey,
		"vapidprivatekey": privateKey,
		"subscriber":      "admin@example.org",
	}

	t.Run("accepts a complete provider", func(t *testing.T) {
		psp := push.NewEmptyPushServiceProvider()
		if err := service.BuildPushServiceProviderFromMap(base, psp); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		// The private key must not be part of the provider's identity, and must
		// not be returned by /psps, so it belongs in VolatileData.
		if psp.FixedData["vapidprivatekey"] != "" {
			t.Error("The VAPID private key must not be stored in FixedData")
		}
		if psp.VolatileData["vapidprivatekey"] != privateKey {
			t.Error("The VAPID private key should be stored in VolatileData")
		}
		if psp.FixedData["vapidpublickey"] != publicKey {
			t.Error("The VAPID public key should be stored in FixedData")
		}
	})

	t.Run("strips a mailto prefix", func(t *testing.T) {
		// webpush-go prepends "mailto:" to anything that is not an https URL,
		// so storing a pre-formed URI would produce "mailto:mailto:...".
		kv := cloneMap(base)
		kv["subscriber"] = "mailto:admin@example.org"
		psp := push.NewEmptyPushServiceProvider()
		if err := service.BuildPushServiceProviderFromMap(kv, psp); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if got := psp.FixedData["subscriber"]; got != "admin@example.org" {
			t.Errorf("Expected the mailto: prefix to be stripped, got %q", got)
		}
	})

	t.Run("rejects missing and malformed fields", func(t *testing.T) {
		testCases := []struct {
			name   string
			mutate func(map[string]string)
		}{
			{"no service", func(kv map[string]string) { delete(kv, "service") }},
			{"no public key", func(kv map[string]string) { delete(kv, "vapidpublickey") }},
			{"no private key", func(kv map[string]string) { delete(kv, "vapidprivatekey") }},
			{"no subscriber", func(kv map[string]string) { delete(kv, "subscriber") }},
			{"bare mailto subscriber", func(kv map[string]string) { kv["subscriber"] = "mailto:" }},
			{"public key not base64", func(kv map[string]string) { kv["vapidpublickey"] = "!!!not base64!!!" }},
			{"public key wrong length", func(kv map[string]string) {
				kv["vapidpublickey"] = base64.RawURLEncoding.EncodeToString([]byte("too short"))
			}},
			{"private key wrong length", func(kv map[string]string) {
				kv["vapidprivatekey"] = base64.RawURLEncoding.EncodeToString([]byte("too short"))
			}},
			{"keys swapped", func(kv map[string]string) {
				kv["vapidpublickey"], kv["vapidprivatekey"] = kv["vapidprivatekey"], kv["vapidpublickey"]
			}},
		}
		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				kv := cloneMap(base)
				testCase.mutate(kv)
				psp := push.NewEmptyPushServiceProvider()
				if err := service.BuildPushServiceProviderFromMap(kv, psp); err == nil {
					t.Error("Expected an error")
				}
			})
		}
	})
}

func TestBuildDeliveryPointFromMap(t *testing.T) {
	service := NewPushService("webpush").(*pushService)
	p256dh, auth := newSubscriptionKeys(t)

	base := map[string]string{
		"service":    "testservice",
		"subscriber": "testsubscriber",
		"endpoint":   "https://ntfy.sh/up?id=abcdef",
		"p256dh":     p256dh,
		"auth":       auth,
	}

	t.Run("accepts a complete subscription", func(t *testing.T) {
		dp := push.NewEmptyDeliveryPoint()
		if err := service.BuildDeliveryPointFromMap(base, dp); err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		// The endpoint defines this delivery point's identity: two subscriptions
		// with the same endpoint are the same device.
		if dp.FixedData["endpoint"] != base["endpoint"] {
			t.Error("The endpoint should be stored in FixedData")
		}
	})

	t.Run("accepts standard-alphabet base64 keys", func(t *testing.T) {
		// Connector libraries differ on which base64 alphabet they emit.
		raw, err := base64.RawURLEncoding.DecodeString(p256dh)
		if err != nil {
			t.Fatalf("Could not decode the test key: %v", err)
		}
		kv := cloneMap(base)
		kv["p256dh"] = base64.StdEncoding.EncodeToString(raw)
		dp := push.NewEmptyDeliveryPoint()
		if err := service.BuildDeliveryPointFromMap(kv, dp); err != nil {
			t.Errorf("Expected standard base64 to be accepted, got: %v", err)
		}
	})

	t.Run("rejects missing and malformed fields", func(t *testing.T) {
		testCases := []struct {
			name   string
			mutate func(map[string]string)
		}{
			{"no endpoint", func(kv map[string]string) { delete(kv, "endpoint") }},
			{"no p256dh", func(kv map[string]string) { delete(kv, "p256dh") }},
			{"no auth", func(kv map[string]string) { delete(kv, "auth") }},
			{"endpoint is not a URL", func(kv map[string]string) { kv["endpoint"] = "://nonsense" }},
			{"endpoint scheme rejected", func(kv map[string]string) { kv["endpoint"] = "file:///etc/passwd" }},
			{"p256dh wrong length", func(kv map[string]string) {
				kv["p256dh"] = base64.RawURLEncoding.EncodeToString([]byte("short"))
			}},
			{"auth wrong length", func(kv map[string]string) {
				kv["auth"] = base64.RawURLEncoding.EncodeToString([]byte("short"))
			}},
		}
		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				kv := cloneMap(base)
				testCase.mutate(kv)
				dp := push.NewEmptyDeliveryPoint()
				if err := service.BuildDeliveryPointFromMap(kv, dp); err == nil {
					t.Error("Expected an error")
				}
			})
		}
	})
}

// pushOnce runs a single push and returns the one result.
func pushOnce(t *testing.T, service *pushService, psp *push.PushServiceProvider, dp *push.DeliveryPoint, notif *push.Notification) *push.Result {
	t.Helper()
	dpQueue := make(chan *push.DeliveryPoint, 1)
	dpQueue <- dp
	close(dpQueue)
	resQueue := make(chan *push.Result, 4)
	service.Push(psp, dpQueue, resQueue, notif)

	var results []*push.Result
	for res := range resQueue {
		results = append(results, res)
	}
	if len(results) != 1 {
		t.Fatalf("Expected exactly 1 result, got %d", len(results))
	}
	return results[0]
}

func TestPushOutcomes(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		header     http.Header
		check      func(t *testing.T, result *push.Result)
	}{
		{
			name:       "201 Created is success",
			statusCode: 201,
			header:     http.Header{"Location": []string{"https://ntfy.sh/message/1"}},
			check: func(t *testing.T, result *push.Result) {
				if result.Err != nil {
					t.Errorf("Expected success, got: %v", result.Err)
				}
				if result.MsgID != "https://ntfy.sh/message/1" {
					t.Errorf("Expected the Location header as MsgID, got %q", result.MsgID)
				}
			},
		},
		{
			// The spec says to accept anything in 200-299, not just 201.
			name:       "200 OK is also success",
			statusCode: 200,
			check: func(t *testing.T, result *push.Result) {
				if result.Err != nil {
					t.Errorf("Expected success, got: %v", result.Err)
				}
			},
		},
		{
			name:       "404 unsubscribes",
			statusCode: 404,
			check:      expectUnsubscribe,
		},
		{
			name:       "410 unsubscribes",
			statusCode: 410,
			check:      expectUnsubscribe,
		},
		{
			// Our bug, not a dead endpoint. Unsubscribing here would destroy a
			// perfectly good subscription.
			name:       "400 is a bad notification, not an unsubscribe",
			statusCode: 400,
			check:      expectBadNotification,
		},
		{
			name:       "413 is a bad notification, not an unsubscribe",
			statusCode: 413,
			check:      expectBadNotification,
		},
		{
			name:       "429 is retryable",
			statusCode: 429,
			header:     http.Header{"Retry-After": []string{"120"}},
			check: func(t *testing.T, result *push.Result) {
				retryErr, ok := result.Err.(*push.RetryError)
				if !ok {
					t.Fatalf("Expected a RetryError, got %T: %v", result.Err, result.Err)
				}
				if retryErr.After.Seconds() != 120 {
					t.Errorf("Expected the Retry-After header to be honoured, got %v", retryErr.After)
				}
			},
		},
		{
			name:       "503 is retryable",
			statusCode: 503,
			check: func(t *testing.T, result *push.Result) {
				if _, ok := result.Err.(*push.RetryError); !ok {
					t.Errorf("Expected a RetryError, got %T: %v", result.Err, result.Err)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := newTestService(t, func(*http.Request) (*http.Response, error) {
				return newResponse(testCase.statusCode, testCase.header), nil
			})
			defer service.Finalize()

			psp := newTestPSP(t, service)
			dp := newTestDP(t, service)
			result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "hi"}})
			testCase.check(t, result)
		})
	}
}

// A push server explains its refusal in the response body, and the status code
// alone leaves an operator guessing. ntfy's 507 is the case that motivated this:
// it means the UnifiedPush topic has no registered subscriber, which no amount
// of staring at "507" would tell anyone.
func TestPushQuotesTheErrorBody(t *testing.T) {
	const body = `{"code":50701,"http":507,"error":"cannot publish to UnifiedPush topic without previously active subscriber"}`

	testCases := []struct {
		name       string
		statusCode int
		want       string
	}{
		{name: "retryable status", statusCode: 507, want: "50701"},
		{name: "bad notification", statusCode: 400, want: "50701"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := newTestService(t, func(*http.Request) (*http.Response, error) {
				response := newResponse(testCase.statusCode, nil)
				response.Body = io.NopCloser(strings.NewReader(body))
				return response, nil
			})
			defer service.Finalize()

			psp := newTestPSP(t, service)
			dp := newTestDP(t, service)
			result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "hi"}})

			if result.Err == nil {
				t.Fatalf("Expected an error for HTTP %d", testCase.statusCode)
			}
			message := result.Err.Error()
			if !strings.Contains(message, testCase.want) {
				t.Errorf("Expected the error to quote the response body, got: %s", message)
			}
			// "HTTP 507" and not bare "507": the body itself contains 507 twice,
			// so a looser check would pass on an error message that named no
			// status code at all.
			if status := fmt.Sprintf("HTTP %d", testCase.statusCode); !strings.Contains(message, status) {
				t.Errorf("Expected the error to name %q, got: %s", status, message)
			}
		})
	}
}

// A push server's body reaches log lines, so it must not be able to carry a
// newline and forge a record. Flattening happens where the bytes are read.
func TestPushFlattensControlCharactersInErrorBodies(t *testing.T) {
	service := newTestService(t, func(*http.Request) (*http.Response, error) {
		response := newResponse(503, nil)
		response.Body = io.NopCloser(strings.NewReader("rate limited\n[Push][Info] forged record\x00\x1b[31m"))
		return response, nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	dp := newTestDP(t, service)
	result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "hi"}})

	if result.Err == nil {
		t.Fatal("Expected an error for HTTP 503")
	}
	message := result.Err.Error()
	if strings.ContainsAny(message, "\n\r\x00\x1b") {
		t.Errorf("Expected control characters to be removed, got: %q", message)
	}
	if !strings.Contains(message, "rate limited [Push][Info] forged record") {
		t.Errorf("Expected the body text to survive flattening, got: %q", message)
	}
}

// An http.Client may return a response with no body. This runs on a path where
// the push has already failed, so a panic here would take the server with it.
func TestPushHandlesNilResponseBody(t *testing.T) {
	service := newTestService(t, func(*http.Request) (*http.Response, error) {
		response := newResponse(503, nil)
		response.Body = nil
		return response, nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	dp := newTestDP(t, service)
	result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "hi"}})

	if result.Err == nil {
		t.Fatal("Expected an error for HTTP 503")
	}
	if !strings.Contains(result.Err.Error(), "HTTP 503") {
		t.Errorf("Expected the error to name the status code, got: %s", result.Err)
	}
}

// A body long enough to swamp a log line is truncated rather than quoted whole.
func TestPushTruncatesLongErrorBodies(t *testing.T) {
	service := newTestService(t, func(*http.Request) (*http.Response, error) {
		response := newResponse(503, nil)
		response.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", 4096)))
		return response, nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	dp := newTestDP(t, service)
	result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "hi"}})

	if result.Err == nil {
		t.Fatal("Expected an error for HTTP 503")
	}
	if quoted := strings.Count(result.Err.Error(), "x"); quoted > maxErrorBodyBytes {
		t.Errorf("Quoted %d bytes of the body, expected at most %d", quoted, maxErrorBodyBytes)
	}
}

func expectUnsubscribe(t *testing.T, result *push.Result) {
	t.Helper()
	if _, ok := result.Err.(*push.UnsubscribeUpdate); !ok {
		t.Errorf("Expected an UnsubscribeUpdate, got %T: %v", result.Err, result.Err)
	}
}

func expectBadNotification(t *testing.T, result *push.Result) {
	t.Helper()
	if _, ok := result.Err.(*push.BadNotification); !ok {
		t.Errorf("Expected a BadNotification, got %T: %v", result.Err, result.Err)
	}
}

// TestPushSendsEncryptedBody checks the wire format UnifiedPush requires.
// The spec is explicit that a Content-Encoding of anything but aes128gcm means
// the library is implementing an obsolete draft and will not work.
func TestPushSendsEncryptedBody(t *testing.T) {
	var captured *http.Request
	var body []byte
	service := newTestService(t, func(r *http.Request) (*http.Response, error) {
		captured = r
		body, _ = io.ReadAll(r.Body)
		return newResponse(201, nil), nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	dp := newTestDP(t, service)
	plaintext := "a short message"
	pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": plaintext}})

	if captured == nil {
		t.Fatal("No request was made")
	}
	if got := captured.Header.Get("Content-Encoding"); got != "aes128gcm" {
		t.Errorf("Expected Content-Encoding aes128gcm (RFC 8291 final), got %q", got)
	}
	if got := captured.Header.Get("TTL"); got == "" || got == "0" {
		// A TTL of 0 means "drop unless the device is connected right now", and
		// Microsoft WNS rejects it outright.
		t.Errorf("Expected a non-zero TTL header, got %q", got)
	}
	if got := captured.Header.Get("Authorization"); !strings.HasPrefix(got, "vapid") {
		t.Errorf("Expected a VAPID Authorization header, got %q", got)
	}
	if strings.Contains(string(body), plaintext) {
		t.Error("The plaintext appears in the request body; the payload was not encrypted")
	}
	// webpush-go pads every record to its full size, so the body length reveals
	// nothing about the payload -- and must stay under the 4096-byte spec limit.
	if len(body) != defaultRecordSize {
		t.Errorf("Expected the body to be padded to exactly %d bytes, got %d", defaultRecordSize, len(body))
	}
	if len(body) > 4096 {
		t.Errorf("Body of %d bytes exceeds the 4096-byte UnifiedPush limit", len(body))
	}
}

// TestPushRejectsPrivateEndpointAtSendTime is the DNS-rebinding guard: an
// endpoint that passed validation at /subscribe must be re-checked per push.
func TestPushRejectsPrivateEndpointAtSendTime(t *testing.T) {
	var called bool
	service := newTestService(t, func(*http.Request) (*http.Response, error) {
		called = true
		return newResponse(201, nil), nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	dp := newTestDP(t, service)

	// Re-enable the policy after the delivery point was accepted, standing in
	// for a name that has since started resolving to a private address.
	service.policy.AllowPrivateAddresses = false

	result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "hi"}})
	if called {
		t.Error("A request was made to a private address")
	}
	if _, ok := result.Err.(*push.BadDeliveryPoint); !ok {
		t.Errorf("Expected a BadDeliveryPoint, got %T: %v", result.Err, result.Err)
	}
}

// TestPushDoesNotFollowRedirects covers two things at once. The UnifiedPush
// spec says plainly that "Redirects MUST NOT be followed on push endpoints",
// and following them would also defeat the SSRF checks: only the endpoint URL
// is vetted, so a push server answering 302 with a Location of
// http://169.254.169.254/ would have uniqush fetch it.
func TestPushDoesNotFollowRedirects(t *testing.T) {
	var mu sync.Mutex
	var requestedURLs []string

	service := newTestService(t, func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		requestedURLs = append(requestedURLs, r.URL.String())
		mu.Unlock()
		return newResponse(302, http.Header{
			"Location": []string{"http://169.254.169.254/latest/meta-data/"},
		}), nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	dp := newTestDP(t, service)
	result := pushOnce(t, service, psp, dp, &push.Notification{Data: map[string]string{"msg": "hi"}})

	mu.Lock()
	defer mu.Unlock()
	if len(requestedURLs) != 1 {
		t.Fatalf("Expected exactly 1 request, got %d: %v", len(requestedURLs), requestedURLs)
	}
	for _, requested := range requestedURLs {
		if strings.Contains(requested, "169.254.169.254") {
			t.Errorf("Followed a redirect to a link-local address: %s", requested)
		}
	}
	// A 3xx is not success. It should surface rather than being swallowed.
	if result.Err == nil {
		t.Error("Expected a redirect response to be reported as an error, not treated as success")
	}
}

func TestPushRejectsOversizedPayload(t *testing.T) {
	service := newTestService(t, func(*http.Request) (*http.Response, error) {
		t.Error("No request should be made for an oversized payload")
		return newResponse(201, nil), nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	dp := newTestDP(t, service)
	notif := &push.Notification{Data: map[string]string{
		payloadKey: strings.Repeat("x", service.maxPayloadSize()+1),
	}}

	result := pushOnce(t, service, psp, dp, notif)
	if _, ok := result.Err.(*push.BadNotification); !ok {
		t.Errorf("Expected a BadNotification, got %T: %v", result.Err, result.Err)
	}
}

// TestPushDrainsQueueOnPayloadError makes sure a bad payload cannot wedge the
// caller, which is blocked writing to dpQueue.
func TestPushDrainsQueueOnPayloadError(t *testing.T) {
	service := newTestService(t, func(*http.Request) (*http.Response, error) {
		return newResponse(201, nil), nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	dpQueue := make(chan *push.DeliveryPoint)
	resQueue := make(chan *push.Result, 8)

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 3; i++ {
			dpQueue <- newTestDP(t, service)
		}
		close(dpQueue)
	}()

	// An empty notification has no payload to send.
	service.Push(psp, dpQueue, resQueue, &push.Notification{Data: map[string]string{}})
	wg.Wait() // would deadlock if Push stopped reading dpQueue

	count := 0
	for range resQueue {
		count++
	}
	if count != 1 {
		t.Errorf("Expected a single error result, got %d", count)
	}
}

func TestPushMultipleDeliveryPoints(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	service := newTestService(t, func(*http.Request) (*http.Response, error) {
		mu.Lock()
		requests++
		mu.Unlock()
		return newResponse(201, nil), nil
	})
	defer service.Finalize()

	psp := newTestPSP(t, service)
	const count = 25 // more than maxConcurrentPushes, to exercise the semaphore

	dpQueue := make(chan *push.DeliveryPoint, count)
	for i := 0; i < count; i++ {
		dpQueue <- newTestDP(t, service)
	}
	close(dpQueue)

	resQueue := make(chan *push.Result, count)
	service.Push(psp, dpQueue, resQueue, &push.Notification{Data: map[string]string{"msg": "hi"}})

	results := 0
	for res := range resQueue {
		results++
		if res.Err != nil {
			t.Errorf("Unexpected error: %v", res.Err)
		}
	}
	if results != count {
		t.Errorf("Expected %d results, got %d", count, results)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != count {
		t.Errorf("Expected %d requests, got %d", count, requests)
	}
}

func TestToWebPushPayload(t *testing.T) {
	t.Run("raw payload passes through verbatim", func(t *testing.T) {
		raw := `{"custom":"blob"}`
		payload, err := toWebPushPayload(&push.Notification{Data: map[string]string{payloadKey: raw}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if string(payload) != raw {
			t.Errorf("Expected %q, got %q", raw, payload)
		}
	})

	t.Run("uniqush control parameters are stripped", func(t *testing.T) {
		payload, err := toWebPushPayload(&push.Notification{Data: map[string]string{
			"msg":              "hello",
			"uniqush.selfonly": "1",
			"uniqush.ttl":      "60",
		}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		var decoded map[string]string
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("Payload is not valid JSON: %v", err)
		}
		if decoded["msg"] != "hello" {
			t.Errorf("Expected msg to survive, got %v", decoded)
		}
		for key := range decoded {
			if strings.HasPrefix(key, "uniqush.") {
				t.Errorf("uniqush control parameter %q leaked into the payload", key)
			}
		}
	})

	t.Run("empty notification is rejected", func(t *testing.T) {
		if _, err := toWebPushPayload(&push.Notification{Data: map[string]string{}}); err == nil {
			t.Error("Expected an error for an empty payload")
		}
	})
}

func TestPreviewReturnsPlaintext(t *testing.T) {
	service := NewPushService("webpush")
	payload, err := service.Preview(&push.Notification{Data: map[string]string{"msg": "hello"}})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	// Preview shows what will be encrypted, not the ciphertext: RFC 8291
	// encryption is randomised and subscriber-specific, so the bytes on the wire
	// are neither reproducible nor useful for debugging.
	if !strings.Contains(string(payload), "hello") {
		t.Errorf("Expected the plaintext payload, got %q", payload)
	}
}

func TestServiceName(t *testing.T) {
	// The same implementation is registered under both names.
	for _, name := range []string{"webpush", "unifiedpush"} {
		if got := NewPushService(name).Name(); got != name {
			t.Errorf("Expected name %q, got %q", name, got)
		}
	}
}

func cloneMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

// The record size is configurable because it is a trade-off: smaller records
// halve the bytes on the wire, larger ones are what Tink-based clients insist on
// and raise the payload ceiling. Whatever it is set to, the body must come out
// exactly that size, since webpush-go pads to fill it.
func TestRecordSizeConfiguration(t *testing.T) {
	t.Run("a new service starts at the default", func(t *testing.T) {
		service := NewPushService("webpush").(*pushService)
		defer service.Finalize()
		if service.recordSize.Load() != defaultRecordSize {
			t.Errorf("Expected a record size of %d, got %d", defaultRecordSize, service.recordSize.Load())
		}
		// The default has to be a value the config would accept, or removing
		// record_size from a config file would produce a service that cannot
		// send anything.
		if defaultRecordSize < minRecordSize || defaultRecordSize > maxRecordSize {
			t.Errorf("The default record size %d is outside the accepted range %d-%d",
				defaultRecordSize, minRecordSize, maxRecordSize)
		}
	})

	// The arithmetic, against figures that come from outside this package: 3993
	// is the UnifiedPush spec's own number for the largest cleartext that fits
	// in a 4096-byte message.
	t.Run("payload ceilings", func(t *testing.T) {
		testCases := map[uint32]int{4096: 3993, 2048: 1945}
		for recordSize, want := range testCases {
			service := NewPushService("webpush").(*pushService)
			service.recordSize.Store(recordSize)
			if got := service.maxPayloadSize(); got != want {
				t.Errorf("record size %d gives a payload ceiling of %d, want %d", recordSize, got, want)
			}
			service.Finalize()
		}
	})

	t.Run("a configured size reaches the wire", func(t *testing.T) {
		var body []byte
		service := newTestService(t, func(request *http.Request) (*http.Response, error) {
			body, _ = io.ReadAll(request.Body)
			return newResponse(201, nil), nil
		})
		defer service.Finalize()
		// Through the config, not by assignment: what matters is that the
		// supported way of setting this reaches the bytes on the wire.
		service.SetPushServiceConfig(configWithRecordSize(t, 2048))
		if service.recordSize.Load() != 2048 {
			t.Fatalf("Expected the config to set a record size of 2048, got %d", service.recordSize.Load())
		}

		psp := newTestPSP(t, service)
		dp := newTestDP(t, service)
		if result := pushOnce(t, service, psp, dp, &push.Notification{
			Data: map[string]string{"msg": "hi"},
		}); result.Err != nil {
			t.Fatalf("Unexpected error: %v", result.Err)
		}
		if len(body) != 2048 {
			t.Errorf("Expected a 2048-byte body, got %d", len(body))
		}
	})

	t.Run("out-of-range values fall back to the default", func(t *testing.T) {
		for _, size := range []int{0, -1, 17, minRecordSize - 1, maxRecordSize + 1, 1 << 20} {
			service := NewPushService("webpush").(*pushService)
			service.recordSize.Store(2048) // as if a previous config had set it
			service.SetPushServiceConfig(configWithRecordSize(t, size))
			if service.recordSize.Load() != defaultRecordSize {
				t.Errorf("record_size=%d gave a record size of %d, expected the default %d",
					size, service.recordSize.Load(), defaultRecordSize)
			}
			service.Finalize()
		}
	})

	t.Run("an in-range value is accepted", func(t *testing.T) {
		service := NewPushService("webpush").(*pushService)
		defer service.Finalize()
		service.SetPushServiceConfig(configWithRecordSize(t, minRecordSize))
		if service.recordSize.Load() != minRecordSize {
			t.Errorf("Expected a record size of %d, got %d", minRecordSize, service.recordSize.Load())
		}
	})
}

// configWithRecordSize builds a [webpush] section containing just record_size.
func configWithRecordSize(t *testing.T, size int) *push.PushServiceConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "uniqush.conf")
	contents := fmt.Sprintf("[webpush]\nrecord_size=%d\n", size)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("Could not write the test config: %v", err)
	}
	file, err := conf.ReadConfigFile(path)
	if err != nil {
		t.Fatalf("Could not read the test config: %v", err)
	}
	return push.NewPushServiceConfig(file, "webpush")
}
