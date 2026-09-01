package apns

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/apnstest"
	"github.com/uniqush/uniqush-push/srv/apns/common"
	"github.com/uniqush/uniqush-push/srv/apns/http_api"
)

// Integration tests for deterministic provider tokens across several uniqush
// instances sharing one signing key, against a single simulated Apple.
//
// This is the scenario the whole deterministic scheme exists for, and the one
// no single-process test can reach. Apple's mint floor is per *key*, counted on
// arrival, and it does not care how many processes an operator runs: two
// instances refreshing on their own schedules trip it between them without
// either behaving badly, and a restart inside the window trips it against a
// predecessor that no longer exists.
//
// The failure mode is also the reason these are worth the setup. Both edges --
// the 20-minute floor and the 1-hour expiry -- reject the *push*, not just the
// token, so getting this wrong takes a provider entirely offline rather than
// degrading it. Neither boundary is reachable in real time.
//
// An "instance" here is a separate pushService with its own request processor,
// which is what actually distinguishes one uniqush process from another: the
// token cache and the client cache both live on the processor. Nothing is
// shared between them except the .p8 on disk and the simulator.

// instance is one uniqush process, with a clock that can be moved independently
// of its peers so that skew and staggered starts are reachable.
type instance struct {
	name    string
	service *pushService
	psp     *push.PushServiceProvider
	skew    time.Duration
}

// fleet is a set of instances sharing one signing key and one simulated Apple.
type fleet struct {
	t      *testing.T
	server *apnstest.Server
	key    *apnstest.SigningKey
	now    time.Time
}

// newFleet starts a simulator demanding token auth, with the wall clock at start.
func newFleet(t *testing.T, start time.Time) *fleet {
	t.Helper()

	server, key := newTokenSimulator(t)
	f := &fleet{t: t, server: server, key: key, now: start}
	// The simulator reads the shared wall clock, so every instance's traffic is
	// judged against one timeline no matter how their own clocks are skewed.
	server.SetClock(func() time.Time { return f.now })
	return f
}

// start brings up an instance whose clock runs `skew` off the shared wall clock.
//
// A negative skew is an instance running behind, which is the interesting
// direction: it stays in the previous bucket longer than its peers.
func (f *fleet) start(name string, skew time.Duration) *instance {
	f.t.Helper()

	service, _ := newSimulatorService(f.t)
	inst := &instance{
		name:    name,
		service: service,
		psp:     newTokenPSP(f.t, f.server, f.key),
		skew:    skew,
	}

	clock := func() time.Time { return f.now.Add(skew) }
	service.httpRequestProcessor.(interface{ SetClock(func() time.Time) }).SetClock(clock)
	return inst
}

// startDisposable brings up an instance the test will stop itself.
//
// Identical to start except that it registers no cleanup: pushService.Finalize
// is not idempotent -- the binary processor prints a complaint on a second call
// -- so an instance that is explicitly stopped must not also be stopped again
// at the end of the test.
func (f *fleet) startDisposable(name string) *instance {
	f.t.Helper()

	service := NewPushService().(*pushService)
	service.SetErrorReportChan(make(chan push.Error, 100))
	service.httpRequestProcessor.(interface{ SetClock(func() time.Time) }).
		SetClock(func() time.Time { return f.now })

	return &instance{name: name, service: service, psp: newTokenPSP(f.t, f.server, f.key)}
}

// startWithKeyAt brings up an instance that reaches the same signing key by a
// different path, as a copied or symlinked deployment would.
func (f *fleet) startWithKeyAt(name, authKeyPath string) *instance {
	f.t.Helper()
	ensureAPNSRegistered()

	caPath, err := f.server.WriteCACert(f.t.TempDir())
	if err != nil {
		f.t.Fatalf("Could not write the simulator's CA: %v", err)
	}

	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"pushservicetype":  "apns",
		"service":          "tokenconformance",
		"subscriber":       "conformance",
		common.AuthKeyKey:  authKeyPath,
		common.KeyIDKey:    f.key.KeyID,
		common.TeamIDKey:   f.key.TeamID,
		"bundleid":         conformanceTopic,
		common.EndpointKey: f.server.URL(),
		common.CACertKey:   caPath,
	})
	if err != nil {
		f.t.Fatalf("Could not build a provider for %s: %v", name, err)
	}

	service, _ := newSimulatorService(f.t)
	service.httpRequestProcessor.(interface{ SetClock(func() time.Time) }).
		SetClock(func() time.Time { return f.now })

	return &instance{name: name, service: service, psp: psp}
}

// advance moves the shared wall clock, and with it every instance's clock.
func (f *fleet) advance(d time.Duration) { f.now = f.now.Add(d) }

