package apns

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/apnstest"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// End-to-end tests for token (.p8) authentication, against a simulator that
// verifies the JWT the way Apple does.
//
// The interesting part of token auth is not that a signed request is accepted
// once -- it is the refresh schedule, which has to stay inside Apple's one-hour
// expiry and outside its one-token-per-20-minutes mint limit. Both edges fail
// as a 4xx on every push, so the failure mode is an outage rather than a
// degradation, and neither is reachable in real time. The simulator's clock
// makes them reachable.

const (
	tokenTestKeyID  = "KEYID12345"
	tokenTestTeamID = "TEAMID6789"
)

// newTokenSimulator starts a simulator that demands a provider token.
func newTokenSimulator(t *testing.T) (*apnstest.Server, *apnstest.SigningKey) {
	t.Helper()

	server := startSimulator(t)
	key, err := apnstest.GenerateSigningKey(t.TempDir(), tokenTestKeyID, tokenTestTeamID)
	if err != nil {
		t.Fatalf("Could not generate a signing key: %v", err)
	}
	server.RequireToken(key)
	return server, key
}

// newTokenPSP registers a provider authenticating with a signing key.
//
// Note the absence of cert and key: a provider using token authentication has
// no certificate, which is the entire reason the feature exists.
func newTokenPSP(t *testing.T, server *apnstest.Server, key *apnstest.SigningKey) *push.PushServiceProvider {
	t.Helper()
	ensureAPNSRegistered()

	caPath, err := server.WriteCACert(t.TempDir())
	if err != nil {
		t.Fatalf("Could not write the simulator's CA: %v", err)
	}

	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"pushservicetype": "apns",
		"service":         "tokenconformance",
		"subscriber":      "conformance",
		"authkey":         key.Path,
		"keyid":           key.KeyID,
		"teamid":          key.TeamID,
		"bundleid":        conformanceTopic,
		"endpoint":        server.URL(),
		"cacert":          caPath,
	})
	if err != nil {
		t.Fatalf("Could not build a token-auth provider: %v", err)
	}
	return psp
}

// setProcessorClock points the push service's HTTP/2 processor at a fake clock,
// and returns a function to move it.
func setProcessorClock(service *pushService, start time.Time) func(time.Duration) {
	current := start
	processor := service.httpRequestProcessor.(interface{ SetClock(func() time.Time) })
	processor.SetClock(func() time.Time { return current })
	return func(advance time.Duration) { current = current.Add(advance) }
}

// TestConformanceTokenAuthPush is the baseline: a push authenticated with a
// signing key, verified by the simulator against the public half.
func TestConformanceTokenAuthPush(t *testing.T) {
	server, key := newTokenSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newTokenPSP(t, server, key)

	results := pushToSimulator(t, service, psp, createNotification("Hello World"), apnstest.DeviceToken(0xb1))
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("Expected the push to be accepted, got: %s", describeResults(results))
	}

	request := requireOneRequest(t, server)
	authorization := request.Header.Get("authorization")
	if authorization == "" {
		t.Fatal("Expected an authorization header on a token-authenticated push")
	}
	if !strings.HasPrefix(authorization, "bearer ") {
		t.Errorf("Expected a bearer token, got %q", authorization)
	}
	assertNoViolations(t, server)
}

// TestConformanceTokenAuthSendsNoClientCertificate checks the other half of the
// change: a token-auth provider has no certificate to present, so the TLS
// config must not try to load one.
func TestConformanceTokenAuthSendsNoClientCertificate(t *testing.T) {
	server, key := newTokenSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newTokenPSP(t, server, key)

	if psp.FixedData["cert"] != "" || psp.FixedData["key"] != "" {
		t.Errorf("Expected no certificate in fixed data, got cert=%q key=%q",
			psp.FixedData["cert"], psp.FixedData["key"])
	}

	results := pushToSimulator(t, service, psp, createNotification("Hello World"), apnstest.DeviceToken(0xb2))
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("Expected the push to succeed without a client certificate, got: %s", describeResults(results))
	}
}

// TestConformanceTokenIsReusedAcrossPushes is the rate-limit test.
//
// Apple permits one new token per 20 minutes per key, counting tokens rather
// than requests, so a provider that signs a fresh JWT per push stops working as
// soon as it is busy. Every push here succeeds either way; only the token count
// distinguishes correct from broken.
func TestConformanceTokenIsReusedAcrossPushes(t *testing.T) {
	server, key := newTokenSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newTokenPSP(t, server, key)

	start := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	advance := setProcessorClock(service, start)
	server.SetClock(func() time.Time { return start })

	for i := 0; i < 5; i++ {
		results := pushToSimulator(t, service, psp, createNotification("Hello World"), apnstest.DeviceToken(byte(0xc0+i)))
		if len(results) != 1 || results[0].Err != nil {
			t.Fatalf("Push %d failed: %s", i, describeResults(results))
		}
		advance(time.Minute)
	}

	if tokens := server.IssuedTokens(); len(tokens) != 1 {
		t.Errorf("Expected one provider token to serve five pushes, got %d. "+
			"Apple's mint limit is per key and counts tokens, not requests", len(tokens))
	}
	assertNoViolations(t, server)
}

