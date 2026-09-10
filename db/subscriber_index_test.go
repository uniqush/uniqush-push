package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/uniqush/uniqush-push/push"
)

// Tests for the per-service subscriber index: srv-2-sub:<service>, a sorted set
// of subscriber names scored by their last /subscribe, and
// srv.type-2-dp:<service>:<type>, a set of delivery point names.
//
// Both are written by the same two scripts that write the subscriber's device
// set, so the property under test throughout is that the three cannot disagree.

// indexMembers lists a sorted set's members, with their scores, through the
// same ZSCAN the wildcard read path uses.
func indexMembers(t *testing.T, fixture *rebindingFixture, key string) map[string]float64 {
	t.Helper()

	members := make(map[string]float64)
	var cursor uint64
	for {
		page, next, err := fixture.raw.client.ZScan(context.Background(), key, cursor, "*", 100).Result()
		if err != nil {
			t.Fatalf("Could not scan %q: %v", key, err)
		}
		// ZSCAN returns members and scores flattened into one list.
		for i := 0; i+1 < len(page); i += 2 {
			score, err := strconv.ParseFloat(page[i+1], 64)
			if err != nil {
				t.Fatalf("Could not read the score of %q in %q: %v", page[i], key, err)
			}
			members[page[i]] = score
		}
		if next == 0 {
			return members
		}
		cursor = next
	}
}

func typeMembers(t *testing.T, fixture *rebindingFixture, pushServiceType string) []string {
	t.Helper()

	names, err := fixture.raw.client.SMembers(context.Background(),
		typeDeviceSetKey(ServiceName, pushServiceType)).Result()
	if err != nil {
		t.Fatalf("Could not read the %s device set: %v", pushServiceType, err)
	}
	return names
}

// TestSubscribeIndexesTheSubscriberAndTheDevice is the base case.
func TestSubscribeIndexesTheSubscriberAndTheDevice(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	before := time.Now().Unix()
	dp := fixture.subscribe(t, "devtoken-1")

	subscribers := indexMembers(t, fixture, subscriberIndexKey(ServiceName))
	score, listed := subscribers[rebindingSubscriber]
	if !listed {
		t.Fatalf("Expected %q in the service's subscriber index, got %v", rebindingSubscriber, subscribers)
	}
	if score < float64(before) || score > float64(time.Now().Unix()) {
		t.Errorf("Expected the score to be the time of the subscribe, got %v", score)
	}

	if names := typeMembers(t, fixture, "apns"); len(names) != 1 || names[0] != dp.Name() {
		t.Errorf("Expected the apns device set to hold %q, got %v", dp.Name(), names)
	}
}

// TestResubscribingRefreshesTheScore is what makes the score a last-seen.
//
// An application calls /subscribe when it launches, and the device is usually
// already subscribed, so the SADD adds nothing. The ZADD has to happen anyway,
// or the score records only the first launch ever.
func TestResubscribingRefreshesTheScore(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")

	// Backdate the score, standing in for a subscribe a year ago.
	key := subscriberIndexKey(ServiceName)
	stale := float64(time.Now().Add(-365 * 24 * time.Hour).Unix())
	if err := fixture.raw.client.ZAdd(context.Background(), key,
		redis.Z{Score: stale, Member: rebindingSubscriber}).Err(); err != nil {
		t.Fatalf("Could not seed redis: %v", err)
	}

	fixture.subscribe(t, "devtoken-1")

	if score := indexMembers(t, fixture, key)[rebindingSubscriber]; score <= stale {
		t.Errorf("Re-subscribing did not refresh the score: still %v", score)
	}
	if got := len(indexMembers(t, fixture, key)); got != 1 {
		t.Errorf("Expected the subscriber to be indexed once, got %d entries", got)
	}
}

