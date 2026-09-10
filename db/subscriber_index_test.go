package db

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
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
