package apns

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/apnstest"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// End-to-end tests for the HTTP/2 push path, against the simulator in
// srv/apns/apnstest.
//
// Everything else in this package tests the push service with the transport
// mocked out, so nothing has ever exercised the code that opens a TLS
// connection, negotiates h2, builds the headers and parses what comes back --
// the exact code the 2026 repairs changed. These tests do, over a real socket.
//
// The simulator enforces Apple's documented contract rather than accepting
// whatever arrives, so a violation of it fails the test with the rule that was
// broken. That is the difference between this and a mock: a permissive mock
// would have happily accepted every bug those repairs fixed.

const conformanceTopic = "com.example.conformance"

// newSimulatorService builds a push service whose HTTP/2 transport is real.
//
// Note what is *not* replaced: httpRequestProcessor is left as
// http_api.NewRequestProcessor(), so this drives the production code path.
// Only the binary processor is left alone, since these tests never select it.
func newSimulatorService(t *testing.T) (*pushService, chan push.Error) {
	t.Helper()

	service := NewPushService().(*pushService)
	// Buffered generously: waitResults reports asynchronously and a test that
	// does not read every message must not wedge the goroutine producing them.
	errChan := make(chan push.Error, 100)
	service.SetErrorReportChan(errChan)
	t.Cleanup(service.Finalize)
	return service, errChan
}

// ensureAPNSRegistered makes sure the manager can build an "apns" provider.
//
// Providers have to be built through the manager, because that is what attaches
// the push service type -- without it PushPeer.Name() dereferences a nil. The
// manager is a singleton that rejects duplicate names, and other tests in this
// package register their own mock-backed service, so whichever runs first wins.
// Either is fine here: the registered instance is only consulted for building
// and for its name, while each test drives its own instance directly.
var registerAPNSOnce sync.Once

func ensureAPNSRegistered() {
	registerAPNSOnce.Do(func() {
		// The instance is kept rather than passed straight in, so that it can be
		// finalized when registration is refused.
		//
		// NewPushService starts the binary processor's pushMux goroutine at
		// construction. The manager rejects a duplicate name, so when another
		// test in this package has already registered "apns" this service
		// becomes unreachable -- and, if it were dropped here, would leak the
		// very goroutine TestBuildingAProviderLeaksNoGoroutines is watching for.
		service := NewPushService()
		if err := push.GetPushServiceManager().RegisterPushServiceType(service); err != nil {
			service.Finalize()
		}
	})
}

// newSimulatorPSP registers a provider pointed at the simulator.
//
// It verifies the simulator's certificate against the simulator's own CA rather
// than setting skipverify. That is deliberate: skipverify would leave the
// certificate-verification path -- the one production uses -- completely
// untested, and would also mean these tests still passed if createTLSConfig
// stopped verifying anything at all.
func newSimulatorPSP(t *testing.T, server *apnstest.Server, extra map[string]string) *push.PushServiceProvider {
	t.Helper()
	ensureAPNSRegistered()

	dir := t.TempDir()
	certPath, keyPath, err := apnstest.GenerateClientCert(dir)
	if err != nil {
		t.Fatalf("Could not generate a client certificate: %v", err)
	}
	caPath, err := server.WriteCACert(dir)
	if err != nil {
		t.Fatalf("Could not write the simulator's CA: %v", err)
	}

	kv := map[string]string{
		"pushservicetype": "apns",
		"service":         "conformance",
		"subscriber":      "conformance",
		"cert":            certPath,
		"key":             keyPath,
		"bundleid":        conformanceTopic,
		"endpoint":        server.URL(),
		"cacert":          caPath,
	}
	for key, value := range extra {
		kv[key] = value
	}

	psm := push.GetPushServiceManager()
	psp, err := psm.BuildPushServiceProviderFromMap(kv)
	if err != nil {
		t.Fatalf("Could not build a push service provider: %v", err)
	}
	return psp
}

// allowNonAppleEndpoints permits simulator endpoints for one test.
//
// Every test in this package points a provider at a local simulator, which is
// exactly the destination /addpsp refuses unless uniqush.conf opts in. That
// refusal is the feature -- see common.ErrNonAppleEndpoint -- so these tests
// enable it the way an operator running against a simulator would, rather than
// the package defaulting to permissive and leaving the guard untested.
func allowNonAppleEndpoints(t *testing.T) {
	t.Helper()
	previous := common.AllowsNonAppleEndpoints()
	common.SetAllowNonAppleEndpoints(true)
	t.Cleanup(func() { common.SetAllowNonAppleEndpoints(previous) })
}