// TestConformanceTokenIsRefreshedBeforeItExpires is the other edge.
//
// A token older than an hour is rejected with ExpiredProviderToken, so reuse
// cannot be unconditional. The simulator enforces the expiry against its own
// clock, so this fails if the refresh interval is ever widened past it.
func TestConformanceTokenIsRefreshedBeforeItExpires(t *testing.T) {
	server, key := newTokenSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newTokenPSP(t, server, key)

	current := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	advance := setProcessorClock(service, current)
	server.SetClock(func() time.Time { return current })
	moveBoth := func(d time.Duration) {
		current = current.Add(d)
		advance(d)
	}

	results := pushToSimulator(t, service, psp, createNotification("Hello World"), apnstest.DeviceToken(0xd1))
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("The first push failed: %s", describeResults(results))
	}

	// Past the refresh interval and still well inside the expiry: uniqush
	// should have re-signed, and the simulator should accept the new token
	// because it is far enough from the last mint.
	moveBoth(50 * time.Minute)

	results = pushToSimulator(t, service, psp, createNotification("Hello again"), apnstest.DeviceToken(0xd2))
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("The push after the refresh interval failed: %s", describeResults(results))
	}

	if tokens := server.IssuedTokens(); len(tokens) != 2 {
		t.Errorf("Expected the token to be refreshed after %s, saw %d distinct tokens",
			50*time.Minute, len(tokens))
	}
	assertNoViolations(t, server)
}

// TestConformanceMintFloorIsMeasuredOnArrival guards the simulator against a
// client that could talk its way out of the rate limit.
//
// iat is chosen by the sender. A provider that re-signed for every push could
// space its claims twenty minutes apart while sending them seconds apart, and a
// simulator comparing iat values would wave that through -- reporting a pass for
// exactly the behaviour Apple answers with TooManyProviderTokenUpdates, since
// Apple can only observe arrivals. So the floor is measured against the server's
// own clock, and this drives uniqush's clock forward while holding the
// simulator's still to prove it.
func TestConformanceMintFloorIsMeasuredOnArrival(t *testing.T) {
	server, key := newTokenSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newTokenPSP(t, server, key)

	start := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	advance := setProcessorClock(service, start)
	// The simulator's clock never moves: every token arrives at the same instant
	// as far as it is concerned.
	server.SetClock(func() time.Time { return start })

	results := pushToSimulator(t, service, psp, createNotification("first"), apnstest.DeviceToken(0xa1))
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("The first push failed: %s", describeResults(results))
	}

	// Past uniqush's refresh interval, so it signs a new token whose iat is 50
	// minutes after the first -- comfortably outside Apple's 20-minute floor, if
	// you believe the claim. It arrives immediately after the previous one.
	advance(50 * time.Minute)

	results = pushToSimulator(t, service, psp, createNotification("second"), apnstest.DeviceToken(0xa2))
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("Expected a second token arriving immediately after the first to be refused, got: %s",
			describeResults(results))
	}
	if !strings.Contains(results[0].Err.Error(), apnstest.ReasonTooManyProviderTokenUpdates) {
		t.Errorf("Expected TooManyProviderTokenUpdates, got: %v", results[0].Err)
	}

	// Reported against the provider, not the notification.
	//
	// Minting too fast is a property of the key's schedule and affects every
	// push using that key; the payload is irrelevant and unchanged. Falling
	// through to BadNotification told the caller their message was bad, which is
	// both wrong and actively misleading -- it sends an operator to inspect a
	// payload that was never the problem.
	if _, isProviderError := results[0].Err.(*push.BadPushServiceProvider); !isProviderError {
		t.Errorf("Expected a BadPushServiceProvider for TooManyProviderTokenUpdates, got %T: %v",
			results[0].Err, results[0].Err)
	}
	if _, unsubscribed := results[0].Err.(*push.UnsubscribeUpdate); unsubscribed {
		t.Error("A mint-floor refusal must not unsubscribe the device")
	}
}