// TestUnsubscribingTheLastDeviceRemovesTheSubscriber covers the conditional
// that makes these scripts rather than a MULTI.
func TestUnsubscribingTheLastDeviceRemovesTheSubscriber(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	first := fixture.subscribe(t, "devtoken-1")
	second := fixture.subscribe(t, "devtoken-2")

	if err := fixture.client.RemoveDeliveryPointFromService(ServiceName, rebindingSubscriber, first); err != nil {
		t.Fatalf("Could not unsubscribe the first device: %v", err)
	}

	// One device left, so the subscriber is still in the service.
	if _, listed := indexMembers(t, fixture, subscriberIndexKey(ServiceName))[rebindingSubscriber]; !listed {
		t.Error("Removing one of two devices removed the subscriber from the index")
	}
	if names := typeMembers(t, fixture, "apns"); len(names) != 1 || names[0] != second.Name() {
		t.Errorf("Expected the apns device set to hold only %q, got %v", second.Name(), names)
	}

	if err := fixture.client.RemoveDeliveryPointFromService(ServiceName, rebindingSubscriber, second); err != nil {
		t.Fatalf("Could not unsubscribe the second device: %v", err)
	}

	if _, listed := indexMembers(t, fixture, subscriberIndexKey(ServiceName))[rebindingSubscriber]; listed {
		t.Error("Removing the last device left the subscriber in the index")
	}
	if names := typeMembers(t, fixture, "apns"); len(names) != 0 {
		t.Errorf("Expected an empty apns device set, got %v", names)
	}
}

// TestCleaningUpAMissingDeliveryPointMaintainsTheIndex covers the third caller
// of the unsubscribe script: the read path's teardown of a name whose record
// has gone. It writes to the same three keys, or the index outlives the device.
func TestCleaningUpAMissingDeliveryPointMaintainsTheIndex(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	dp := fixture.subscribe(t, "devtoken-1")

	if err := fixture.raw.RemoveDeliveryPoint(dp.Name()); err != nil {
		t.Fatalf("Could not remove the delivery point record: %v", err)
	}
	// Reading the subscriber is what triggers the cleanup.
	if pairs := fixture.pairs(t); len(pairs) != 0 {
		t.Fatalf("Expected no devices, got %d", len(pairs))
	}

	if _, listed := indexMembers(t, fixture, subscriberIndexKey(ServiceName))[rebindingSubscriber]; listed {
		t.Error("The subscriber is still indexed after their only device was cleaned up")
	}
	if names := typeMembers(t, fixture, "apns"); len(names) != 0 {
		t.Errorf("The cleaned-up device is still in the apns device set: %v", names)
	}
}

// TestTheIndexSeparatesPushServiceTypes pins the per-type device sets, which
// are what /stats counts.
func TestTheIndexSeparatesPushServiceTypes(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.addProviderOfType(t, "fcm", "first.cert")

	apns := fixture.subscribe(t, "devtoken-1")
	fcm := fixture.subscribeWithType(t, "fcm", "regid-1")

	if names := typeMembers(t, fixture, "apns"); len(names) != 1 || names[0] != apns.Name() {
		t.Errorf("Expected only the apns device in the apns set, got %v", names)
	}
	if names := typeMembers(t, fixture, "fcm"); len(names) != 1 || names[0] != fcm.Name() {
		t.Errorf("Expected only the fcm device in the fcm set, got %v", names)
	}
	// One subscriber, two devices, two types: still one entry in the index.
	if got := len(indexMembers(t, fixture, subscriberIndexKey(ServiceName))); got != 1 {
		t.Errorf("Expected one subscriber in the index, got %d", got)
	}
}

// TestSubscribersOfDifferentServicesAreIndexedApart guards the obvious mistake
// of one global subscriber index.
func TestSubscribersOfDifferentServicesAreIndexedApart(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")

	if got := len(indexMembers(t, fixture, subscriberIndexKey(OtherServiceName))); got != 0 {
		t.Errorf("Expected nothing in another service's index, got %d entries", got)
	}
}

// TestManySubscribersAreAllIndexed walks past one ZSCAN page, which is the
// thing every wildcard push depends on once the index is in use.
func TestManySubscribersAreAllIndexed(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")

	const subscribers = 300
	for i := 0; i < subscribers; i++ {
		fixture.subscribeAs(t, fmt.Sprintf("subscriber-%d", i), "devtoken-1")
	}

	if got := len(indexMembers(t, fixture, subscriberIndexKey(ServiceName))); got != subscribers {
		t.Errorf("Expected all %d subscribers in the index, got %d", subscribers, got)
	}
}