// push sends one notification from an instance and fails the test if it did not
// arrive.
func (f *fleet) push(inst *instance, deviceToken byte) {
	f.t.Helper()

	results := pushToSimulator(f.t, inst.service, inst.psp,
		createNotification("hello from "+inst.name), apnstest.DeviceToken(deviceToken))

	if len(results) != 1 || results[0].Err != nil {
		f.t.Fatalf("Push from %s at %s failed: %s",
			inst.name, f.now.Format(time.TimeOnly), describeResults(results))
	}
}

// expectOneToken asserts that Apple has accepted exactly one distinct token,
// and that nothing it saw broke a rule.
//
// One is the only interesting number for a fleet inside a single bucket, which
// is what every caller is checking; a general "expect N" helper would just be a
// parameter that never varies.
func (f *fleet) expectOneToken(why string) {
	f.t.Helper()

	if got := len(f.server.IssuedTokens()); got != 1 {
		f.t.Errorf("Apple accepted %d distinct tokens, expected 1.\n%s", got, why)
	}
	assertNoViolations(f.t, f.server)
}

// bucketStart rounds a time down to a bucket boundary.
//
// Buckets are aligned to the Unix epoch rather than to the wall clock, so
// whether a round-looking time is a boundary depends on the interval and not on
// intuition. Tests that need one round down to it rather than assuming.
func bucketStart(t time.Time) time.Time {
	step := int64(fleetBucket / time.Second)
	return time.Unix(t.UTC().Unix()/step*step, 0).UTC()
}

// fleetBucket is the production bucket length, read from the implementation
// rather than restated here. It has changed twice under review -- 45 minutes,
// then 40, now 35 -- and every time a test carried its own copy the copy went
// stale silently, leaving the test computing boundaries that did not exist.
const fleetBucket = http_api.TokenRefreshInterval

var fleetEpoch = bucketStart(time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC))

// TestMultiInstanceSharesOneTokenPerBucket is the headline case.
//
// Three instances, one key, one bucket. Before deterministic signing each would
// have minted its own JWT on its own schedule, and Apple would have answered the
// second and third with TooManyProviderTokenUpdates -- taking those instances
// offline for up to twenty minutes while the first worked fine, which is a
// memorably confusing way for a fleet to fail.
func TestMultiInstanceSharesOneTokenPerBucket(t *testing.T) {
	f := newFleet(t, fleetEpoch.Add(2*time.Minute))

	for i, name := range []string{"alpha", "beta", "gamma"} {
		inst := f.start(name, 0)
		f.push(inst, byte(0x10+i))
		// Spread across the bucket, so this is not passing because everything
		// happened in the same instant.
		f.advance(3 * time.Minute)
	}

	f.expectOneToken("Three instances sharing a .p8 must present one identical token per bucket. " +
		"Apple's mint floor is per key and counts tokens on arrival, so independent schedules " +
		"would put all but the first instance offline.")
}

// TestMultiInstanceStaggeredStartsAgree covers instances that come up at
// different points in a bucket, as a rolling deploy produces.
//
// A newcomer has an empty cache and must arrive at the token its peers are
// already using by computing it, not by being told.
func TestMultiInstanceStaggeredStartsAgree(t *testing.T) {
	f := newFleet(t, fleetEpoch)

	first := f.start("first", 0)
	f.push(first, 0x20)

	// Well into the bucket, so the newcomer is not simply repeating the first
	// instance's arithmetic at the same instant.
	f.advance(fleetBucket / 2)

	second := f.start("second", 0)
	f.push(second, 0x21)
	f.push(first, 0x22)

	f.expectOneToken("An instance starting mid-bucket must compute the token its peers are " +
		"already using rather than minting a new one")
}

// TestMultiInstanceRestartWithinTheFloorCostsNothing is the restart case that
// motivated the design.
//
// The original problem was stated as: after a restart there exists a valid token
// the process cannot reach. A replacement starting inside the 20-minute floor
// could only mint, and Apple would refuse it. Here the predecessor is finalized
// -- its cache gone, as a killed process's would be -- and the replacement has
// to reproduce the token from the key and the clock alone.
func TestMultiInstanceRestartWithinTheFloorCostsNothing(t *testing.T) {
	f := newFleet(t, fleetEpoch.Add(time.Minute))

	original := f.startDisposable("original")
	f.push(original, 0x30)

	// Gone, along with everything it had cached.
	original.service.Finalize()

	// Well inside the floor: the window in which a mint would be refused.
	f.advance(4 * time.Minute)

	replacement := f.start("replacement", 0)
	f.push(replacement, 0x31)

	f.expectOneToken("A restart inside Apple's 20-minute floor must recompute the same token " +
		"rather than mint a second one. This is the failure the deterministic scheme exists to " +
		"remove: the predecessor's token is valid but unreachable by any other means.")
}

