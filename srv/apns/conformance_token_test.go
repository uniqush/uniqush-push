package apns

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/apnstest"
	"github.com/uniqush/uniqush-push/srv/apns/common"
	"github.com/uniqush/uniqush-push/srv/apns/http_api"
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

// fakeClock is a clock the test goroutine moves and another goroutine reads.
//
// Guarded, because both of those things are true at once. The simulator reads
// its clock on the HTTP handler goroutine while the test advances it between
// pushes, and there is no happens-before edge between the two: the round trip
// travels over a loopback socket, which is not something the race detector
// models, and the server's own mutex does not order a write the test makes
// without taking it. A plain time.Time field there is a data race that CI runs
// with -race and would eventually catch.
//
// The processor's clock was safe by accident -- results come back over a Go
// channel, which does order things -- but it is the same shape, so it uses the
// same type rather than relying on a reader to work out which of the two is
// which.
type fakeClock struct {
	mutex sync.Mutex
	now   time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

// Now reads the clock. Passed directly to SetClock on both sides.
func (c *fakeClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

// Advance moves the clock forward.
func (c *fakeClock) Advance(d time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.now = c.now.Add(d)
}

// Set moves the clock to a specific instant, for a test that jumps rather than
// advances.
func (c *fakeClock) Set(t time.Time) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.now = t
}

// setProcessorClock points the push service's HTTP/2 processor at a fake clock,
// and returns it.
func setProcessorClock(service *pushService, start time.Time) *fakeClock {
	clock := newFakeClock(start)
	processor := service.httpRequestProcessor.(interface{ SetClock(func() time.Time) })
	processor.SetClock(clock.Now)
	return clock
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
	processorClock := setProcessorClock(service, start)
	server.SetClock(newFakeClock(start).Now)

	for i := 0; i < 5; i++ {
		results := pushToSimulator(t, service, psp, createNotification("Hello World"), apnstest.DeviceToken(byte(0xc0+i)))
		if len(results) != 1 || results[0].Err != nil {
			t.Fatalf("Push %d failed: %s", i, describeResults(results))
		}
		processorClock.Advance(time.Minute)
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

	start := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	processorClock := setProcessorClock(service, start)
	serverClock := newFakeClock(start)
	server.SetClock(serverClock.Now)
	moveBoth := func(d time.Duration) {
		serverClock.Advance(d)
		processorClock.Advance(d)
	}

	results := pushToSimulator(t, service, psp, createNotification("Hello World"), apnstest.DeviceToken(0xd1))
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("The first push failed: %s", describeResults(results))
	}

	// Past the refresh interval and still well inside the expiry: uniqush
	// should have re-signed, and the simulator should accept the new token
	// because it is far enough from the last mint.
	moveBoth(http_api.TokenRefreshInterval + 15*time.Minute)

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
	processorClock := setProcessorClock(service, start)
	// The simulator's clock never moves: every token arrives at the same instant
	// as far as it is concerned.
	server.SetClock(newFakeClock(start).Now)

	results := pushToSimulator(t, service, psp, createNotification("first"), apnstest.DeviceToken(0xa1))
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("The first push failed: %s", describeResults(results))
	}

	// Past uniqush's refresh interval, so it signs a new token whose iat is far
	// enough from the first to look compliant -- if you believe the claim. It
	// arrives immediately after the previous one as far as the server can tell.
	processorClock.Advance(http_api.TokenRefreshInterval + 15*time.Minute)
	pushToSimulator(t, service, psp, createNotification("second"), apnstest.DeviceToken(0xa2))

	// Asserted on the simulator rather than on the push result, deliberately.
	//
	// This test is about the *simulator*: that it dates a mint from arrival
	// rather than from the sender's iat, so that it cannot be talked out of the
	// rate limit by a client choosing convenient claims. Whether the push
	// ultimately succeeds is a separate question and no longer a fixed one --
	// uniqush now recovers by presenting the previous bucket's token, and
	// whether that recovery is available depends on where the clocks are.
	//
	// The earlier version asserted the push failed, which was true only while
	// there was no recovery. It began passing for a reason unrelated to what it
	// was named for as soon as the bucket length changed, which is exactly the
	// kind of drift a conformance test should not have.
	refusals := 0
	for _, violation := range server.Violations() {
		if strings.Contains(violation.Details, apnstest.TokenMinInterval.String()) ||
			strings.Contains(violation.Rule, "auth") {
			refusals++
		}
	}
	if refusals == 0 {
		t.Errorf("The simulator accepted a second freshly-minted token that arrived immediately "+
			"after the first.\nIt must date the floor from arrival, not from the iat the sender "+
			"chose, or it would report a pass for exactly the behaviour Apple answers with %s.",
			apnstest.ReasonTooManyProviderTokenUpdates)
	}
}