// TestConformanceExpiredTokenIsRejected proves the simulator's expiry check is
// real, so the test above is not passing vacuously.
//
// uniqush's clock is frozen while the simulator's advances past an hour, which
// is exactly the situation a too-wide refresh interval would produce.
func TestConformanceExpiredTokenIsRejected(t *testing.T) {
	server, key := newTokenSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newTokenPSP(t, server, key)

	start := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	setProcessorClock(service, start) // never advances: uniqush keeps re-sending one token
	serverNow := start
	server.SetClock(func() time.Time { return serverNow })

	if results := pushToSimulator(t, service, psp, createNotification("Hello"), apnstest.DeviceToken(0xe1)); results[0].Err != nil {
		t.Fatalf("The first push failed: %s", describeResults(results))
	}

	serverNow = start.Add(90 * time.Minute)

	results := pushToSimulator(t, service, psp, createNotification("Hello"), apnstest.DeviceToken(0xe2))
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("Expected a stale provider token to be rejected, got: %s", describeResults(results))
	}
	if !strings.Contains(results[0].Err.Error(), apnstest.ReasonExpiredProviderToken) {
		t.Errorf("Expected ExpiredProviderToken, got: %v", results[0].Err)
	}
	// A stale credential is a provider problem, and has to be reported as one.
	//
	// The weaker assertion this replaces only checked that the error was not an
	// unsubscribe, which passed while the handler was still returning
	// BadNotification -- an error about the message body, for a failure that has
	// nothing to do with the message. Every push for this provider fails until
	// an operator intervenes, and BadNotification suggests the opposite: that
	// some other notification might get through.
	//
	// Unsubscribing would be worse. A rejected provider token says nothing about
	// any particular device, so treating it as a dead token would delete every
	// subscription in the service because a clock had drifted. Asserting
	// BadPushServiceProvider implies that, but it is kept explicit below because
	// it is the more expensive mistake.
	if _, isProviderError := results[0].Err.(*push.BadPushServiceProvider); !isProviderError {
		t.Errorf("Expected a BadPushServiceProvider for an expired provider token, got %T: %v",
			results[0].Err, results[0].Err)
	}
	if _, unsubscribed := results[0].Err.(*push.UnsubscribeUpdate); unsubscribed {
		t.Error("An expired provider token must not unsubscribe the device")
	}
}

// TestConformanceForbiddenIsAProviderFailure closes a disagreement between two
// halves of this repository.
//
// TestLiveForbiddenIsNotTreatedAsADeadToken already asserts, against Apple's
// real response, that Forbidden must not unsubscribe the device: it means the
// credentials are wrong, and acting on it would delete every subscription in a
// service over one misconfiguration.
//
// But Forbidden was missing from the classification map, so it fell through to
// BadNotification. uniqush knew it was not a dead token and still told the
// caller their payload was bad -- the one message guaranteed to send an operator
// to the wrong place, since every push from that provider is failing and none of
// them are about the payload.
func TestConformanceForbiddenIsAProviderFailure(t *testing.T) {
	server := startSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newSimulatorPSP(t, server, nil)

	deviceToken := apnstest.DeviceToken(0xf9)
	server.SetResponse(deviceToken, apnstest.Response{
		Status: http.StatusForbidden,
		Reason: "Forbidden",
	})

	results := pushToSimulator(t, service, psp, createNotification("Hello"), deviceToken)
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("Expected Forbidden to be reported, got: %s", describeResults(results))
	}
	if _, isProviderError := results[0].Err.(*push.BadPushServiceProvider); !isProviderError {
		t.Errorf("Expected a BadPushServiceProvider for Forbidden, got %T: %v",
			results[0].Err, results[0].Err)
	}
	if _, unsubscribed := results[0].Err.(*push.UnsubscribeUpdate); unsubscribed {
		t.Error("Forbidden must not unsubscribe the device: it is a credential problem, and " +
			"acting on it would delete every subscription in the service")
	}
}

// TestConformanceInvalidProviderTokenIsAProviderFailure covers the rejection an
// operator is most likely to meet, since a mistyped keyid or teamid is a setup
// mistake rather than a runtime one.
//
// Worth asserting separately from the expiry case because it arrives with a
// different reason code, and a misreported error here sends someone looking at
// their payload for a problem that is in their credentials.
func TestConformanceInvalidProviderTokenIsAProviderFailure(t *testing.T) {
	server, key := newTokenSimulator(t)
	service, _ := newSimulatorService(t)

	psp := newTokenPSP(t, server, key)
	// A key id the simulator does not know, which is what Apple sees when the
	// kid header does not match any key registered to the team.
	psp.VolatileData[common.KeyIDKey] = "WRONGKEYID"

	results := pushToSimulator(t, service, psp, createNotification("Hello"), apnstest.DeviceToken(0xf1))
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("Expected a token with an unrecognised key id to be rejected, got: %s",
			describeResults(results))
	}
	if _, isProviderError := results[0].Err.(*push.BadPushServiceProvider); !isProviderError {
		t.Errorf("Expected a BadPushServiceProvider for an unrecognised key id, got %T: %v",
			results[0].Err, results[0].Err)
	}
	if _, unsubscribed := results[0].Err.(*push.UnsubscribeUpdate); unsubscribed {
		t.Error("An unrecognised signing key must not unsubscribe the device")
	}
}