func startSimulator(t *testing.T) *apnstest.Server {
	allowNonAppleEndpoints(t)
	t.Helper()
	server := apnstest.NewServer()
	server.RequireTopic(conformanceTopic)
	t.Cleanup(server.Close)
	return server
}

// pushToSimulator sends one notification to every token and returns the results
// uniqush produced synchronously.
func pushToSimulator(t *testing.T, service *pushService, psp *push.PushServiceProvider, notif *push.Notification, tokens ...string) []*push.Result {
	t.Helper()

	dpQueue := make(chan *push.DeliveryPoint, len(tokens))
	for _, token := range tokens {
		dp := push.NewEmptyDeliveryPoint()
		dp.FixedData["devtoken"] = token
		dp.FixedData["subscriber"] = "conformance"
		dpQueue <- dp
	}
	close(dpQueue)

	// Buffered past the number of tokens: Push writes every result before
	// returning, and an unbuffered channel would deadlock a synchronous caller.
	resQueue := make(chan *push.Result, len(tokens)+8)
	service.Push(psp, dpQueue, resQueue, notif)

	var results []*push.Result
	for res := range resQueue {
		results = append(results, res)
	}
	return results
}

// awaitAsyncError waits for one message on the service's error channel.
//
// APNs outcomes that arrive after uniqush has already answered its caller --
// an unsubscribe, most importantly -- are reported here rather than in the push
// results. Polling with a timeout rather than blocking forever, so a regression
// that stops reporting them fails the test instead of hanging it.
func awaitAsyncError(t *testing.T, errChan <-chan push.Error) push.Error {
	t.Helper()
	select {
	case err := <-errChan:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Timed out waiting for an asynchronous result from APNs")
		return nil
	}
}

// describeResults renders push results for a failure message.
//
// Not just "%v" on the slice. push.Result.Error() formats r.Destination.Name(),
// and a Result built from the error channel has no Destination -- which is how
// every APNs error response arrives. Printing one directly panics inside the
// formatter, so a genuine test failure reports "PANIC=Error method" instead of
// what went wrong, which is a memorably unhelpful way to spend an afternoon.
func describeResults(results []*push.Result) string {
	if len(results) == 0 {
		return "no results"
	}
	described := make([]string, 0, len(results))
	for i, result := range results {
		switch {
		case result == nil:
			described = append(described, fmt.Sprintf("[%d] <nil result>", i))
		case result.Err == nil:
			described = append(described, fmt.Sprintf("[%d] success (MsgID=%s)", i, result.MsgID))
		default:
			described = append(described, fmt.Sprintf("[%d] %T: %v", i, result.Err, result.Err))
		}
	}
	return strings.Join(described, "; ")
}

func assertNoViolations(t *testing.T, server *apnstest.Server) {
	t.Helper()
	for _, violation := range server.Violations() {
		t.Errorf("APNs conformance violation: %s", violation)
	}
}

func requireOneRequest(t *testing.T, server *apnstest.Server) apnstest.Request {
	t.Helper()
	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("Expected exactly one request to reach APNs, got %d", len(requests))
	}
	return requests[0]
}