// TestConformanceMintFloorRefusalIsRetryableNotFatal covers how uniqush reports
// a mint-floor refusal it cannot recover from.
//
// Separated from the simulator test above because it is about classification
// rather than about the floor. Both clocks move together here and the fallback
// is driven past its lifetime, so there is no earlier token to present and the
// 429 reaches the caller.
//
// It must arrive as a RetryError. The floor always clears, so the condition is
// transient; reporting it as a credential failure would tell an operator to go
// and fix a key that is perfectly good, and reporting it as a bad notification
// would send them to a payload that was never involved.
func TestConformanceMintFloorRefusalIsRetryableNotFatal(t *testing.T) {
	server, key := newTokenSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newTokenPSP(t, server, key)

	current := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	processorClock := setProcessorClock(service, current)
	serverClock := newFakeClock(current)
	server.SetClock(serverClock.Now)

	if results := pushToSimulator(t, service, psp, createNotification("first"), apnstest.DeviceToken(0xa5)); results[0].Err != nil {
		t.Fatalf("The first push failed: %s", describeResults(results))
	}

	// Far enough that even the previous bucket has expired, so no fallback
	// exists; the simulator's clock barely moves, so anything new arrives inside
	// its floor.
	//
	// The distance is derived rather than picked. previousToken returns the
	// bucket before the current one, so it is only unusable when *that* bucket
	// is more than a lifetime old -- and how far into a bucket the clock lands
	// decides whether it is. An earlier version advanced a flat two hours, which
	// happened to land 15 minutes into a bucket and left the previous one only
	// 50 minutes old: the fallback still existed and the test never reached the
	// branch it was named for. Landing on a boundary and stepping back one
	// second puts the clock at the very end of a bucket, where the previous
	// bucket's age is at its maximum.
	interval := http_api.TokenRefreshInterval
	step := int64(interval / time.Second)
	nextBoundary := time.Unix(current.Add(2*time.Hour).Unix()/step*step+step, 0).UTC()
	processorClock.Advance(nextBoundary.Add(-time.Second).Sub(current))
	serverClock.Set(current.Add(time.Minute))

	before := len(server.Violations())
	results := pushToSimulator(t, service, psp, createNotification("second"), apnstest.DeviceToken(0xa6))
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("Expected an unrecoverable mint-floor refusal to be reported, got: %s",
			describeResults(results))
	}

	// Exactly one token refused, which is what makes this the *unrecoverable*
	// case rather than a recoverable one that happened to fail twice.
	//
	// Both end in a RetryError, so the error alone cannot tell them apart -- an
	// earlier version asserted only that, and passed just as happily when a
	// fallback existed and was itself refused. Counting refusals is the
	// difference: no fallback means nothing is offered a second time.
	//
	// Counted on refusals rather than on Requests(), because the simulator
	// records a request only once it has authenticated it; a token it turns away
	// never becomes one.
	if refusals := len(server.Violations()) - before; refusals != 1 {
		t.Errorf("Expected one refused token and no fallback to retry with, got %d refusals.\n"+
			"More than one means a previous token was still within its lifetime, so this is "+
			"not the no-fallback branch the test is named for.", refusals)
	}
	if _, retryable := results[0].Err.(*push.RetryError); !retryable {
		t.Errorf("Expected a RetryError for TooManyProviderTokenUpdates, got %T: %v",
			results[0].Err, results[0].Err)
	}
	if _, unsubscribed := results[0].Err.(*push.UnsubscribeUpdate); unsubscribed {
		t.Error("A mint-floor refusal must not unsubscribe the device")
	}
}

