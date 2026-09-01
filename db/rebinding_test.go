package db

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/push"
)

// Tests for how a delivery point is bound to its push service provider.
//
// The binding used to be both authoritative and fragile: a delivery point was
// stored against a provider's name, that name hashes the provider's fixed data,
// and the read path deleted any delivery point whose provider it could not
// find. So changing a provider's credentials silently unsubscribed the service.
//
// These tests run against a real redis. They are skipped when there is none,
// which is why CI runs a redis service container -- without it they assert
// nothing and the package still reports ok.

const rebindingSubscriber = "rebinding_test_subscriber"

// rebindingPushServiceType is a minimal push service type that can build both
// providers and delivery points.
//
// The mock in srv/apns/http_api/mocks panics on BuildDeliveryPointFromMap, and
// every test here needs delivery points, which is the whole subject.
type rebindingPushServiceType struct {
	name string
}

var _ push.PushServiceType = &rebindingPushServiceType{}

func (t *rebindingPushServiceType) Name() string { return t.name }

// BuildPushServiceProviderFromMap mirrors the shape of a certificate-based APNs
// provider: the credential is part of the fixed data, and therefore part of the
// provider's identity. That is the arrangement under test.
func (t *rebindingPushServiceType) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
	for key, value := range kv {
		switch key {
		case "service", "cert", "key":
			psp.FixedData[key] = value
		case "addr", "bundleid":
			psp.VolatileData[key] = value
		}
	}
	if psp.FixedData["service"] == "" {
		return fmt.Errorf("NoService")
	}
	return nil
}

func (t *rebindingPushServiceType) BuildDeliveryPointFromMap(kv map[string]string, dp *push.DeliveryPoint) error {
	if err := dp.AddCommonData(kv); err != nil {
		return err
	}
	devtoken, ok := kv["devtoken"]
	if !ok || devtoken == "" {
		return fmt.Errorf("NoDevToken")
	}
	dp.FixedData["devtoken"] = devtoken
	return nil
}

func (t *rebindingPushServiceType) Push(*push.PushServiceProvider, <-chan *push.DeliveryPoint, chan<- *push.Result, *push.Notification) {
}
func (t *rebindingPushServiceType) Preview(*push.Notification) ([]byte, push.Error) { return nil, nil }
func (t *rebindingPushServiceType) SetErrorReportChan(chan<- push.Error)            {}
func (t *rebindingPushServiceType) SetPushServiceConfig(*push.PushServiceConfig)    {}
func (t *rebindingPushServiceType) Finalize()                                       {}

// rebindingFixture bundles helpers for rebinding-related tests (DB client, push service manager, and raw redis access).
type rebindingFixture struct {
	client PushDatabase
	psm    *push.PushServiceManager
	raw    *PushRedisDB
}

func newRebindingFixture(t *testing.T) *rebindingFixture {
	t.Helper()

	client := connectDatabaseAndClearRedisData(t)
	psm := initializePushServiceManagerForTest()
	if err := psm.RegisterPushServiceType(&rebindingPushServiceType{name: "apns"}); err != nil {
		t.Fatalf("Could not register the test push service type: %v", err)
	}
	return &rebindingFixture{
		client: client,
		psm:    psm,
		raw:    client.(*pushDatabaseOpts).db.(*PushRedisDB),
	}
}

// addProvider registers a provider whose certificate path is cert, so tests can
// produce a second provider with different fixed data.
func (f *rebindingFixture) addProvider(t *testing.T, cert string) *push.PushServiceProvider {
	t.Helper()

	psp, err := f.psm.BuildPushServiceProviderFromMap(map[string]string{
		"pushservicetype": "apns",
		"service":         ServiceName,
		"cert":            cert,
		"key":             cert + ".key",
	})
	if err != nil {
		t.Fatalf("Could not build a provider: %v", err)
	}
	if err := f.client.AddPushServiceProviderToService(ServiceName, psp); err != nil {
		t.Fatalf("Could not add the provider: %v", err)
	}
	return psp
}

