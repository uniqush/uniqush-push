package db

import (
	"context"
	"fmt"
	"io"
	"strings"
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

// addProviderBehindTheConflictCheck writes a provider straight to redis, past
// AddPushServiceProviderToService.
//
// This is the only way to reach the state a pre-PR-#201 database can be in --
// two providers of one push service type in one service -- because the check
// that rejects it is the same one this work relaxes.
func (f *rebindingFixture) addProviderBehindTheConflictCheck(t *testing.T, cert string) *push.PushServiceProvider {
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
	if err := f.raw.SetPushServiceProvider(psp); err != nil {
		t.Fatalf("Could not write the provider: %v", err)
	}
	if err := f.raw.AddPushServiceProviderToService(ServiceName, psp.Name()); err != nil {
		t.Fatalf("Could not add the provider to the service: %v", err)
	}
	return psp
}

func (f *rebindingFixture) buildDeliveryPoint(t *testing.T, devtoken string) *push.DeliveryPoint {
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
	return dp
}

// trySubscribe subscribes and hands back the error, for the cases where being
// refused is the expected outcome.
func (f *rebindingFixture) trySubscribe(t *testing.T, dp *push.DeliveryPoint) (*push.PushServiceProvider, error) {
	t.Helper()
	return f.client.AddDeliveryPointToService(ServiceName, rebindingSubscriber, dp)
}

func (f *rebindingFixture) subscribe(t *testing.T, devtoken string) *push.DeliveryPoint {
	t.Helper()

	dp := f.buildDeliveryPoint(t, devtoken)
	if _, err := f.trySubscribe(t, dp); err != nil {
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

// TestProviderIsDerivedFromServiceAndType is the Phase 1 test, and the point of
// the whole exercise.
//
// The device is bound to a provider name that hashes the provider's
// credentials. Here the provider is replaced with one carrying a different
// certificate -- so a different name -- exactly as switching an APNs service
// from a certificate to a .p8 signing key would. The device must still resolve,
// because its provider is derived from the service and the push service type
// rather than read from the stale binding.
func TestProviderIsDerivedFromServiceAndType(t *testing.T) {
	fixture := newRebindingFixture(t)
	original := fixture.addProvider(t, "first.cert")
	dp := fixture.subscribe(t, "devtoken-1")

	// The binding still names the original provider, and is now stale.
	if err := fixture.client.RemovePushServiceProviderFromService(ServiceName, original); err != nil {
		t.Fatalf("Could not remove the original provider: %v", err)
	}
	replacement := fixture.addProvider(t, "second.cert")
	if replacement.Name() == original.Name() {
		t.Fatal("The two providers must have different names for this test to mean anything")
	}

	pairs := fixture.pairs(t)
	if len(pairs) != 1 {
		t.Fatalf("Expected the device to resolve to the replacement provider, got %d pairs", len(pairs))
	}
	if got := pairs[0].PushServiceProvider.Name(); got != replacement.Name() {
		t.Errorf("Expected the replacement provider %q, got %q", replacement.Name(), got)
	}
	if pairs[0].DeliveryPoint.Name() != dp.Name() {
		t.Errorf("Expected delivery point %q, got %q", dp.Name(), pairs[0].DeliveryPoint.Name())
	}

	// The stale binding is untouched, so a downgrade still works.
	stored, err := fixture.raw.GetPushServiceProviderNameByServiceDeliveryPoint(ServiceName, dp.Name())
	if err != nil {
		t.Fatalf("Could not read the stored binding: %v", err)
	}
	if stored != original.Name() {
		t.Errorf("Expected the stored binding to be left alone for rollback, got %q", stored)
	}
}

// TestDeliveryPointWithNoProviderOfItsTypeIsSkipped checks the derivation fails
// closed.
//
// A service whose provider of the right type is missing must skip the device,
// not fall back to a provider of some other type -- pushing an APNs payload
// through FCM would be a memorable bug.
func TestDeliveryPointWithNoProviderOfItsTypeIsSkipped(t *testing.T) {
	fixture := newRebindingFixture(t)
	if err := fixture.psm.RegisterPushServiceType(&rebindingPushServiceType{name: "fcm"}); err != nil {
		t.Fatalf("Could not register a second push service type: %v", err)
	}

	provider := fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")

	// Swap the service's only provider for one of a different type.
	if err := fixture.client.RemovePushServiceProviderFromService(ServiceName, provider); err != nil {
		t.Fatalf("Could not remove the provider: %v", err)
	}
	fcmPSP, err := fixture.psm.BuildPushServiceProviderFromMap(map[string]string{
		"pushservicetype": "fcm",
		"service":         ServiceName,
		"cert":            "irrelevant",
		"key":             "irrelevant",
	})
	if err != nil {
		t.Fatalf("Could not build an fcm provider: %v", err)
	}
	if err := fixture.client.AddPushServiceProviderToService(ServiceName, fcmPSP); err != nil {
		t.Fatalf("Could not add the fcm provider: %v", err)
	}

	if pairs := fixture.pairs(t); len(pairs) != 0 {
		t.Errorf("Expected an apns device with only an fcm provider available to be skipped, got %d pairs "+
			"(pushing an APNs payload through FCM would be worse than not pushing)", len(pairs))
	}
}

// TestAmbiguousProvidersFallBackToTheStoredBinding covers data written before
// one-provider-per-type was enforced.
//
// srv-2-psp is an unordered set, so picking a match arbitrarily would resolve
// differently between reads and the device would appear to work intermittently.
// The stored binding records what the device subscribed to, which is the
// closest thing to an intended answer.
func TestAmbiguousProvidersFallBackToTheStoredBinding(t *testing.T) {
	fixture := newRebindingFixture(t)
	original := fixture.addProvider(t, "first.cert")
	dp := fixture.subscribe(t, "devtoken-1")

	// The state a pre-PR-#201 database can be in.
	fixture.addProviderBehindTheConflictCheck(t, "second.cert")

	// Repeated, because the failure this guards against is nondeterministic:
	// picking arbitrarily from an unordered set passes roughly half the time.
	for i := 0; i < 20; i++ {
		pairs := fixture.pairs(t)
		if len(pairs) != 1 {
			t.Fatalf("Expected one pair, got %d", len(pairs))
		}
		if got := pairs[0].PushServiceProvider.Name(); got != original.Name() {
			t.Fatalf("Read %d resolved to %q, but the device is bound to %q", i, got, original.Name())
		}
	}

	// With the binding gone there is genuinely nothing to choose between them,
	// and guessing would be worse than skipping.
	if err := fixture.raw.RemovePushServiceProviderOfServiceDeliveryPoint(ServiceName, dp.Name()); err != nil {
		t.Fatalf("Could not remove the binding: %v", err)
	}
	if pairs := fixture.pairs(t); len(pairs) != 0 {
		t.Errorf("Expected an unresolvable ambiguity to be skipped rather than guessed, got %d pairs", len(pairs))
	}
}

// TestSubscribingResolvesTheProviderTheSameWayReadingDoes is the write path's
// half of the derivation.
//
// Subscribing used to take the first provider of a matching type out of
// srv-2-psp, which is an unordered set. Fixing only the read path would leave
// the two disagreeing on exactly the data that made the derivation ambiguous in
// the first place -- and the binding written at subscribe time is the
// tie-breaker the read path falls back on, so the disagreement would stick.
func TestSubscribingResolvesTheProviderTheSameWayReadingDoes(t *testing.T) {
	fixture := newRebindingFixture(t)
	original := fixture.addProvider(t, "first.cert")
	fixture.subscribe(t, "devtoken-1")
	fixture.addProviderBehindTheConflictCheck(t, "second.cert")

	// A device this service already knows keeps the provider it was subscribed
	// to. Repeated because picking from an unordered set passes about half the
	// time.
	for i := 0; i < 20; i++ {
		psp, err := fixture.trySubscribe(t, fixture.buildDeliveryPoint(t, "devtoken-1"))
		if err != nil {
			t.Fatalf("Re-subscribe %d failed: %v", i, err)
		}
		if psp.Name() != original.Name() {
			t.Fatalf("Re-subscribe %d moved the device to %q, from %q", i, psp.Name(), original.Name())
		}
	}

	// A device with no binding has nothing to choose between them, so the
	// subscribe is refused rather than resolved by coin toss.
	fresh := fixture.buildDeliveryPoint(t, "devtoken-2")
	psp, err := fixture.trySubscribe(t, fresh)
	if err == nil {
		t.Fatalf("Expected an ambiguous subscribe to be refused, got provider %q", psp.Name())
	}
	if !strings.Contains(err.Error(), "/checkdb") {
		t.Errorf("The refusal should point at the tool that explains it, got: %v", err)
	}

	// Refused means nothing was written: a half-subscribed device would be
	// invisible to /subscriptions and undeletable by /unsubscribe.
	if fixture.keyExists(t, DeliveryPointPrefix+fresh.Name()) {
		t.Error("A refused subscribe left the delivery point record behind")
	}
	if fixture.keyExists(t, DeliveryPointCounterPrefix+fresh.Name()) {
		t.Error("A refused subscribe left a counter behind")
	}
	if pairs := fixture.pairs(t); len(pairs) != 1 {
		t.Errorf("Expected the one resolvable device, got %d pairs", len(pairs))
	}
}

// TestSubscribingWithNoProviderOfItsTypeIsRefused pins the two error strings
// callers have always seen, which the refactor to a shared resolver could
// silently have changed.
func TestSubscribingWithNoProviderOfItsTypeIsRefused(t *testing.T) {
	fixture := newRebindingFixture(t)

	if _, err := fixture.trySubscribe(t, fixture.buildDeliveryPoint(t, "devtoken-1")); err == nil {
		t.Fatal("Expected subscribing to a service with no providers to fail")
	} else if !strings.Contains(err.Error(), "Cannot Find Service") {
		t.Errorf("Expected the service-not-found error, got: %v", err)
	}

	// A provider of a different push service type is not a provider for this
	// device.
	if err := fixture.psm.RegisterPushServiceType(&rebindingPushServiceType{name: "gcm"}); err != nil {
		t.Fatalf("Could not register a second push service type: %v", err)
	}
	fixture.addProvider(t, "first.cert")

	other, err := fixture.psm.BuildDeliveryPointFromMap(map[string]string{
		"pushservicetype": "gcm",
		"service":         ServiceName,
		"subscriber":      rebindingSubscriber,
		"devtoken":        "devtoken-gcm",
	})
	if err != nil {
		t.Fatalf("Could not build a gcm delivery point: %v", err)
	}
	if _, err := fixture.trySubscribe(t, other); err == nil {
		t.Fatal("Expected subscribing a gcm device to an apns-only service to fail")
	} else if !strings.Contains(err.Error(), "Cannot Find Push Service Provider with Type gcm") {
		t.Errorf("Expected the type-not-found error, got: %v", err)
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

// TestSubscribingSurvivesADanglingProviderName covers the write path's version
// of the nil-logger trap.
//
// AddDeliveryPointToService takes no logger, so the provider lookup underneath
// it is called without one. That lookup logs in exactly one place -- a provider
// name in srv-2-psp with no record behind it -- which is a branch no healthy
// database reaches, so a nil logger there would have turned a database
// inconsistency into a panic on /subscribe.
func TestSubscribingSurvivesADanglingProviderName(t *testing.T) {
	fixture := newRebindingFixture(t)
	good := fixture.addProvider(t, "first.cert")

	// A name in the service's set with no record behind it: an interrupted
	// /addpsp, and what /checkdb reports as a dangling provider.
	if err := fixture.raw.AddPushServiceProviderToService(ServiceName, "apns:0000000000000000000000000000000000000000"); err != nil {
		t.Fatalf("Could not add a dangling provider name: %v", err)
	}

	// Subscribing must skip the dangling name and use the real provider.
	psp, err := fixture.trySubscribe(t, fixture.buildDeliveryPoint(t, "devtoken-1"))
	if err != nil {
		t.Fatalf("Subscribe failed with a dangling provider name in the service: %v", err)
	}
	if psp.Name() != good.Name() {
		t.Errorf("Expected the subscribe to resolve to %q, got %q", good.Name(), psp.Name())
	}

	// And with no usable provider left, it is an error rather than a panic.
	if err := fixture.raw.RemovePushServiceProviderFromService(ServiceName, good.Name()); err != nil {
		t.Fatalf("Could not remove the good provider: %v", err)
	}
	if _, err := fixture.trySubscribe(t, fixture.buildDeliveryPoint(t, "devtoken-2")); err == nil {
		t.Error("Expected subscribing with only a dangling provider name to fail")
	}
}
