package apns

import (
	"sync"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/apnstest"
)

// The HTTP/2 client connection release tests.
//
// srv/apns/http_api goes to some trouble to retire a superseded client and to
// close everything at Finalize, and every test of that machinery substitutes a
// double through clientFactory. A double reports whatever it is asked to
// report, which is fine for the borrow accounting it is there to check and
// useless for the question these ask: whether the connection actually went
// away.
//
// So these drive the production client factory over a real socket and count the
// connections the simulator is holding. Per server, not per process: the
// obvious client-side signal is the number of live http2 read-loop goroutines,
// and that is shared with every other test in the package -- a neighbour
// opening or closing a connection moves it underneath whoever is reading, which
// makes the measurement flaky in both directions. The server knows exactly how
// many sockets are its own.

// TestFinalizedServicesDoNotAccumulateConnections is the regression test for a
// Finalize that emptied its map and left every connection open.
//
// Counted across repeated build-push-finalize cycles rather than asserted once,
// for the same reason as the retirement test below: closing is prompt but not
// synchronous. CloseIdleConnections closes what is idle at that instant, and a
// connection still handing back its last response goes a moment later through a
// different path, so a single before/after reading occasionally measures the
// scheduler instead of the code. Repeating separates the two -- a Finalize that
// releases nothing grows the count by one per cycle, and a slow close does not.
func TestFinalizedServicesDoNotAccumulateConnections(t *testing.T) {
	const cycles = 8

	servers := make([]*apnstest.Server, 0, cycles)
	notif := push.NewEmptyNotification()
	notif.Data["msg"] = "finalize"

	for cycle := 0; cycle < cycles; cycle++ {
		server := startSimulator(t)
		servers = append(servers, server)

		service, finalize := newServiceFinalizedOnce(t)
		psp := newSimulatorPSP(t, server, nil)
		pushToSimulator(t, service, psp, notif, deviceTokenForRelease(byte(cycle)))

		// Asserted so the test cannot pass vacuously. If no connection was ever
		// opened -- a broken simulator, a push that failed before dialling --
		// then finding none open afterwards would prove nothing.
		if live := server.ActiveConnections(); live < 1 {
			t.Fatalf("Cycle %d opened no connection to its simulator; this test cannot say "+
				"anything about closing one (active: %d).", cycle, live)
		}
		finalize()
	}

	total := func() int {
		sum := 0
		for _, server := range servers {
			sum += server.ActiveConnections()
		}
		return sum
	}
	// A handful may still be closing. The tolerance is loose on purpose: this
	// is a test about accumulation, and the two outcomes are far apart -- a
	// Finalize that releases nothing leaves `cycles` connections open, where a
	// slow close leaves one or two. Tightening it to exactly zero measures the
	// scheduler under -race rather than the code.
	const tolerated = 3
	settled := total()
	for i := 0; i < 500 && settled > tolerated; i++ {
		time.Sleep(10 * time.Millisecond)
		settled = total()
	}
	if settled > tolerated {
		t.Errorf("After %d finalized services, %d connections are still open (at most %d "+
			"expected, for a few still closing).\nFinalize marks every cached client retired and "+
			"closes it, so the connection each push opened should be gone.", cycles, settled, tolerated)
	}
}

