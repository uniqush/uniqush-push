package db

import (
	"testing"
)

// Tests for removing every device a subscriber has in one call.
//
// These run against a real redis and are skipped when there is none, like the
// rest of the package's tests.

// TestRemoveAllDeliveryPointsClearsTheSubscriber is the case the feature exists
// for: an account is deleted, and the application does not know or care which
// devices it had.
func TestRemoveAllDeliveryPointsClearsTheSubscriber(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	first := fixture.subscribe(t, "devtoken-1")
	second := fixture.subscribe(t, "devtoken-2")

	removed, err := fixture.client.RemoveAllDeliveryPointsFromService(ServiceName, rebindingSubscriber)
	if err != nil {
		t.Fatalf("Could not remove the subscriber's delivery points: %v", err)
	}
	if removed != 2 {
		t.Errorf("Expected 2 delivery points to be removed, got %d", removed)
	}

	if pairs := fixture.pairs(t); len(pairs) != 0 {
		t.Errorf("Expected the subscriber to have no devices left, got %d", len(pairs))
	}
	// The same bookkeeping a single unsubscribe does: the pointer, the counter
	// and the device's own record all go, or the database grows debris that
	// only /checkdb will ever find.
	for _, dp := range []string{first.Name(), second.Name()} {
		if fixture.keyExists(t, DeliveryPointPrefix+dp) {
			t.Errorf("The record for %s was left behind", dp)
		}
		if fixture.keyExists(t, DeliveryPointCounterPrefix+dp) {
			t.Errorf("The counter for %s was left behind", dp)
		}
		if fixture.keyExists(t, ServiceDeliveryPointToPushServiceProviderPrefix+ServiceName+":"+dp) {
			t.Errorf("The provider binding for %s was left behind", dp)
		}
	}
}

// TestRemoveAllDeliveryPointsIsSuccessWhenThereAreNone covers what the issue
// asked for in as many words.
//
// An account-deletion path that had to treat "this subscriber already had
// nothing" as an error would have to special-case it at every call site.
func TestRemoveAllDeliveryPointsIsSuccessWhenThereAreNone(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")

	removed, err := fixture.client.RemoveAllDeliveryPointsFromService(ServiceName, "nobody-here")
	if err != nil {
		t.Errorf("Removing the devices of a subscriber with none should succeed, got: %v", err)
	}
	if removed != 0 {
		t.Errorf("Expected 0 delivery points to be removed, got %d", removed)
	}
}

// TestRemoveAllDeliveryPointsLeavesOtherSubscribersAlone is the blast-radius
// test.
//
// This deletes in bulk from a name, so the thing worth proving is that the name
// bounds it.
func TestRemoveAllDeliveryPointsLeavesOtherSubscribersAlone(t *testing.T) {
	fixture := newRebindingFixture(t)
	psp := fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")

	// A second subscriber in the same service, and a device of their own.
	other, err := fixture.psm.BuildDeliveryPointFromMap(map[string]string{
		"pushservicetype": "apns",
		"service":         ServiceName,
		"subscriber":      "other-subscriber",
		"devtoken":        "devtoken-other",
	})
	if err != nil {
		t.Fatalf("Could not build the other subscriber's delivery point: %v", err)
	}
	if _, addErr := fixture.client.AddDeliveryPointToService(ServiceName, "other-subscriber", other); addErr != nil {
		t.Fatalf("Could not subscribe the other subscriber: %v", addErr)
	}

	if _, removeErr := fixture.client.RemoveAllDeliveryPointsFromService(ServiceName, rebindingSubscriber); removeErr != nil {
		t.Fatalf("Could not remove the subscriber's delivery points: %v", removeErr)
	}

	names, err := fixture.raw.GetDeliveryPointsNameByServiceSubscriber(ServiceName, "other-subscriber")
	if err != nil {
		t.Fatalf("Could not list the other subscriber's delivery points: %v", err)
	}
	if len(names[ServiceName]) != 1 {
		t.Errorf("Expected the other subscriber to keep their device, got %v", names[ServiceName])
	}
	if !fixture.keyExists(t, DeliveryPointPrefix+other.Name()) {
		t.Error("The other subscriber's delivery point record was removed")
	}
	// And the provider is untouched: this removes subscriptions, not services.
	if !fixture.keyExists(t, PushServiceProviderPrefix+psp.Name()) {
		t.Error("The service's provider was removed")
	}
}

// TestRemoveAllDeliveryPointsRemovesOrphans covers the reason this does not go
// through GetPushServiceProviderDeliveryPointPairs.
//
// That path resolves each delivery point's provider and skips the ones whose
// provider has gone -- so building this on top of it would have left behind
// exactly the debris an operator most wants cleared, and reported success.
func TestRemoveAllDeliveryPointsRemovesOrphans(t *testing.T) {
	fixture := newRebindingFixture(t)
	psp := fixture.addProvider(t, "first.cert")
	dp := fixture.subscribe(t, "devtoken-1")

	if err := fixture.client.RemovePushServiceProviderFromService(ServiceName, psp); err != nil {
		t.Fatalf("Could not remove the provider: %v", err)
	}
	// Nothing can resolve a provider for this device now.
	if pairs := fixture.pairs(t); len(pairs) != 0 {
		t.Fatalf("Expected no readable pairs once the provider is gone, got %d", len(pairs))
	}

	removed, err := fixture.client.RemoveAllDeliveryPointsFromService(ServiceName, rebindingSubscriber)
	if err != nil {
		t.Fatalf("Could not remove the subscriber's delivery points: %v", err)
	}
	if removed != 1 {
		t.Errorf("Expected the orphaned delivery point to be removed, got %d", removed)
	}
	if fixture.keyExists(t, DeliveryPointPrefix+dp.Name()) {
		t.Error("The orphaned delivery point's record was left behind")
	}
}