// clearIndexBuiltMarker puts the fixture's database back into the state an
// upgraded one starts in: index entries for whatever has been written since,
// and no claim that they cover everything.
//
// The in-process cache of a positive answer has to go too, since it is what a
// long-running uniqush would be holding.
func clearIndexBuiltMarker(t *testing.T, fixture *rebindingFixture) {
	t.Helper()

	if err := fixture.raw.client.Del(context.Background(), SubscriberIndexBuiltKey).Err(); err != nil {
		t.Fatalf("Could not clear the built marker: %v", err)
	}
	fixture.raw.indexBuilt.Store(false)
}

// TestAFreshDatabaseIsIndexedAtStartup covers the one case that marks itself.
//
// A brand new installation has nothing to rebuild, and requiring
// /rebuildsubscriberindex before wildcards worked would be a rite of passage
// rather than a safeguard.
func TestAFreshDatabaseIsIndexedAtStartup(t *testing.T) {
	fixture := newRebindingFixture(t)

	// newRebindingFixture flushes the database and then does what startup does.
	if !fixture.keyExists(t, SubscriberIndexBuiltKey) {
		t.Fatal("An empty database was not marked as indexed at startup")
	}

	built, err := fixture.raw.subscriberIndexBuilt()
	if err != nil {
		t.Fatalf("Could not read the built marker: %v", err)
	}
	if !built {
		t.Error("The marker is present but the database does not read as indexed")
	}
}

// TestAnExistingDatabaseIsNotIndexedAtStartup is the case that matters: an
// upgrade must not claim an index it has not built.
func TestAnExistingDatabaseIsNotIndexedAtStartup(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")
	clearIndexBuiltMarker(t, fixture)

	if err := fixture.client.PrepareSubscriberIndex(nil); err != nil {
		t.Fatalf("PrepareSubscriberIndex failed: %v", err)
	}

	if fixture.keyExists(t, SubscriberIndexBuiltKey) {
		t.Error("Startup marked a database it had not indexed")
	}
}

// TestTheBuiltMarkerIsNotInferredFromTheIndexKey is the mistake the marker
// exists to prevent.
//
// Redis deletes an empty sorted set, so srv-2-sub:<service> is absent on a
// database that has never been indexed -- and the first /subscribe after an
// upgrade recreates it holding one subscriber. Anything keyed on the index
// key's presence would switch itself on at that moment, and a wildcard push
// would then miss every subscriber who had not happened to re-subscribe.
func TestTheBuiltMarkerIsNotInferredFromTheIndexKey(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	clearIndexBuiltMarker(t, fixture)

	// A subscribe after the upgrade. The index key now exists.
	fixture.subscribe(t, "devtoken-1")
	if !fixture.keyExists(t, subscriberIndexKey(ServiceName)) {
		t.Fatal("Expected the subscribe to create the service's index")
	}

	built, err := fixture.raw.subscriberIndexBuilt()
	if err != nil {
		t.Fatalf("Could not read the built marker: %v", err)
	}
	if built {
		t.Error("An index holding one post-upgrade subscriber was read as covering the database")
	}
}

// TestCheckDBReportsAnUnbuiltIndex makes the cause visible.
//
// On such a database every subscriber written before the upgrade is missing
// from the index. Reporting each of them, rather than the one reason, would
// bury the answer under its own consequences.
func TestCheckDBReportsAnUnbuiltIndex(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")
	clearIndexBuiltMarker(t, fixture)

	report := checkConsistency(t, fixture)
	notBuilt := findProblems(report, ProblemIndexNotBuilt)
	if len(notBuilt) != 1 {
		t.Fatalf("Expected one index_not_built problem, got %d: %v", len(notBuilt), report.Problems)
	}
	if !strings.Contains(notBuilt[0].Detail, "/rebuildsubscriberindex") {
		t.Errorf("Expected the detail to say how to fix it, got %q", notBuilt[0].Detail)
	}
}

