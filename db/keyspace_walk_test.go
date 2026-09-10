package db

import (
	"fmt"
	"testing"
)

// Tests for the keyspace walks that used to run KEYS.
//
// KEYS is one command that redis runs to completion on the thread serving every
// other client, so the wildcard branch of a /push -- the only keyspace walk on
// a request path -- stalled every other push for the length of a full walk.
// SCAN pages instead, which costs a cursor loop and, more importantly, the
// snapshot: the same key can come back on two pages.
//
// These run against a real redis and are skipped when there is none, like the
// rest of the package's tests.

// wildcardSubscriber is the prefix of the subscriber names written by
// subscribeMany, chosen so the pattern cannot match the fixture's own
// subscriber or anything else in the database.
const wildcardSubscriber = "keyspace_walk_test_subscriber"

// subscribeManySize is deliberately larger than scanKeysCount, so the walk
// needs more than one round trip. A single-page walk would pass whether or not
// the cursor loop is right, and would never produce a duplicate key.
const subscribeManySize = 2 * scanKeysCount

// subscribeMany writes one subscriber set per delivery point, straight to
// redis.
//
// Straight to redis because the subject here is the walk over those keys, not
// what builds them: going through AddDeliveryPointToService would mean a
// thousand provider lookups and delivery point records for a test that reads
// neither.
func subscribeMany(t *testing.T, fixture *rebindingFixture, count int) map[string]bool {
	t.Helper()

	expected := make(map[string]bool, count)
	for i := 0; i < count; i++ {
		subscriber := fmt.Sprintf("%s_%04d", wildcardSubscriber, i)
		dpName := fmt.Sprintf("apns:devtoken-%04d", i)
		if err := fixture.raw.AddDeliveryPointToServiceSubscriber(ServiceName, subscriber, dpName); err != nil {
			t.Fatalf("Could not add a delivery point to %q: %v", subscriber, err)
		}
		expected[dpName] = true
	}
	return expected
}

// TestWildcardLookupFindsEveryMatchingSubscriber is the regression test for the
// cursor loop.
//
// KEYS returned every match in one reply. A SCAN that stopped at the first page
// -- or that mistook an empty page for the end of the walk, which is normal,
// because COUNT bounds the work done rather than the rows returned -- would
// silently push to a fraction of the subscribers a wildcard covers.
func TestWildcardLookupFindsEveryMatchingSubscriber(t *testing.T) {
	fixture := newRebindingFixture(t)
	expected := subscribeMany(t, fixture, subscribeManySize)

	found, err := fixture.raw.GetDeliveryPointsNameByServiceSubscriber(ServiceName, wildcardSubscriber+"_*", "", nil)
	if err != nil {
		t.Fatalf("Could not list delivery points by wildcard: %v", err)
	}
	if len(found[ServiceName]) != len(expected) {
		t.Errorf("Expected %d delivery points behind the wildcard, got %d",
			len(expected), len(found[ServiceName]))
	}
	for _, name := range found[ServiceName] {
		if !expected[name] {
			t.Errorf("The wildcard lookup returned %q, which it was never given", name)
		}
	}
	for name := range expected {
		if !containsName(found[ServiceName], name) {
			t.Errorf("The wildcard lookup missed %q", name)
		}
	}
}

// TestWildcardLookupReturnsEachDeliveryPointOnce is the reason the keys are
// deduplicated rather than taken as SCAN hands them over.
//
// SCAN gives up KEYS's snapshot: a key present for the whole walk can still be
// returned twice, when redis resizes its hash table underneath the cursor. Here
// each repeat would be a second copy of every delivery point behind that
// subscriber, and a duplicate notification on a phone -- the one SCAN
// concession this package cannot pass on to its caller.
func TestWildcardLookupReturnsEachDeliveryPointOnce(t *testing.T) {
	fixture := newRebindingFixture(t)
	expected := subscribeMany(t, fixture, subscribeManySize)

	found, err := fixture.raw.GetDeliveryPointsNameByServiceSubscriber(ServiceName, wildcardSubscriber+"_*", "", nil)
	if err != nil {
		t.Fatalf("Could not list delivery points by wildcard: %v", err)
	}

	seen := make(map[string]int, len(expected))
	for _, name := range found[ServiceName] {
		seen[name]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("The wildcard lookup returned %q %d times; every repeat is a duplicate push", name, count)
		}
	}
}