// TestConformanceAlertPush is the baseline: an ordinary push, end to end, over
// a real HTTP/2 connection.
func TestConformanceAlertPush(t *testing.T) {
	server := startSimulator(t)
	service, errChan := newSimulatorService(t)
	psp := newSimulatorPSP(t, server, nil)

	token := apnstest.DeviceToken(0x11)
	results := pushToSimulator(t, service, psp, createNotification("Hello World"), token)

	if len(results) != 1 {
		t.Fatalf("Expected one result, got %d", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("Expected the push to succeed, got: %v", results[0].Err)
	}

	request := requireOneRequest(t, server)
	if request.Token != token {
		t.Errorf("Expected the token in the path to be %s, got %s", token, request.Token)
	}
	if request.Topic != conformanceTopic {
		t.Errorf("Expected apns-topic %s, got %q", conformanceTopic, request.Topic)
	}
	if request.PushType != common.PushTypeAlert {
		t.Errorf("Expected apns-push-type alert, got %q", request.PushType)
	}
	if request.Priority != common.PriorityImmediate {
		t.Errorf("Expected apns-priority 10 for an alert, got %q", request.Priority)
	}
	if request.APNSID == "" {
		t.Error("Expected uniqush to send an apns-id; without one a delivery cannot be traced afterwards")
	}
	// The expiry is a timestamp, not a duration. Sending a duration would make
	// every notification expire in 1970.
	expiration, err := strconv.ParseInt(request.Expiration, 10, 64)
	if err != nil {
		t.Fatalf("apns-expiration %q is not a number: %v", request.Expiration, err)
	}
	if expiration < time.Now().Unix() {
		t.Errorf("apns-expiration %d is in the past; it must be an absolute timestamp", expiration)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatalf("The payload is not JSON: %v (%s)", err, request.Payload)
	}
	if _, ok := payload["aps"]; !ok {
		t.Errorf("Expected an aps dictionary in the payload, got %s", request.Payload)
	}

	assertNoViolations(t, server)
	if len(errChan) > 0 {
		t.Errorf("Expected no asynchronous errors, got %d", len(errChan))
	}
}

// TestConformanceHeadersAreSentOnce is the regression test for the header bug
// the lowercase-literal comment in http_api/processor.go describes.
//
// http.Header.Set canonicalises "apns-topic" to "Apns-Topic". Setting it that
// way alongside a lowercase literal leaves two map entries that both serialise
// to the same HTTP/2 field, and APNs answers 400 DuplicateHeaders. It is an
// easy change to make while tidying, and impossible to notice without a server
// that counts.
func TestConformanceHeadersAreSentOnce(t *testing.T) {
	server := startSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newSimulatorPSP(t, server, nil)

	pushToSimulator(t, service, psp, createNotification("Hello World"), apnstest.DeviceToken(0x22))

	request := requireOneRequest(t, server)
	for _, name := range []string{"apns-topic", "apns-push-type", "apns-priority", "apns-expiration", "apns-id"} {
		if values := request.Header.Values(name); len(values) != 1 {
			t.Errorf("Expected %s exactly once, got %d occurrences: %v", name, len(values), values)
		}
	}
	assertNoViolations(t, server)
}

// TestConformanceBackgroundPushUsesPriority5 covers the rule APNs enforces with
// a 400: "Always use priority 5. Using priority 10 is an error."
//
// The simulator rejects the wrong combination, so this fails loudly rather than
// through a subtly missing notification.
func TestConformanceBackgroundPushUsesPriority5(t *testing.T) {
	server := startSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newSimulatorPSP(t, server, nil)

	notif := createNotification("Hello World")
	notif.Data[apnsPushTypeKey] = common.PushTypeBackground

	results := pushToSimulator(t, service, psp, notif, apnstest.DeviceToken(0x33))
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("Expected a background push to be accepted, got: %s", describeResults(results))
	}

	request := requireOneRequest(t, server)
	if request.PushType != common.PushTypeBackground {
		t.Errorf("Expected apns-push-type background, got %q", request.PushType)
	}
	if request.Priority != common.PriorityPowerAware {
		t.Errorf("Expected apns-priority 5 for a background push, got %q", request.Priority)
	}
	assertNoViolations(t, server)
}

// TestConformanceVoIPPush covers both spellings, since uniqush.apns_voip
// predates the push type key and is still honoured.
func TestConformanceVoIPPush(t *testing.T) {
	cases := map[string]map[string]string{
		"uniqush.apns_push_type": {apnsPushTypeKey: common.PushTypeVoIP},
		"uniqush.apns_voip":      {apnsVoIPKey: "1"},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			server := startSimulator(t)
			service, _ := newSimulatorService(t)
			psp := newSimulatorPSP(t, server, nil)

			notif := createNotification("Ring ring")
			for key, value := range data {
				notif.Data[key] = value
			}

			results := pushToSimulator(t, service, psp, notif, apnstest.DeviceToken(0x44))
			if len(results) != 1 || results[0].Err != nil {
				t.Fatalf("Expected the VoIP push to be accepted, got: %s", describeResults(results))
			}
			if request := requireOneRequest(t, server); request.PushType != common.PushTypeVoIP {
				t.Errorf("Expected apns-push-type voip, got %q", request.PushType)
			}
			assertNoViolations(t, server)
		})
	}
}

// TestConformanceEachTokenGetsItsOwnAPNSID guards the header clone in
// sendRequests.
//
// apns-id has to differ per device token. Sharing one http.Header across the
// fan-out would give every device the same id, which makes "did this push
// arrive?" unanswerable for all but one of them.
func TestConformanceEachTokenGetsItsOwnAPNSID(t *testing.T) {
	server := startSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newSimulatorPSP(t, server, nil)

	tokens := []string{apnstest.DeviceToken(0x51), apnstest.DeviceToken(0x52), apnstest.DeviceToken(0x53)}
	pushToSimulator(t, service, psp, createNotification("Hello World"), tokens...)

	requests := server.Requests()
	if len(requests) != len(tokens) {
		t.Fatalf("Expected %d requests, got %d", len(tokens), len(requests))
	}
	seen := make(map[string]string, len(requests))
	for _, request := range requests {
		if previous, duplicate := seen[request.APNSID]; duplicate {
			t.Errorf("Tokens %s and %s were sent with the same apns-id %s",
				previous, request.Token, request.APNSID)
		}
		seen[request.APNSID] = request.Token
	}
	assertNoViolations(t, server)
}

