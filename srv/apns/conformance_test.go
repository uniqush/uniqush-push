package apns

import (
	"encoding/json"
	"net/http"
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

// newSimulatorPSP registers a provider pointed at the simulator.
//
// It verifies the simulator's certificate against the simulator's own CA rather
// than setting skipverify. That is deliberate: skipverify would leave the
// certificate-verification path -- the one production uses -- completely
// untested, and would also mean these tests still passed if createTLSConfig
// stopped verifying anything at all.
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
		//nolint:errcheck // a registration from another test in this package is fine
		push.GetPushServiceManager().RegisterPushServiceType(NewPushService())
	})
}

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

func startSimulator(t *testing.T) *apnstest.Server {
	t.Helper()
	server, err := apnstest.NewServer()
	if err != nil {
		t.Fatalf("Could not start the APNs simulator: %v", err)
	}
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
		t.Fatalf("Expected a background push to be accepted, got: %v", results)
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
				t.Fatalf("Expected the VoIP push to be accepted, got: %v", results)
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

	service := NewPushService().(*pushService)
	if err := service.BuildPushServiceProviderFromMap(full, psp); err != nil {
		t.Fatalf("Could not build the provider: %v", err)
	}
	return psp
}

// TestEndpointAndCACertAreClearedWhenAbsent is the other half of making them
// settable.
//
// A provider that can be pointed at a simulator but never pointed back would be
// a trap, and the doc comment on buildHTTP2Destination promises the opposite:
// that absent settings fall back to the addr-inferred host and the system roots.
func TestEndpointAndCACertAreClearedWhenAbsent(t *testing.T) {
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
		t.Fatalf("Expected the push to reach the simulator, got: %v", results)
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
		t.Fatalf("Expected an oversized payload to be rejected, got: %v", results)
	}
	if !strings.Contains(results[0].Err.Error(), "too large") {
		t.Errorf("Expected the error to say the payload is too large, got: %v", results[0].Err)
	}
	if requests := server.Requests(); len(requests) != 0 {
		t.Errorf("Expected the oversized push not to be sent, but %d reached the server", len(requests))
	}
}