// TestCheckDBFindsMissingIndexEntries covers both halves of the forward check:
// a subscriber the index does not know, and a device its type set does not.
func TestCheckDBFindsMissingIndexEntries(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	dp := fixture.subscribe(t, "devtoken-1")

	// Take both index entries away, leaving the subscription: a subscriber
	// written before the index existed looks exactly like this.
	if err := fixture.raw.client.Del(context.Background(),
		subscriberIndexKey(ServiceName), typeDeviceSetKey(ServiceName, "apns")).Err(); err != nil {
		t.Fatalf("Could not seed redis: %v", err)
	}

	report := checkConsistency(t, fixture)
	missing := findProblems(report, ProblemMissingIndexEntry)
	if len(missing) != 2 {
		t.Fatalf("Expected the subscriber and the device to be reported, got %d: %v", len(missing), report.Problems)
	}
	subjects := map[string]bool{missing[0].Subject: true, missing[1].Subject: true}
	if !subjects[rebindingSubscriber] {
		t.Errorf("Expected the missing subscriber to be reported, got %v", subjects)
	}
	if !subjects[dp.Name()] {
		t.Errorf("Expected the missing delivery point to be reported, got %v", subjects)
	}
}

// TestCheckDBFindsStaleIndexEntries covers the reverse: entries with nothing
// behind them, which are what make /stats overcount.
func TestCheckDBFindsStaleIndexEntries(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")

	// A subscriber with no devices, and a device with no record: what a rebuild
	// interrupted or run against a moving database can leave.
	if err := fixture.raw.client.ZAdd(context.Background(), subscriberIndexKey(ServiceName),
		redis.Z{Score: 1, Member: "departed-subscriber"}).Err(); err != nil {
		t.Fatalf("Could not seed redis: %v", err)
	}
	if err := fixture.raw.client.SAdd(context.Background(),
		typeDeviceSetKey(ServiceName, "apns"), "apns:no-such-device").Err(); err != nil {
		t.Fatalf("Could not seed redis: %v", err)
	}

	report := checkConsistency(t, fixture)
	stale := findProblems(report, ProblemStaleIndexEntry)
	if len(stale) != 2 {
		t.Fatalf("Expected both stale entries to be reported, got %d: %v", len(stale), report.Problems)
	}
	if report.Subscribers != 2 {
		t.Errorf("Expected the report to count both indexed subscribers, got %d", report.Subscribers)
	}
}

// TestCheckDBIsQuietAboutAHealthyIndex is what gives the four tests above
// meaning: the checks must say nothing about a database this release wrote.
func TestCheckDBIsQuietAboutAHealthyIndex(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.addProviderOfType(t, "fcm", "first.cert")
	fixture.subscribe(t, "devtoken-1")
	fixture.subscribeWithType(t, "fcm", "regid-1")
	fixture.subscribeAs(t, "another-subscriber", "devtoken-2")

	report := checkConsistency(t, fixture)
	if !report.Healthy() {
		t.Errorf("Expected no problems on a database this release wrote, got: %v", report.Problems)
	}
	if report.Subscribers != 2 {
		t.Errorf("Expected 2 indexed subscribers, got %d", report.Subscribers)
	}
}

// dropTheIndex removes everything the index consists of, leaving the
// subscriptions: a database written by a uniqush that predates it.
func dropTheIndex(t *testing.T, fixture *rebindingFixture) {
	t.Helper()

	keys, err := fixture.raw.scanUniqueKeys(ServiceToSubscribersPrefix + "*")
	if err != nil {
		t.Fatalf("Could not list the subscriber indexes: %v", err)
	}
	typeKeys, err := fixture.raw.scanUniqueKeys(ServiceTypeToDeliveryPointsPrefix + "*")
	if err != nil {
		t.Fatalf("Could not list the per-type device sets: %v", err)
	}
	keys = append(keys, typeKeys...)
	if len(keys) > 0 {
		if err := fixture.raw.client.Del(context.Background(), keys...).Err(); err != nil {
			t.Fatalf("Could not drop the index: %v", err)
		}
	}
	clearIndexBuiltMarker(t, fixture)
}