// TestConformanceUnregisteredUnsubscribes covers the 410 that means the app was
// uninstalled.
func TestConformanceUnregisteredUnsubscribes(t *testing.T) {
	cases := map[string]apnstest.Response{
		"410 with a reason and timestamp": {
			Status:    http.StatusGone,
			Reason:    apnstest.ReasonUnregistered,
			Timestamp: time.Now().Add(-time.Hour),
		},
		// Apple always sends a reason, but uniqush deliberately does not depend
		// on it: a 410 whose body is missing still means the token is dead, and
		// leaking the subscription would leave it undeliverable forever.
		"410 with no body at all": {
			Status:   http.StatusGone,
			OmitBody: true,
		},
		"400 BadDeviceToken": {
			Status: http.StatusBadRequest,
			Reason: apnstest.ReasonBadDeviceToken,
		},
		"400 DeviceTokenNotForTopic": {
			Status: http.StatusBadRequest,
			Reason: apnstest.ReasonDeviceTokenNotForTopic,
		},
		"410 ExpiredToken": {
			Status: http.StatusGone,
			Reason: apnstest.ReasonExpiredToken,
		},
	}

	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			server := startSimulator(t)
			service, errChan := newSimulatorService(t)
			psp := newSimulatorPSP(t, server, nil)

			token := apnstest.DeviceToken(0x66)
			server.SetResponse(token, response)

			pushToSimulator(t, service, psp, createNotification("Hello World"), token)

			err := awaitAsyncError(t, errChan)
			if _, ok := err.(*push.UnsubscribeUpdate); !ok {
				t.Errorf("Expected an UnsubscribeUpdate for a dead token, got %T: %v", err, err)
			}
			assertNoViolations(t, server)
		})
	}
}

// TestConformanceTransientFailuresAreRetried covers behaviour APNs never had.
//
// Every non-permanent reason used to become a BadNotification and stop there:
// a 503 from Apple, or a per-device rate limit, was reported as though the
// payload were malformed and the notification was dropped. FCM has mapped these
// to RetryError since its rewrite; APNs simply did not.
func TestConformanceTransientFailuresAreRetried(t *testing.T) {
	cases := map[string]apnstest.Response{
		"429 TooManyRequests": {Status: http.StatusTooManyRequests, Reason: apnstest.ReasonTooManyRequests},
		// Reached only when the previous bucket's token is unavailable to fall
		// back on; otherwise sendRequest recovers before this point.
		"429 TooManyProviderTokenUpdates": {
			Status: http.StatusTooManyRequests,
			Reason: apnstest.ReasonTooManyProviderTokenUpdates,
		},
		"500 InternalServerError": {Status: http.StatusInternalServerError, Reason: apnstest.ReasonInternalServerError},
		"503 ServiceUnavailable":  {Status: http.StatusServiceUnavailable, Reason: apnstest.ReasonServiceUnavailable},
	}

	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			server := startSimulator(t)
			service, _ := newSimulatorService(t)
			psp := newSimulatorPSP(t, server, nil)

			token := apnstest.DeviceToken(0xb8)
			server.SetResponse(token, response)

			results := pushToSimulator(t, service, psp, createNotification("Hello World"), token)
			if len(results) != 1 || results[0].Err == nil {
				t.Fatalf("Expected an error, got: %s", describeResults(results))
			}

			retry, isRetry := results[0].Err.(*push.RetryError)
			if !isRetry {
				t.Fatalf("Expected a RetryError for %s, got %T: %v", name, results[0].Err, results[0].Err)
			}
			// The backend can only re-send a push it has all three parts of.
			// Without them it drops the retry silently, which would look
			// identical to the bug this replaced.
			if retry.Provider == nil || retry.Destination == nil || retry.Content == nil {
				t.Errorf("RetryError is missing what the backend needs to re-send: provider=%v destination=%v content=%v",
					retry.Provider != nil, retry.Destination != nil, retry.Content != nil)
			}
			if retry.After <= 0 {
				t.Errorf("Expected a positive backoff, got %s", retry.After)
			}
		})
	}
}