// TestRetiringASupersededClientsDoNotAccumulate covers the other half: a
// provider repointed at a new destination leaves its old client behind.
//
// This is the case retireSupersededClient exists for, and the one whose cost is
// unbounded. Transports here deliberately have no idle timeout -- infrequent
// pushes otherwise lose their connection mid-flight -- so nothing else ever
// reclaims them: a provider repointed a few times a day accumulates one live
// connection per change, to destinations uniqush is no longer using, for the
// life of the process.
//
// Accumulation is what is asserted, rather than "the previous destination is at
// zero the moment the next push returns". Closing a superseded client is prompt
// but not synchronous -- CloseIdleConnections closes what is idle at that
// instant, and a connection still handing back its last response goes a moment
// later through a different path -- so a single before/after reading measures
// the scheduler as much as the code. Repointing repeatedly separates the two:
// a leak grows with every move, and a lingering close does not.
func TestRetiringASupersededClientsDoNotAccumulate(t *testing.T) {
	const moves = 8

	service, _ := newServiceFinalizedOnce(t)
	notif := push.NewEmptyNotification()
	notif.Data["msg"] = "retire"

	servers := make([]*apnstest.Server, 0, moves)
	first := startSimulator(t)
	servers = append(servers, first)

	psp := newSimulatorPSP(t, first, nil)
	pushToSimulator(t, service, psp, notif, deviceTokenForRelease(0))
	if live := first.ActiveConnections(); live < 1 {
		t.Fatalf("Pushing opened no connection to the first simulator (active: %d)", live)
	}

	for move := 1; move < moves; move++ {
		next := startSimulator(t)
		servers = append(servers, next)

		// The same provider name, a different destination. The credentials have
		// to be reused verbatim: cert and key live in FixedData, which is what
		// psp.Name() hashes, so generating a fresh pair would produce a
		// *different* provider and retire nothing. Only endpoint and cacert
		// change, and both live in VolatileData -- which is precisely the case
		// retirement is for.
		repointed := repointProvider(t, psp, next)
		if repointed.Name() != psp.Name() {
			t.Fatalf("Expected repointing to keep the provider name %q, got %q; "+
				"this test is no longer exercising retirement.", psp.Name(), repointed.Name())
		}
		pushToSimulator(t, service, repointed, notif, deviceTokenForRelease(byte(move)))
	}

	// One live connection is expected: the destination in use. A few more may
	// still be closing. The tolerance is loose on purpose -- the two outcomes
	// are far apart, since without retirement this is `moves` and climbing,
	// where a slow close leaves one or two behind. Tightening it measures the
	// scheduler under -race rather than the code.
	total := func() int {
		sum := 0
		for _, server := range servers {
			sum += server.ActiveConnections()
		}
		return sum
	}
	const tolerated = 3
	settled := total()
	for i := 0; i < 500 && settled > tolerated; i++ {
		time.Sleep(10 * time.Millisecond)
		settled = total()
	}
	if settled > tolerated {
		t.Errorf("After %d repoints, %d connections are still open across %d destinations "+
			"(at most %d expected: the one in use, and a few still closing).\n"+
			"retireSupersededClient drops the old entry from the map and closes it, so the "+
			"connections to destinations the provider has been moved off should not survive -- "+
			"and these transports have no idle timeout, so nothing else will reclaim them.",
			moves-1, settled, len(servers), tolerated)
	}

	// And the destination actually in use is connected, so a broken final push
	// cannot make this pass.
	if live := servers[len(servers)-1].ActiveConnections(); live < 1 {
		t.Errorf("Nothing is connected to the destination in use (active: %d), so the last push "+
			"did not go where this test assumes.", live)
	}
}

// newServiceFinalizedOnce builds a simulator-backed service that these tests
// finalize themselves.
//
// newSimulatorService registers Finalize as a cleanup, which is right for tests
// that never call it -- but here Finalize is the thing under test, and calling
// it twice makes the service log a warning that reads like a bug in the code
// rather than an artefact of the harness.
func newServiceFinalizedOnce(t *testing.T) (*pushService, func()) {
	t.Helper()

	service := NewPushService().(*pushService)
	// Buffered generously: waitResults reports asynchronously and a test that
	// does not read every message must not wedge the goroutine producing them.
	service.SetErrorReportChan(make(chan push.Error, 100))

	var once sync.Once
	finalize := func() { once.Do(service.Finalize) }
	t.Cleanup(finalize)
	return service, finalize
}

// repointProvider re-registers a provider at a different simulator, keeping its
// credentials and therefore its name.
func repointProvider(t *testing.T, psp *push.PushServiceProvider, server *apnstest.Server) *push.PushServiceProvider {
	t.Helper()

	caPath, err := server.WriteCACert(t.TempDir())
	if err != nil {
		t.Fatalf("Could not write the simulator's CA: %v", err)
	}

	repointed, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"pushservicetype": "apns",
		"service":         psp.FixedData["service"],
		"subscriber":      psp.FixedData["subscriber"],
		"cert":            psp.FixedData["cert"],
		"key":             psp.FixedData["key"],
		"bundleid":        conformanceTopic,
		"endpoint":        server.URL(),
		"cacert":          caPath,
	})
	if err != nil {
		t.Fatalf("Could not repoint the provider: %v", err)
	}
	return repointed
}

// deviceTokenForRelease keeps these tests off the tokens the conformance tests
// configure canned responses for.
func deviceTokenForRelease(seed byte) string {
	return apnstest.DeviceToken(0xd0 + seed)
}