// TestLookupWithoutAWildcardStaysAnExactKey guards the fast path.
//
// A name with no "*" in it is one key, read directly. Turning that into a scan
// would put a keyspace walk on every ordinary push, and matching by prefix
// would push to subscribers nobody asked for: "user1" must not reach
// "user10".
func TestLookupWithoutAWildcardStaysAnExactKey(t *testing.T) {
	fixture := newRebindingFixture(t)
	if err := fixture.raw.AddDeliveryPointToServiceSubscriber(ServiceName, "user1", "apns:devtoken-user1"); err != nil {
		t.Fatalf("Could not add a delivery point: %v", err)
	}
	if err := fixture.raw.AddDeliveryPointToServiceSubscriber(ServiceName, "user10", "apns:devtoken-user10"); err != nil {
		t.Fatalf("Could not add a delivery point: %v", err)
	}

	found, err := fixture.raw.GetDeliveryPointsNameByServiceSubscriber(ServiceName, "user1", "", nil)
	if err != nil {
		t.Fatalf("Could not list delivery points: %v", err)
	}
	if len(found[ServiceName]) != 1 || found[ServiceName][0] != "apns:devtoken-user1" {
		t.Errorf("Expected exactly user1's delivery point, got %v", found[ServiceName])
	}
}

// TestRebuildServiceSetFindsProvidersWrittenBeforeTheServiceSet covers the
// other walk, which is the migration path /rebuildserviceset exists for.
//
// A database written by uniqush 1.5.x has provider records and no set of
// service names, so /subscriptions and /psps report nothing until this runs.
// The providers are written straight to redis here because that is the state
// being migrated: AddPushServiceProviderToService maintains the set, which
// would leave nothing to rebuild.
func TestRebuildServiceSetFindsProvidersWrittenBeforeTheServiceSet(t *testing.T) {
	fixture := newRebindingFixture(t)
	services := []string{ServiceName, OtherServiceName}
	for _, service := range services {
		psp, err := fixture.psm.BuildPushServiceProviderFromMap(map[string]string{
			"pushservicetype": "apns",
			"service":         service,
			"cert":            service + ".cert",
			"key":             service + ".cert.key",
		})
		if err != nil {
			t.Fatalf("Could not build a provider for %q: %v", service, err)
		}
		if err := fixture.raw.SetPushServiceProvider(psp); err != nil {
			t.Fatalf("Could not write the provider for %q: %v", service, err)
		}
	}

	before, err := fixture.raw.GetServiceNames()
	if err != nil {
		t.Fatalf("Could not read the service names: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("Expected no service names before the rebuild, got %v", before)
	}

	if rebuildErr := fixture.raw.RebuildServiceSet(); rebuildErr != nil {
		t.Fatalf("Could not rebuild the service set: %v", rebuildErr)
	}

	after, err := fixture.raw.GetServiceNames()
	if err != nil {
		t.Fatalf("Could not read the service names: %v", err)
	}
	if len(after) != len(services) {
		t.Errorf("Expected %d services after the rebuild, got %v", len(services), after)
	}
	for _, service := range services {
		if !containsName(after, service) {
			t.Errorf("The rebuild missed the service %q", service)
		}
	}
}

// TestRebuildServiceSetIsQuietOnAnEmptyDatabase checks the walk that finds
// nothing, which is what /rebuildserviceset does on every database that does
// not need it.
func TestRebuildServiceSetIsQuietOnAnEmptyDatabase(t *testing.T) {
	fixture := newRebindingFixture(t)
	if err := fixture.raw.RebuildServiceSet(); err != nil {
		t.Errorf("Rebuilding an empty database should do nothing, got: %v", err)
	}
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