// TestConformanceProviderFailuresAreReportedAgainstTheProvider covers the other
// half of the remapping.
//
// A wrong signing key, a wrong topic or a skewed clock are not properties of
// the notification, and calling them BadNotification sends whoever is debugging
// to look at the payload. These are the reasons an operator has to act on.
func TestConformanceProviderFailuresAreReportedAgainstTheProvider(t *testing.T) {
	cases := map[string]apnstest.Response{
		"403 InvalidProviderToken": {Status: http.StatusForbidden, Reason: apnstest.ReasonInvalidProviderToken},
		"403 MissingProviderToken": {Status: http.StatusForbidden, Reason: apnstest.ReasonMissingProviderToken},
		"403 ExpiredProviderToken": {Status: http.StatusForbidden, Reason: apnstest.ReasonExpiredProviderToken},
		"403 Forbidden":            {Status: http.StatusForbidden, Reason: apnstest.ReasonForbidden},
		// TooManyProviderTokenUpdates is deliberately absent: it is transient,
		// the floor always clears, and treating it as a provider failure would
		// have meant every push failing for up to 20 minutes. See
		// TestConformanceTransientFailuresAreRetried and the fallback in
		// sendRequest.
	}

	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			server := startSimulator(t)
			service, _ := newSimulatorService(t)
			psp := newSimulatorPSP(t, server, nil)

			token := apnstest.DeviceToken(0xb9)
			server.SetResponse(token, response)

			results := pushToSimulator(t, service, psp, createNotification("Hello World"), token)
			if len(results) != 1 || results[0].Err == nil {
				t.Fatalf("Expected an error, got: %s", describeResults(results))
			}
			if _, isProvider := results[0].Err.(*push.BadPushServiceProvider); !isProvider {
				t.Errorf("Expected a BadPushServiceProvider for %s, got %T: %v", name, results[0].Err, results[0].Err)
			}
			// And never an unsubscribe: none of these is the device's fault.
			if _, unsubscribed := results[0].Err.(*push.UnsubscribeUpdate); unsubscribed {
				t.Errorf("%s must not unsubscribe the device", name)
			}
		})
	}
}

// TestConformanceForbiddenDoesNotUnsubscribe is the deliberate deviation from
// Apple's own do-not-retry list, and the one most worth pinning down.
//
// Apple lists Forbidden and PayloadTooLarge alongside BadDeviceToken as
// responses not to retry. uniqush does not unsubscribe on them, because they
// describe a broken provider configuration or an oversized payload rather than
// a dead device -- and acting on them would delete working subscriptions across
// an entire service because of one bad push.
func TestConformanceForbiddenDoesNotUnsubscribe(t *testing.T) {
	cases := map[string]apnstest.Response{
		"403 Forbidden": {
			Status: http.StatusForbidden,
			Reason: apnstest.ReasonForbidden,
		},
		"413 PayloadTooLarge": {
			Status: http.StatusRequestEntityTooLarge,
			Reason: apnstest.ReasonPayloadTooLarge,
		},
		"429 TooManyRequests": {
			Status:     http.StatusTooManyRequests,
			Reason:     apnstest.ReasonTooManyRequests,
			RetryAfter: "30",
		},
		"500 InternalServerError": {
			Status: http.StatusInternalServerError,
			Reason: apnstest.ReasonInternalServerError,
		},
		"503 ServiceUnavailable": {
			Status: http.StatusServiceUnavailable,
			Reason: apnstest.ReasonServiceUnavailable,
		},
	}

	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			server := startSimulator(t)
			service, errChan := newSimulatorService(t)
			psp := newSimulatorPSP(t, server, nil)

			token := apnstest.DeviceToken(0x77)
			server.SetResponse(token, response)

			results := pushToSimulator(t, service, psp, createNotification("Hello World"), token)

			var reported bool
			for _, result := range results {
				if result.Err == nil {
					continue
				}
				reported = true
				if _, unsubscribed := result.Err.(*push.UnsubscribeUpdate); unsubscribed {
					t.Errorf("%s must not unsubscribe the device: %v", name, result.Err)
				}
				if !strings.Contains(result.Err.Error(), response.Reason) {
					t.Errorf("Expected the error to carry the reason %q, got: %v", response.Reason, result.Err)
				}
			}
			if !reported {
				t.Errorf("Expected %s to be reported as an error", name)
			}

			// Nothing should arrive asynchronously either, since none of these
			// reaches the result channel.
			select {
			case err := <-errChan:
				if _, unsubscribed := err.(*push.UnsubscribeUpdate); unsubscribed {
					t.Errorf("%s produced an asynchronous unsubscribe: %v", name, err)
				}
			case <-time.After(250 * time.Millisecond):
			}
			assertNoViolations(t, server)
		})
	}
}