func (f *rebindingFixture) subscribe(t *testing.T, devtoken string) *push.DeliveryPoint {
	t.Helper()

	dp, err := f.psm.BuildDeliveryPointFromMap(map[string]string{
		"pushservicetype": "apns",
		"service":         ServiceName,
		"subscriber":      rebindingSubscriber,
		"devtoken":        devtoken,
	})
	if err != nil {
		t.Fatalf("Could not build a delivery point: %v", err)
	}
	if _, err := f.client.AddDeliveryPointToService(ServiceName, rebindingSubscriber, dp); err != nil {
		t.Fatalf("Could not subscribe the delivery point: %v", err)
	}
	return dp
}

func (f *rebindingFixture) pairs(t *testing.T) []PushServiceProviderDeliveryPointPair {
	t.Helper()
	pairs, err := f.client.GetPushServiceProviderDeliveryPointPairs(ServiceName, rebindingSubscriber, nil, nil)
	if err != nil {
		t.Fatalf("Could not read delivery point pairs: %v", err)
	}
	return pairs
}

func (f *rebindingFixture) keyExists(t *testing.T, key string) bool {
	t.Helper()
	count, err := f.raw.client.Exists(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("Could not check redis key %q: %v", key, err)
	}
	return count > 0
}

// TestReadDoesNotDeleteDeliveryPointsWhenTheProviderIsGone is the Phase 0
// regression test, and the reason any of this work exists.
//
// Removing a provider used to unsubscribe every device in the service on the
// next read, unrecoverably. Nothing in the API said so: /rmpsp reported success
// and the devices disappeared later, during an unrelated push.
func TestReadDoesNotDeleteDeliveryPointsWhenTheProviderIsGone(t *testing.T) {
	fixture := newRebindingFixture(t)
	psp := fixture.addProvider(t, "first.cert")
	dp := fixture.subscribe(t, "devtoken-1")

	if pairs := fixture.pairs(t); len(pairs) != 1 {
		t.Fatalf("Expected the device to be readable before the provider is removed, got %d", len(pairs))
	}

	if err := fixture.client.RemovePushServiceProviderFromService(ServiceName, psp); err != nil {
		t.Fatalf("Could not remove the provider: %v", err)
	}

	// The read finds no provider. It must skip the device, not delete it.
	if pairs := fixture.pairs(t); len(pairs) != 0 {
		t.Errorf("Expected no pairs while the provider is missing, got %d", len(pairs))
	}

	if !fixture.keyExists(t, DeliveryPointPrefix+dp.Name()) {
		t.Error("The delivery point was deleted by a read. A read must not destroy data, " +
			"and this is what made /rmpsp unrecoverable.")
	}
	if !fixture.keyExists(t, ServiceSubscriberToDeliveryPointsPrefix+ServiceName+":"+rebindingSubscriber) {
		t.Error("The subscriber's delivery point set was deleted by a read")
	}

	// The real test of "not destroyed": putting the provider back brings the
	// device back, with no re-subscribe.
	fixture.addProvider(t, "first.cert")
	pairs := fixture.pairs(t)
	if len(pairs) != 1 {
		t.Fatalf("Expected the device to return once the provider was restored, got %d", len(pairs))
	}
	if pairs[0].DeliveryPoint.Name() != dp.Name() {
		t.Errorf("Expected delivery point %q, got %q", dp.Name(), pairs[0].DeliveryPoint.Name())
	}
}