// TestRebuildingFromNoIndexLeavesNothingToReport is the upgrade path: a
// database with subscriptions and no index, one call, and /checkdb is quiet.
func TestRebuildingFromNoIndexLeavesNothingToReport(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.addProviderOfType(t, "fcm", "first.cert")
	fixture.subscribe(t, "devtoken-1")
	fixture.subscribeWithType(t, "fcm", "regid-1")
	fixture.subscribeAs(t, "another-subscriber", "devtoken-2")
	dropTheIndex(t, fixture)

	// The state being repaired must actually be broken, or this proves nothing.
	if before := checkConsistency(t, fixture); before.Healthy() {
		t.Fatal("Expected a database with no index to report problems")
	}

	if err := fixture.client.RebuildSubscriberIndex(); err != nil {
		t.Fatalf("RebuildSubscriberIndex failed: %v", err)
	}

	report := checkConsistency(t, fixture)
	if !report.Healthy() {
		t.Errorf("Expected nothing to report after a rebuild, got: %v", report.Problems)
	}
	if report.Subscribers != 2 {
		t.Errorf("Expected 2 indexed subscribers, got %d", report.Subscribers)
	}
	if got := len(typeMembers(t, fixture, "apns")); got != 2 {
		t.Errorf("Expected 2 apns devices in the rebuilt index, got %d", got)
	}
	if got := len(typeMembers(t, fixture, "fcm")); got != 1 {
		t.Errorf("Expected 1 fcm device in the rebuilt index, got %d", got)
	}
}

// TestRebuildingIsIdempotent is the property that makes it safe to just run it.
func TestRebuildingIsIdempotent(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")
	fixture.subscribeAs(t, "another-subscriber", "devtoken-2")
	dropTheIndex(t, fixture)

	for attempt := 0; attempt < 3; attempt++ {
		if err := fixture.client.RebuildSubscriberIndex(); err != nil {
			t.Fatalf("Rebuild %d failed: %v", attempt, err)
		}
		if report := checkConsistency(t, fixture); !report.Healthy() {
			t.Fatalf("Rebuild %d left problems: %v", attempt, report.Problems)
		}
		if got := len(indexMembers(t, fixture, subscriberIndexKey(ServiceName))); got != 2 {
			t.Fatalf("Rebuild %d indexed %d subscribers, expected 2", attempt, got)
		}
	}
}

// TestRebuildingForgetsWhatIsNoLongerThere covers the delete half of the swap.
//
// A rebuild that only added would leave a service whose subscribers have all
// gone with an index saying otherwise, and /stats would go on counting it --
// which is the failure mode that would be least likely to be noticed, since the
// number would merely be too big.
func TestRebuildingForgetsWhatIsNoLongerThere(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")

	// Index entries with nothing behind them, in the service under test and in
	// one that has nothing at all.
	if err := fixture.raw.client.ZAdd(context.Background(), subscriberIndexKey(ServiceName),
		redis.Z{Score: 1, Member: "departed-subscriber"}).Err(); err != nil {
		t.Fatalf("Could not seed redis: %v", err)
	}
	if err := fixture.raw.client.ZAdd(context.Background(), subscriberIndexKey(OtherServiceName),
		redis.Z{Score: 1, Member: rebindingSubscriber}).Err(); err != nil {
		t.Fatalf("Could not seed redis: %v", err)
	}
	if err := fixture.raw.client.SAdd(context.Background(),
		typeDeviceSetKey(ServiceName, "apns"), "apns:no-such-device").Err(); err != nil {
		t.Fatalf("Could not seed redis: %v", err)
	}

	if err := fixture.client.RebuildSubscriberIndex(); err != nil {
		t.Fatalf("RebuildSubscriberIndex failed: %v", err)
	}

	subscribers := indexMembers(t, fixture, subscriberIndexKey(ServiceName))
	if _, listed := subscribers["departed-subscriber"]; listed {
		t.Error("The rebuild kept a subscriber with no devices")
	}
	if len(subscribers) != 1 {
		t.Errorf("Expected only the real subscriber, got %v", subscribers)
	}
	if got := len(indexMembers(t, fixture, subscriberIndexKey(OtherServiceName))); got != 0 {
		t.Errorf("The rebuild kept an index for a service with no subscribers, holding %d entries", got)
	}
	if got := len(typeMembers(t, fixture, "apns")); got != 1 {
		t.Errorf("Expected only the real device in the apns set, got %d", got)
	}
}