// TestConformanceMissingTokenIsRejected checks the simulator notices an absent
// authorization header, which is what makes the tests above meaningful: without
// it they would pass even if uniqush sent no token at all.
func TestConformanceMissingTokenIsRejected(t *testing.T) {
	server, _ := newTokenSimulator(t)
	service, _ := newSimulatorService(t)

	// A certificate-based provider against a team that expects tokens: uniqush
	// sends no authorization header, and Apple answers MissingProviderToken.
	psp := newSimulatorPSP(t, server, nil)

	results := pushToSimulator(t, service, psp, createNotification("Hello"), apnstest.DeviceToken(0xf1))
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("Expected a push with no provider token to be rejected, got: %s", describeResults(results))
	}
	if !strings.Contains(results[0].Err.Error(), apnstest.ReasonMissingProviderToken) {
		t.Errorf("Expected MissingProviderToken, got: %v", results[0].Err)
	}
}

// TestTokenAuthProviderShape documents the FixedData decision in a test, since
// it is the part of this change that could silently unsubscribe devices.
//
// A provider's name hashes its FixedData, and a delivery point is stored
// against that name. Adding any of the token settings to FixedData would mean a
// key rotation produced a new provider name, and the old provider's
// disappearance deletes every delivery point bound to it.
func TestTokenAuthProviderShape(t *testing.T) {
	server, key := newTokenSimulator(t)
	psp := newTokenPSP(t, server, key)

	if len(psp.FixedData) != 1 || psp.FixedData["service"] == "" {
		t.Errorf("Expected only the service name to be fixed, got %v", psp.FixedData)
	}
	for _, field := range []string{common.AuthKeyKey, common.KeyIDKey, common.TeamIDKey} {
		if psp.FixedData[field] != "" {
			t.Errorf("%s is in FixedData; rotating it would strand every subscription", field)
		}
		if psp.VolatileData[field] == "" {
			t.Errorf("Expected %s in VolatileData, got nothing", field)
		}
	}
}

// TestTokenAuthRejectsIncompleteConfiguration covers the mistakes an operator
// makes once each.
//
// All of them produce the same 403 InvalidProviderToken from Apple, with
// nothing to say which field was wrong, so /addpsp is much the better place to
// find out.
func TestTokenAuthRejectsIncompleteConfiguration(t *testing.T) {
	server, key := newTokenSimulator(t)
	ensureAPNSRegistered()

	base := func() map[string]string {
		return map[string]string{
			"pushservicetype": "apns",
			"service":         "tokenvalidation",
			"subscriber":      "conformance",
			"authkey":         key.Path,
			"keyid":           key.KeyID,
			"teamid":          key.TeamID,
			"bundleid":        conformanceTopic,
			"endpoint":        server.URL(),
		}
	}

	cases := map[string]func(map[string]string){
		"no keyid":              func(kv map[string]string) { delete(kv, "keyid") },
		"no teamid":             func(kv map[string]string) { delete(kv, "teamid") },
		"short keyid":           func(kv map[string]string) { kv["keyid"] = "TOOSHORT" },
		"short teamid":          func(kv map[string]string) { kv["teamid"] = "TOOSHORT" },
		"unreadable authkey":    func(kv map[string]string) { kv["authkey"] = "/nonexistent/AuthKey.p8" },
		"both auth mechanisms":  func(kv map[string]string) { kv["cert"] = "cert.pem"; kv["key"] = "key.pem" },
		"keyid without authkey": func(kv map[string]string) { delete(kv, "authkey") },
	}

	// And the case that must NOT be rejected. A form post carries every field
	// the page rendered, so empty cert and key arriving alongside a complete
	// token configuration is what an ordinary HTML form looks like -- not an
	// operator configuring two mechanisms.
	t.Run("empty cert and key alongside a signing key are accepted", func(t *testing.T) {
		kv := base()
		kv["cert"] = ""
		kv["key"] = "   "
		if _, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(kv); err != nil {
			t.Errorf("Expected empty cert/key to be treated as absent, got: %v", err)
		}
	})

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			kv := base()
			mutate(kv)
			if _, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(kv); err == nil {
				t.Error("Expected /addpsp to reject this configuration")
			}
		})
	}
}