// TestConformanceLateFirstUseInABucketRecovers is the regression test for the
// hole in the original bucketing scheme.
//
// The scheme assumed Apple would see one new token per bucket. It measures the
// floor from when it *observes* a token, and the first push of a bucket can land
// anywhere inside it. A provider that pushes for the first time near the end of
// a bucket presents that token late, then presents the next bucket's token
// moments later at the boundary -- two tokens seconds apart, inside the floor.
// That is not a one-second startup race; it is any first use in the final 20
// minutes, which for a low-traffic service is most of the time.
//
// Recovery is to fall back to the previous bucket's token, which is the one
// Apple actually saw and which is still valid. The bucket length is chosen so
// that it always is; see the constants in token.go.
func TestConformanceLateFirstUseInABucketRecovers(t *testing.T) {
	server, key := newTokenSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newTokenPSP(t, server, key)

	// Buckets are aligned to the Unix epoch, not to the wall clock, so where a
	// round time falls inside one depends entirely on the interval: 09:00 UTC is
	// 20 minutes into a 40-minute bucket and exactly on a boundary of a
	// 35-minute one. An earlier draft of this test assumed a boundary, got a
	// bucket interior, put both pushes in the same bucket and passed without
	// ever crossing anything -- so the boundary is derived below, and the
	// crossing asserted, rather than either being trusted.
	const bucket = http_api.TokenRefreshInterval
	step := int64(bucket / time.Second)
	bucketOf := func(t time.Time) time.Time {
		return time.Unix(t.UTC().Unix()/step*step, 0).UTC()
	}
	bucketStart := bucketOf(time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC))

	firstUse := bucketStart.Add(bucket - 2*time.Minute)
	afterBoundary := firstUse.Add(4 * time.Minute)
	if bucketOf(firstUse).Equal(bucketOf(afterBoundary)) {
		t.Fatalf("This test is vacuous: %s and %s share a bucket, so no second token is ever presented",
			firstUse.Format(time.TimeOnly), afterBoundary.Format(time.TimeOnly))
	}
	if gap := afterBoundary.Sub(firstUse); gap >= apnstest.TokenMinInterval {
		t.Fatalf("This test is vacuous: the two pushes are %s apart, outside Apple's %s floor",
			gap, apnstest.TokenMinInterval)
	}

	current := firstUse
	processorClock := setProcessorClock(service, current)
	serverClock := newFakeClock(current)
	server.SetClock(serverClock.Now)
	moveBoth := func(d time.Duration) {
		serverClock.Advance(d)
		processorClock.Advance(d)
	}

	// First push of the bucket, two minutes before it ends. Apple sees this
	// token now, which puts the next boundary well inside its floor. Stated
	// relative to the bucket rather than as a figure in minutes, so it does not
	// go stale the next time the interval moves.
	results := pushToSimulator(t, service, psp, createNotification("late first use"), apnstest.DeviceToken(0xc1))
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("The first push failed: %s", describeResults(results))
	}

	// Cross the boundary. uniqush now wants to send the next bucket's token,
	// only four minutes after Apple saw the previous one.
	moveBoth(afterBoundary.Sub(firstUse))

	results = pushToSimulator(t, service, psp, createNotification("just after the boundary"), apnstest.DeviceToken(0xc2))
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("A push just after a bucket boundary following a late first use failed: %s\n"+
			"This is the case the bucketing scheme originally got wrong.", describeResults(results))
	}

	// Apple accepted only the original token: the fallback carried the push.
	if tokens := server.IssuedTokens(); len(tokens) != 1 {
		t.Errorf("Expected Apple to have accepted one token, got %d: the fallback did not take effect", len(tokens))
	}

	// One violation is expected and unavoidable. Whether the floor has passed
	// is only knowable by asking, so the boundary costs exactly one refusal.
	// What must not happen is paying it again on every later push.
	if violations := server.Violations(); len(violations) != 1 {
		t.Errorf("Expected exactly one refused token at the boundary, got %d: %v",
			len(violations), violations)
	}

	// Two more pushes still inside the floor. These should go straight to the
	// accepted token, so neither reaches Apple with the refused one.
	for i, at := range []time.Duration{2 * time.Minute, 5 * time.Minute} {
		moveBoth(at)
		results = pushToSimulator(t, service, psp, createNotification("still inside the floor"), apnstest.DeviceToken(byte(0xc3+i)))
		if len(results) != 1 || results[0].Err != nil {
			t.Fatalf("Push %d inside the floor failed: %s", i, describeResults(results))
		}
	}

	if violations := server.Violations(); len(violations) != 1 {
		t.Errorf("Expected the refusal to be remembered, but Apple was offered the refused token again: %v",
			violations)
	}
}