// TestRebuildingMarksTheIndexBuilt is what the whole endpoint is for: after it,
// the fast paths are allowed to trust the index.
func TestRebuildingMarksTheIndexBuilt(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")
	dropTheIndex(t, fixture)

	if err := fixture.client.RebuildSubscriberIndex(); err != nil {
		t.Fatalf("RebuildSubscriberIndex failed: %v", err)
	}

	built, err := fixture.raw.subscriberIndexBuilt()
	if err != nil {
		t.Fatalf("Could not read the built marker: %v", err)
	}
	if !built {
		t.Error("A completed rebuild did not mark the index as built")
	}
}

// TestRebuildingScoresFromTheSubscribeDate keeps the last-seen a rebuild would
// otherwise reset to the moment the operator happened to run it.
func TestRebuildingScoresFromTheSubscribeDate(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")

	subscribed := time.Now().Add(-30 * 24 * time.Hour).Unix()
	dp := fixture.buildDeliveryPoint(t, "devtoken-1")
	dp.VolatileData[push.SubscribeDate] = strconv.FormatInt(subscribed, 10)
	if _, err := fixture.client.AddDeliveryPointToService(ServiceName, rebindingSubscriber, dp); err != nil {
		t.Fatalf("Could not subscribe: %v", err)
	}
	dropTheIndex(t, fixture)

	if err := fixture.client.RebuildSubscriberIndex(); err != nil {
		t.Fatalf("RebuildSubscriberIndex failed: %v", err)
	}

	if score := indexMembers(t, fixture, subscriberIndexKey(ServiceName))[rebindingSubscriber]; score != float64(subscribed) {
		t.Errorf("Expected the rebuilt score to be the subscribe_date %d, got %v", subscribed, score)
	}
}

// TestRebuildingRecoversFromAnInterruptedRebuild covers the staging area.
//
// A rebuild that died partway through leaves staged keys behind, and a second
// run that added to them would keep whatever the first run staged before the
// database changed -- so it would not be idempotent, which is the one property
// this has to have.
func TestRebuildingRecoversFromAnInterruptedRebuild(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")
	dropTheIndex(t, fixture)

	// What a rebuild interrupted just before the rename leaves behind: staged
	// keys holding a subscriber who has since gone.
	staging := SubscriberIndexStagingPrefix + subscriberIndexKey(ServiceName)
	if err := fixture.raw.client.ZAdd(context.Background(), staging,
		redis.Z{Score: 1, Member: "departed-subscriber"}).Err(); err != nil {
		t.Fatalf("Could not seed redis: %v", err)
	}

	if err := fixture.client.RebuildSubscriberIndex(); err != nil {
		t.Fatalf("RebuildSubscriberIndex failed: %v", err)
	}

	subscribers := indexMembers(t, fixture, subscriberIndexKey(ServiceName))
	if _, listed := subscribers["departed-subscriber"]; listed {
		t.Error("The rebuild adopted what an interrupted run had staged")
	}
	if fixture.keyExists(t, staging) {
		t.Error("The rebuild left its staging key behind")
	}
	if report := checkConsistency(t, fixture); !report.Healthy() {
		t.Errorf("Expected nothing to report after the rebuild, got: %v", report.Problems)
	}
}

// TestRebuildingWalksPastOnePage guards the SCAN loop the rebuild depends on,
// the same way the consistency check's test does.
func TestRebuildingWalksPastOnePage(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")

	const subscribers = scanKeysCount * 2
	for i := 0; i < subscribers; i++ {
		fixture.subscribeAs(t, fmt.Sprintf("subscriber-%d", i), "devtoken-1")
	}
	dropTheIndex(t, fixture)

	if err := fixture.client.RebuildSubscriberIndex(); err != nil {
		t.Fatalf("RebuildSubscriberIndex failed: %v", err)
	}

	if got := len(indexMembers(t, fixture, subscriberIndexKey(ServiceName))); got != subscribers {
		t.Errorf("Expected all %d subscribers to be reindexed, got %d", subscribers, got)
	}
}