// TestMultiInstanceToleratesClockSkew checks that instances whose clocks
// disagree do not multiply tokens.
//
// Skew delays adoption rather than causing a mint. An instance running behind
// stays in the previous bucket a little longer and keeps presenting the token
// its peers have already shown Apple; when it crosses, it computes the one they
// have already computed.
//
// Started mid-bucket deliberately. Skew is only harmless once the older token
// has been seen: an instance whose clock straddles a boundary *at cold start*
// computes a token from a bucket nobody has presented yet, and Apple has no
// reason to accept a second unfamiliar token inside the floor. That case is
// covered separately below rather than hidden by choosing a convenient start.
func TestMultiInstanceToleratesClockSkew(t *testing.T) {
	f := newFleet(t, fleetEpoch.Add(10*time.Minute))

	ahead := f.start("ahead", 45*time.Second)
	behind := f.start("behind", -45*time.Second)

	f.push(ahead, 0x40)
	f.push(behind, 0x41)
	f.expectOneToken("Skewed instances inside one bucket must still agree")

	// Land the shared clock inside the skew window around the next boundary, so
	// "ahead" has crossed and "behind" has not.
	f.advance(fleetBucket - 10*time.Minute + 20*time.Second)

	f.push(ahead, 0x42)
	f.push(behind, 0x43)

	// Two tokens exist now -- the old bucket's and the new one's -- which is
	// correct. What must not happen is a violation: the instance that has not
	// crossed keeps presenting a token Apple has already seen, so no second
	// unfamiliar token arrives inside the floor.
	assertNoViolations(t, f.server)
	if got := len(f.server.IssuedTokens()); got != 2 {
		t.Errorf("Expected the boundary to introduce exactly one new token (2 total), got %d. "+
			"Skew must delay adoption, not multiply tokens.", got)
	}
}

// TestMultiInstanceColdStartAcrossABoundaryWithSkew documents the one case
// where skew does cost something, so that it is a known and measured limit
// rather than a surprise during an incident.
//
// Bucketing makes instances agree on which token to compute; it cannot make
// Apple familiar with a token nobody has presented. When a fleet's very first
// pushes happen with clocks straddling a boundary, two instances legitimately
// compute two different buckets' tokens, and neither has a predecessor to fall
// back to -- the previous bucket's token is equally unfamiliar. Apple accepts
// the first and refuses the second.
//
// This is narrow: it needs a cold start, within the skew of a boundary, and it
// resolves itself as soon as the floor clears. It is reported as a retryable
// error rather than a dropped push, which is the behaviour asserted here.
func TestMultiInstanceColdStartAcrossABoundaryWithSkew(t *testing.T) {
	f := newFleet(t, fleetEpoch)

	ahead := f.start("ahead", 45*time.Second)    // already in the new bucket
	behind := f.start("behind", -45*time.Second) // still in the previous one

	f.push(ahead, 0x80)

	results := pushToSimulator(t, behind.service, behind.psp,
		createNotification("cold start"), apnstest.DeviceToken(0x81))

	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("Expected the skewed cold start to be refused rather than silently accepted, got: %s",
			describeResults(results))
	}
	if _, retryable := results[0].Err.(*push.RetryError); !retryable {
		t.Errorf("A refusal that clears on its own must be retryable, not fatal; got %T: %v",
			results[0].Err, results[0].Err)
	}

	// Once the floor has passed, the laggard recovers without intervention.
	f.advance(21 * time.Minute)
	f.push(behind, 0x82)
}

// TestMultiInstanceSharesAKeyReachedByDifferentPaths checks the cache is keyed
// on the key itself rather than on where it was read from.
//
// Two hosts rarely agree on a path. A key deployed to /etc/uniqush on one and
// /opt/uniqush/etc on another is one key as far as Apple's per-key floor is
// concerned, and keying the cache on the pathname would give them separate mint
// schedules -- reintroducing the exact failure the scheme removes, by a
// different route.
func TestMultiInstanceSharesAKeyReachedByDifferentPaths(t *testing.T) {
	f := newFleet(t, fleetEpoch.Add(time.Minute))

	contents, err := os.ReadFile(f.key.Path)
	if err != nil {
		t.Fatalf("Could not read the signing key: %v", err)
	}
	copied := filepath.Join(t.TempDir(), "copied-authkey.p8")
	if err := os.WriteFile(copied, contents, 0o600); err != nil {
		t.Fatalf("Could not copy the signing key: %v", err)
	}

	f.push(f.startWithKeyAt("original-path", f.key.Path), 0x50)
	f.advance(2 * time.Minute)
	f.push(f.startWithKeyAt("copied-path", copied), 0x51)

	f.expectOneToken("The same .p8 reached by two paths is one key to Apple, so both instances " +
		"must present the same token")
}