// TestConformanceABatchAtABoundaryCostsOneRefusal is the regression test for a
// stampede.
//
// Whether Apple will accept a bucket's token is only knowable by asking, and at
// a boundary the answer can be no. Releasing a whole batch at once meant every
// device asked simultaneously and every one got the same refusal: N round trips,
// N 429s counted against the provider, and N fallback retries, all to learn one
// thing. A batch of a thousand devices turned a single unavoidable refusal into
// a thousand, and doubled the traffic for the entire batch while doing it.
//
// The fix is to send the first push of an unconfirmed bucket on its own and let
// the rest wait for the answer.
func TestConformanceABatchAtABoundaryCostsOneRefusal(t *testing.T) {
	server, key := newTokenSimulator(t)
	service, _ := newSimulatorService(t)
	psp := newTokenPSP(t, server, key)

	const bucket = http_api.TokenRefreshInterval
	step := int64(bucket / time.Second)
	bucketOf := func(at time.Time) time.Time {
		return time.Unix(at.UTC().Unix()/step*step, 0).UTC()
	}
	start := bucketOf(time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)).Add(bucket - 2*time.Minute)

	processorClock := setProcessorClock(service, start)
	serverClock := newFakeClock(start)
	server.SetClock(serverClock.Now)

	// A late first use, so the boundary that follows lands inside Apple's floor.
	results := pushToSimulator(t, service, psp, createNotification("late first use"), apnstest.DeviceToken(0x10))
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("The first push failed: %s", describeResults(results))
	}

	// Cross the boundary, then push to a whole fleet of devices at once.
	serverClock.Advance(3 * time.Minute)
	processorClock.Advance(3 * time.Minute)

	tokens := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		tokens = append(tokens, apnstest.DeviceToken(byte(0x20+i)))
	}

	results = pushToSimulator(t, service, psp, createNotification("the whole fleet"), tokens...)
	if len(results) != len(tokens) {
		t.Fatalf("Expected %d results, got %d: %s", len(tokens), len(results), describeResults(results))
	}
	for i, result := range results {
		if result.Err != nil {
			t.Fatalf("Push %d in the batch failed: %v", i, result.Err)
		}
	}

	// One refusal for the batch, not one per device.
	if refusals := len(server.Violations()); refusals != 1 {
		t.Errorf("A batch of %d at a bucket boundary produced %d refusals, expected 1.\n"+
			"Releasing every device at once makes them all ask the same question and all get "+
			"the same 429; the first push of an unconfirmed bucket has to go on its own.\n%v",
			len(tokens), refusals, server.Violations())
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
	serverClock := newFakeClock(start)
	server.SetClock(serverClock.Now)

	if results := pushToSimulator(t, service, psp, createNotification("Hello"), apnstest.DeviceToken(0xe1)); results[0].Err != nil {
		t.Fatalf("The first push failed: %s", describeResults(results))
	}

	serverClock.Set(start.Add(90 * time.Minute))

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
	// The reason, not only the type. MissingProviderToken -- what Apple answers
	// when no authorization header arrives at all -- is also a provider failure,
	// so a type-only assertion passes just as happily if uniqush stops sending
	// the token it is supposed to be sending wrongly.
	if !strings.Contains(results[0].Err.Error(), apnstest.ReasonInvalidProviderToken) {
		t.Errorf("Expected the failure to name %s, got: %v",
			apnstest.ReasonInvalidProviderToken, results[0].Err)
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