// TestConformanceRejectsAnUntrustedCertificate proves the CA is actually
// enforced.
//
// Without this, every test above would still pass if createTLSConfig quietly
// stopped verifying anything -- they all connect to a server whose certificate
// is self-signed, so "it worked" and "nothing was checked" look identical. Here
// the provider trusts a CA that did not issue the simulator's certificate, and
// the connection has to fail.
func TestConformanceRejectsAnUntrustedCertificate(t *testing.T) {
	server := startSimulator(t)
	service, _ := newSimulatorService(t)

	// A second simulator's CA would be the obvious choice for "the wrong CA"
	// and is not: httptest serves one built-in certificate for every TLS server
	// it starts, so two simulators present the identical certificate and the
	// wrong CA verifies fine. An unrelated self-signed certificate is genuinely
	// a different issuer.
	wrongCA, _, err := apnstest.GenerateClientCert(t.TempDir())
	if err != nil {
		t.Fatalf("Could not generate an unrelated certificate: %v", err)
	}
	psp := newSimulatorPSP(t, server, map[string]string{"cacert": wrongCA})

	results := pushToSimulator(t, service, psp, createNotification("Hello World"), apnstest.DeviceToken(0x88))

	var failed bool
	for _, result := range results {
		if result.Err != nil {
			failed = true
		}
	}
	if !failed {
		t.Error("Expected the push to fail against a certificate signed by an untrusted CA")
	}
	if requests := server.Requests(); len(requests) != 0 {
		t.Errorf("Expected no request to reach the server, got %d", len(requests))
	}
}

// buildAPNSProvider registers a provider through the manager, as /addpsp does.
//
// The point of going through the manager rather than calling ValidateEndpoint
// directly is that /addpsp is the only door an operator comes through, and a
// rule enforced in a helper nothing calls is not enforced at all.
func buildAPNSProvider(t *testing.T, kv map[string]string) (*push.PushServiceProvider, error) {
	t.Helper()
	ensureAPNSRegistered()

	dir := t.TempDir()
	certPath, keyPath, err := apnstest.GenerateClientCert(dir)
	if err != nil {
		t.Fatalf("Could not generate a client certificate: %v", err)
	}
	full := map[string]string{
		"pushservicetype": "apns",
		"service":         "endpointvalidation",
		"subscriber":      "conformance",
		"cert":            certPath,
		"key":             keyPath,
		"bundleid":        conformanceTopic,
	}
	for key, value := range kv {
		if value == "" {
			delete(full, key)
			continue
		}
		full[key] = value
	}
	return push.GetPushServiceManager().BuildPushServiceProviderFromMap(full)
}

// TestSkipVerifyIsRefusedForAppleThroughAddpsp checks the guard on the path an
// operator actually uses.
//
// common.ValidateEndpoint has its own unit test, but a validator is only worth
// anything if it is wired into the request that reaches it. This is the same
// rule observed from outside: /addpsp must refuse to disable certificate
// verification against Apple, because that is a setting people copy from a
// testing recipe and never look at again.
func TestSkipVerifyIsRefusedForAppleThroughAddpsp(t *testing.T) {
	allowNonAppleEndpoints(t)
	for _, host := range []string{common.HostProduction, common.HostDevelopment} {
		t.Run(host, func(t *testing.T) {
			_, err := buildAPNSProvider(t, map[string]string{
				"endpoint":   host,
				"skipverify": "true",
			})
			if err == nil {
				t.Fatalf("Expected /addpsp to refuse skipverify against %s", host)
			}
			if !strings.Contains(err.Error(), "skipverify") {
				t.Errorf("Expected the error to name the setting, got: %v", err)
			}
		})
	}

	// Still allowed where it is the point, or the simulator could not be used.
	if _, err := buildAPNSProvider(t, map[string]string{
		"endpoint":   "https://localhost:8443",
		"skipverify": "true",
	}); err != nil {
		t.Errorf("Expected skipverify to be allowed for a local endpoint, got: %v", err)
	}
}