// TestOrphanedDeliveryPointIsFullyTornDown covers the other half of Phase 0.
//
// A name in a subscriber's set whose delivery.point record has gone is real
// debris and is still cleaned up -- there is no data left to lose. The old code
// deleted delivery.point:<dp>, which was already absent, and left both the set
// membership and the counter behind: a garbage collector that created garbage.
func TestOrphanedDeliveryPointIsFullyTornDown(t *testing.T) {
	fixture := newRebindingFixture(t)
	fixture.addProvider(t, "first.cert")
	dp := fixture.subscribe(t, "devtoken-1")
	survivor := fixture.subscribe(t, "devtoken-2")

	counterKey := DeliveryPointCounterPrefix + dp.Name()
	if !fixture.keyExists(t, counterKey) {
		t.Fatal("Expected a subscriber counter for the delivery point")
	}

	// Delete the record out from under the subscriber set, which is the state
	// this cleanup exists for.
	if err := fixture.raw.RemoveDeliveryPoint(dp.Name()); err != nil {
		t.Fatalf("Could not remove the delivery point record: %v", err)
	}

	pairs := fixture.pairs(t)
	if len(pairs) != 1 {
		t.Fatalf("Expected only the surviving device, got %d", len(pairs))
	}
	if pairs[0].DeliveryPoint.Name() != survivor.Name() {
		t.Errorf("Expected the surviving delivery point %q, got %q", survivor.Name(), pairs[0].DeliveryPoint.Name())
	}

	if fixture.keyExists(t, counterKey) {
		t.Error("The subscriber counter for the orphaned delivery point was leaked")
	}
	if fixture.keyExists(t, ServiceDeliveryPointToPushServiceProviderPrefix+ServiceName+":"+dp.Name()) {
		t.Error("The provider binding for the orphaned delivery point was left behind")
	}

	// The surviving device must not have been caught up in the cleanup.
	if !fixture.keyExists(t, DeliveryPointPrefix+survivor.Name()) {
		t.Error("The surviving delivery point was removed")
	}
	if !fixture.keyExists(t, DeliveryPointCounterPrefix+survivor.Name()) {
		t.Error("The surviving delivery point's counter was removed")
	}

	// And the orphan is gone from the set, so it is not reported again.
	names, err := fixture.raw.GetDeliveryPointsNameByServiceSubscriber(ServiceName, rebindingSubscriber)
	if err != nil {
		t.Fatalf("Could not list delivery points: %v", err)
	}
	for _, name := range names[ServiceName] {
		if name == dp.Name() {
			t.Error("The orphaned delivery point is still in the subscriber's set")
		}
	}
}

// TestOrphanCleanupToleratesANilLoggerOnTheErrorPath is a regression test for a
// panic that only a redis failure could trigger.
//
// The orphan teardown logs nothing when it succeeds and calls logger.Errorf
// when it does not, so a nil logger sailed through every passing test and would
// have panicked the first time redis refused one of the two commands -- on the
// error path, in a goroutine serving a push. The read path is reachable with a
// nil logger by design (tests use it to mean "do not care"), which made this
// reachable too.
//
// WRONGTYPE is the cheapest way to make redis refuse: SREM against a key
// holding a string fails, and the teardown's first statement is an SREM.
func TestOrphanCleanupToleratesANilLoggerOnTheErrorPath(t *testing.T) {
	fixture := newRebindingFixture(t)

	key := ServiceSubscriberToDeliveryPointsPrefix + ServiceName + ":" + rebindingSubscriber
	if err := fixture.raw.client.Set(context.Background(), key, "not-a-set", 0).Err(); err != nil {
		t.Fatalf("Could not seed redis: %v", err)
	}

	// The assertion is that this returns at all.
	fixture.raw.RemoveMissingDeliveryPointFromServiceSubscriber(
		ServiceName, rebindingSubscriber, "a-delivery-point-that-is-not-there", nil)
}

// TestOrDiscardNeverReturnsNil states the rule the fix above relies on: every
// exported method that accepts a logger normalises it once, and nothing below
// checks again.
func TestOrDiscardNeverReturnsNil(t *testing.T) {
	if orDiscard(nil) == nil {
		t.Error("orDiscard(nil) must return a usable logger")
	}
	// A logger that was supplied is handed back untouched, so normalising cannot
	// silently swallow a caller's logs.
	supplied := log.NewLogger(io.Discard, "[test]", log.LOGLEVEL_INFO)
	if got := orDiscard(supplied); got != supplied {
		t.Error("orDiscard replaced a logger that was already there")
	}
}
