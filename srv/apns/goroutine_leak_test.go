package apns

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestBuildingAProviderLeaksNoGoroutines guards a leak that is invisible until
// something counts.
//
// NewPushService constructs the binary processor as well as the HTTP/2 one, and
// the binary processor starts its pushMux goroutine at construction rather than
// on first use. So a helper that builds a service only to call
// BuildPushServiceProviderFromMap -- which never sends anything -- still leaves
// a goroutine running for the life of the test binary unless it finalizes.
//
// Nothing fails when that happens. The tests pass, the goroutines accumulate,
// and the only symptom is a process that slowly grows while doing nothing. This
// counts them instead.
func TestBuildingAProviderLeaksNoGoroutines(t *testing.T) {
	// Let anything already in flight from earlier tests settle, so the baseline
	// is not polluted by a neighbour's teardown.
	before := waitForStableGoroutineCount(t)

	for i := 0; i < 5; i++ {
		buildIntoExistingProvider(t, nil, nil)
	}

	after := waitForStableGoroutineCount(t)

	// A small allowance: the runtime's own bookkeeping goroutines come and go,
	// and this is looking for a leak proportional to the number of services
	// built, not for an exact match.
	if leaked := after - before; leaked >= 5 {
		t.Errorf("Building 5 providers left %d goroutines behind (%d -> %d).\n"+
			"NewPushService starts the binary protocol's pushMux at construction, so a service "+
			"that is built and dropped without Finalize leaks it for the life of the process.\n\n%s",
			leaked, before, after, goroutineDump())
	}
}

// waitForStableGoroutineCount polls until the count stops moving, so the
// comparison is not racing a goroutine that is already on its way out.
func waitForStableGoroutineCount(t *testing.T) int {
	t.Helper()

	previous := runtime.NumGoroutine()
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		current := runtime.NumGoroutine()
		if current == previous {
			return current
		}
		previous = current
	}
	return previous
}

// goroutineDump renders the surviving stacks, so a failure says which goroutine
// leaked rather than only how many.
func goroutineDump() string {
	buffer := make([]byte, 1<<16)
	n := runtime.Stack(buffer, true)

	var interesting []string
	for _, stack := range strings.Split(string(buffer[:n]), "\n\n") {
		if strings.Contains(stack, "uniqush-push/srv/apns") {
			interesting = append(interesting, stack)
		}
	}
	if len(interesting) == 0 {
		return "(no surviving goroutines in srv/apns)"
	}
	return strings.Join(interesting, "\n\n")
}