// buildIntoExistingProvider calls the APNs builder with a provider that already
// carries settings, which is the only way to reach the clearing behaviour.
//
// Note what this does *not* claim. The push service manager hands the builder a
// NewEmptyPushServiceProvider every time, and the database load path
// unserializes without calling the builder at all, so no stale value reaches
// here through /addpsp today. This pins the builder's own contract -- absent
// means cleared, as its comment says and as bundleid has always behaved -- so
// that it stays true if a caller ever does reuse a provider. Going through the
// manager instead would assert nothing: a fresh provider has no old value to
// keep, and the test would pass against either implementation.
func buildIntoExistingProvider(t *testing.T, existing, kv map[string]string) *push.PushServiceProvider {
	t.Helper()

	dir := t.TempDir()
	certPath, keyPath, err := apnstest.GenerateClientCert(dir)
	if err != nil {
		t.Fatalf("Could not generate a client certificate: %v", err)
	}

	psp := push.NewEmptyPushServiceProvider()
	for key, value := range existing {
		psp.VolatileData[key] = value
	}

	full := map[string]string{
		"service":  "endpointvalidation",
		"cert":     certPath,
		"key":      keyPath,
		"bundleid": conformanceTopic,
	}
	for key, value := range kv {
		full[key] = value
	}

	// Finalized even though this helper only builds a provider and never sends
	// anything. NewPushService constructs the binary processor too, and that
	// starts a pushMux goroutine at construction rather than on first use, so a
	// service that is built and dropped leaks it for the life of the test
	// binary. Cheap to get right, and invisible until something counts
	// goroutines.
	service := NewPushService().(*pushService)
	defer service.Finalize()

	if err := service.BuildPushServiceProviderFromMap(full, psp); err != nil {
		t.Fatalf("Could not build the provider: %v", err)
	}
	return psp
}

// TestBuilderRecordsACredentialRevision pins the half of the rotation fix that
// lives in the builder.
//
// The push path decides whether a cached TLS client is still the right one by
// comparing this value, and it deliberately does not open the credential files
// itself -- doing so would put three synchronous reads on the fast path of every
// push, for every provider, to learn something that can only change here.
//
// That trade is only sound if the builder actually records it, and the tests in
// srv/apns/http_api go through a mock push service type, so they cannot see the
// real builder at all. This is the test that does.
func TestBuilderRecordsACredentialRevision(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, err := apnstest.GenerateClientCert(dir)
	if err != nil {
		t.Fatalf("Could not generate a client certificate: %v", err)
	}

	// Through the manager, so the provider carries its push service type and
	// Name() works. The registered type is the real APNs service in this
	// package, so this is the production builder.
	ensureAPNSRegistered()
	build := func() *push.PushServiceProvider {
		t.Helper()
		psp, buildErr := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
			"pushservicetype": "apns",
			"service":         "revisions",
			"cert":            certPath,
			"key":             keyPath,
			"bundleid":        conformanceTopic,
		})
		if buildErr != nil {
			t.Fatalf("Could not build the provider: %v", buildErr)
		}
		return psp
	}

	before := build()
	revision := before.VolatileData[common.CredentialRevisionKey]
	if revision == "" {
		t.Fatal("The builder recorded no credential revision, so the push path has nothing to " +
			"compare and a rotated certificate would never take effect")
	}

	// Rebuilding an unchanged provider must give the same answer, or every
	// /addpsp would pointlessly discard a working client.
	if again := build().VolatileData[common.CredentialRevisionKey]; again != revision {
		t.Error("Rebuilding an unchanged provider produced a different credential revision")
	}

	// The annual renewal: a genuinely different, genuinely loadable pair written
	// over the same paths. Generated rather than faked, because the builder
	// validates the pair and nonsense would fail the build instead.
	newCert, newKey, err := apnstest.GenerateClientCert(t.TempDir())
	if err != nil {
		t.Fatalf("Could not generate a replacement certificate: %v", err)
	}
	for _, pair := range [][2]string{{newCert, certPath}, {newKey, keyPath}} {
		contents, readErr := os.ReadFile(pair[0])
		if readErr != nil {
			t.Fatalf("Could not read %s: %v", pair[0], readErr)
		}
		if writeErr := os.WriteFile(pair[1], contents, 0o600); writeErr != nil {
			t.Fatalf("Could not install the renewed credential: %v", writeErr)
		}
	}

	after := build()
	if after.VolatileData[common.CredentialRevisionKey] == revision {
		t.Error("Renewing the certificate in place did not change the credential revision.\n" +
			"Every path is unchanged and so is psp.Name(), so this value is the only thing that " +
			"can tell the push path to stop using the retired certificate.")
	}
	// The identity must not move: delivery points are stored against the name.
	if after.Name() != before.Name() {
		t.Error("Renewing a certificate changed the provider's name, which would strand every " +
			"delivery point stored against the old one")
	}
}

// TestEndpointAndCACertAreClearedWhenAbsent is the other half of making them
// settable.
//
// A provider that can be pointed at a simulator but never pointed back would be
// a trap, and the doc comment on buildHTTP2Destination promises the opposite:
// that absent settings fall back to the addr-inferred host and the system roots.
func TestEndpointAndCACertAreClearedWhenAbsent(t *testing.T) {
	allowNonAppleEndpoints(t)
	psp := buildIntoExistingProvider(t, map[string]string{
		common.EndpointKey: "https://localhost:8443",
		common.CACertKey:   "/etc/uniqush/simulator-ca.pem",
	}, nil)

	if value, present := psp.VolatileData[common.EndpointKey]; present {
		t.Errorf("Expected the endpoint to be cleared, got %q", value)
	}
	if value, present := psp.VolatileData[common.CACertKey]; present {
		t.Errorf("Expected the CA bundle to be cleared, got %q", value)
	}
	if got := common.ResolveEndpoint(psp); got != common.HostProduction {
		t.Errorf("Expected the destination to fall back to %s, got %s", common.HostProduction, got)
	}
}