// TestMultiInstanceAcrossSeveralBucketsMintsOnePerBucket is the sustained case:
// a fleet pushing steadily for two hours.
//
// The per-bucket assertions above could each be satisfied by a scheme that
// happened to be stable over a short window. This one pins the rate: one token
// per bucket, no more and no fewer, however many instances are pushing and
// whatever order they do it in.
func TestMultiInstanceAcrossSeveralBucketsMintsOnePerBucket(t *testing.T) {
	f := newFleet(t, fleetEpoch)

	instances := []*instance{f.start("one", 0), f.start("two", 0), f.start("three", 0)}

	// Two hours in ten-minute steps, counting the buckets actually visited
	// rather than predicting them. The first draft asserted four from a
	// back-of-envelope "two hours over whole buckets" and was wrong: the
	// last push lands at +110 minutes, inside the third bucket, so only three
	// are ever entered. Deriving it keeps the test honest if the interval moves.
	visited := map[time.Time]bool{}
	deviceToken := byte(0x60)
	for step := 0; step < 12; step++ {
		visited[bucketStart(f.now)] = true
		for _, inst := range instances {
			f.push(inst, deviceToken)
			deviceToken++
		}
		f.advance(10 * time.Minute)
	}

	if got := len(f.server.IssuedTokens()); got != len(visited) {
		t.Errorf("Expected one token per bucket visited (%d), got %d.\n"+
			"More means instances are minting independently; fewer means a token is being "+
			"reused past Apple's one-hour expiry.", len(visited), got)
	}
	assertNoViolations(t, f.server)
}

// TestMultiInstanceLateFirstUseRecoversAcrossInstances is the multi-process form
// of the boundary problem, and the case that most needs deterministic signing.
//
// Apple measures its floor from when it observes a token, so a fleet whose first
// push in a bucket lands late will present the next bucket's token shortly
// afterwards and be refused. Recovery is to fall back to the previous bucket's
// token -- and here the instance doing the falling back is not the one that
// presented it. It can only reproduce that token because signing is
// deterministic; a randomly-signed predecessor's JWT would be unreachable.
func TestMultiInstanceLateFirstUseRecoversAcrossInstances(t *testing.T) {
	f := newFleet(t, fleetEpoch.Add(fleetBucket-2*time.Minute))

	first := f.start("first", 0)
	second := f.start("second", 0)

	// Late first use: the fleet starts two minutes before the bucket ends, so
	// Apple first sees this bucket's token with only those two minutes left --
	// well inside the 20-minute floor that the next boundary then falls in.
	// Stated relative to the bucket rather than as a figure in minutes, which
	// stops describing the run the moment the interval changes.
	f.push(first, 0x70)

	// Cross the boundary four minutes later, on the *other* instance.
	f.advance(4 * time.Minute)
	f.push(second, 0x71)

	if got := len(f.server.IssuedTokens()); got != 1 {
		t.Errorf("Expected the fallback to reuse the token Apple accepted, leaving 1, got %d", got)
	}

	// Each instance pays exactly one refusal, and no more.
	//
	// The memo that stops the refusal being rediscovered lives on the processor,
	// so it is per process by construction -- there is nothing to share it
	// through, which is the same constraint that ruled out sharing the token
	// itself. So the fleet's cost at a boundary is one probe per instance, not
	// one per fleet and not one per push. The first draft of this test asserted
	// one per fleet and was simply wrong about which of those it was.
	if violations := len(f.server.Violations()); violations != 1 {
		t.Errorf("Expected one refusal from the instance that crossed first, got %d: %v",
			violations, f.server.Violations())
	}

	// Several more pushes from both instances, still inside the floor. The
	// instance that has already been refused must not probe again; the other
	// discovers it once.
	for i := 0; i < 3; i++ {
		f.advance(2 * time.Minute)
		f.push(first, byte(0x72+i*2))
		f.push(second, byte(0x73+i*2))
	}

	if violations := len(f.server.Violations()); violations != 2 {
		t.Errorf("Expected one refusal per instance (2 in total) however many pushes follow, got %d: %v\n"+
			"A count that grows with the number of pushes means the refusal is being "+
			"rediscovered on every batch instead of remembered.", violations, f.server.Violations())
	}
}