// TestSkipVerifyIsClearedWhenAbsent is the same rule for the setting where it
// matters most.
//
// Certificate verification that could be turned off but not back on would leave
// an operator who followed a testing recipe with no way to restore it short of
// deleting the provider -- and with it, every subscription in the service.
func TestSkipVerifyIsClearedWhenAbsent(t *testing.T) {
	allowNonAppleEndpoints(t)
	psp := buildIntoExistingProvider(t, map[string]string{
		common.SkipVerifyKey: "true",
	}, nil)

	if value, present := psp.VolatileData[common.SkipVerifyKey]; present {
		t.Errorf("Expected skipverify to be cleared, got %q; "+
			"verification could otherwise be disabled but never restored", value)
	}

	// And it is still recorded when asked for, or the simulator tests could not
	// reach a self-signed endpoint.
	psp = buildIntoExistingProvider(t, nil, map[string]string{
		common.EndpointKey:   "https://localhost:8443",
		common.SkipVerifyKey: "true",
	})
	if psp.VolatileData[common.SkipVerifyKey] != "true" {
		t.Error("Expected skipverify to be recorded when supplied")
	}
}

// TestConformanceEndpointIsHonoured is the test for the change that made all of
// the above possible.
//
// Before it, the HTTP/2 destination was chosen by string-matching the binary
// protocol's addr against "sandbox", so every push went to one of Apple's two
// hosts and the repaired code path could not be exercised at all.
func TestConformanceEndpointIsHonoured(t *testing.T) {
	server := startSimulator(t)
	service, _ := newSimulatorService(t)

	// addr says production. The endpoint has to win, or this push leaves the
	// building and heads for Apple.
	psp := newSimulatorPSP(t, server, map[string]string{"addr": "gateway.push.apple.com:2195"})

	results := pushToSimulator(t, service, psp, createNotification("Hello World"), apnstest.DeviceToken(0x99))
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("Expected the push to reach the simulator, got: %s", describeResults(results))
	}
	if len(server.Requests()) != 1 {
		t.Error("The push did not reach the simulator")
	}
	assertNoViolations(t, server)
}

// TestConformanceOversizedPayloadIsRejectedLocally checks uniqush enforces the
// 4096-byte limit itself.
//
// Apple would answer 413, but finding out costs a round trip and the error is
// less specific. The simulator would also report it, so this asserts the
// request never leaves.
func TestConformanceOversizedPayloadIsRejectedLocally(t *testing.T) {
	server := startSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newSimulatorPSP(t, server, nil)

	notif := createNotification(strings.Repeat("x", 5000))
	results := pushToSimulator(t, service, psp, notif, apnstest.DeviceToken(0xaa))

	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("Expected an oversized payload to be rejected, got: %s", describeResults(results))
	}
	if !strings.Contains(results[0].Err.Error(), "too large") {
		t.Errorf("Expected the error to say the payload is too large, got: %v", results[0].Err)
	}
	if requests := server.Requests(); len(requests) != 0 {
		t.Errorf("Expected the oversized push not to be sent, but %d reached the server", len(requests))
	}
}

// TestAddpspRefusesANonAppleEndpointByDefault is the end-to-end half of the
// policy: the unit test in srv/apns/common covers the rule, this covers the
// door it is fitted to.
//
// Registration is where the mistake is made and where it can still be refused
// cheaply. Once a provider is stored, every push for that service goes to
// whatever host it names -- carrying device tokens and payload, and presenting
// the client certificate on the way -- and the only remaining defence is the
// re-check on the push path.
func TestAddpspRefusesANonAppleEndpointByDefault(t *testing.T) {
	ensureAPNSRegistered()

	if common.AllowsNonAppleEndpoints() {
		t.Fatal("Expected non-Apple endpoints to be disabled by default")
	}

	_, err := buildAPNSProvider(t, map[string]string{
		"endpoint": "https://apns.attacker.example",
	})
	if err == nil {
		t.Fatal("Expected /addpsp to refuse an endpoint that is not Apple's")
	}
	if !strings.Contains(err.Error(), "allow_non_apple_endpoints") {
		t.Errorf("Expected the refusal to name the setting that permits it, got: %v", err)
	}
}
