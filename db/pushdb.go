/*
 * Copyright 2011 Nan Deng
 *           2017 Victor Lang
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package db contains the database implementation for uniqush-push, for managing Push Service Providers, Delivery Points, services, etc. Currently, this only supports redis.
package db

import (
	"errors"
	"fmt"
	"sync"

	"github.com/redis/go-redis/v9"
	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/push"
)

const (
	// DeliveryPointID is the internal identifier for a delivery point(subscription) in Subscription() responses, which may be returned to clients if include_delivery_point_ids=1.
	DeliveryPointID = "delivery_point_id"
)

// PushServiceProviderDeliveryPointPair is a pair of a push service provider and a delivery point belonging to that PSP.
type PushServiceProviderDeliveryPointPair struct {
	PushServiceProvider *push.PushServiceProvider
	DeliveryPoint       *push.DeliveryPoint
}

// isErrCausedByMissingKey checks whether an error means the redis key was absent
// rather than that something went wrong.
//
// This used to string-match "redis: nil" against err.Error(), under a TODO
// asking for exactly this fix. That happened to keep working because the
// sentinel's text never changed, but it would also have matched an unrelated
// error that merely contained the phrase, and it silently depended on an
// implementation detail of the client. redis.Nil is a comparable constant, and
// errors.Is handles the wrapping that pushredisdb.go does with %v/%w.
func isErrCausedByMissingKey(err error) bool {
	return errors.Is(err, redis.Nil)
}

// PushDatabase is an interface for any db implementation that uniqush-push can use. Currently, redis is the only supported database.
type PushDatabase interface {

	// The push service provider may by anonymous whose Name is empty string
	// For anonymous push service provider, it will be added to database
	// and its Name will be set
	RemovePushServiceProviderFromService(service string, pushServiceProvider *push.PushServiceProvider) error

	// The push service provider may by anonymous whose Name is empty string
	// For anonymous push service provider, it will be added to database
	// and its Name will be set
	AddPushServiceProviderToService(service string,
		pushServiceProvider *push.PushServiceProvider) error

	ModifyPushServiceProvider(psp *push.PushServiceProvider) error

	// Get a set of all push service providers
	GetPushServiceProviderConfigs() ([]*push.PushServiceProvider, error)

	// RebuildServiceSet() ensures that a set of all PSPs exists. After FixServiceSet is called on a pre-existing uniqush setup, the set of all PSPs will be accurate (Even after calls to AddPushServiceProvider/RemovePushServiceProvider)
	RebuildServiceSet() error

	// The delivery point may be anonymous whose Name is empty string
	// For anonymous delivery point, it will be added to database and its Name will be set
	// Return value: selected push service provider, error
	AddDeliveryPointToService(service string,
		subscriber string,
		deliveryPoint *push.DeliveryPoint) (*push.PushServiceProvider, error)

	// The delivery point may be anonymous whose Name is empty string
	// For anonymous delivery point, it will be added to database and its Name will be set
	// Return value: selected push service provider, error
	RemoveDeliveryPointFromService(service string,
		subscriber string,
		deliveryPoint *push.DeliveryPoint) error

	ModifyDeliveryPoint(dp *push.DeliveryPoint) error

	// GetPushServiceProviderDeliveryPointPairs takes a logger because it can
	// skip delivery points -- one whose provider has been removed, say -- and a
	// device that silently stops receiving pushes is the hardest kind of
	// failure to diagnose from the outside. GetSubscriptions takes one for the
	// same reason.
	GetPushServiceProviderDeliveryPointPairs(service string, subscriber string, dpNamesRequested []string, logger log.Logger) ([]PushServiceProviderDeliveryPointPair, error)

	GetSubscriptions(services []string, user string, logger log.Logger) ([]map[string]string, error)

	// CheckConsistency scans the database and reports what does not add up. It
	// is read-only and changes nothing, including the problems it finds.
	CheckConsistency() (*ConsistencyReport, error)

	FlushCache() error
}

type pushDatabaseOpts struct {
	db pushRawDatabase
	/* TODO Fine grained locks */
	dblock sync.RWMutex
}

/*
func NewPushDatabaseOpts(conf *DatabaseConfig) (PushDatabase, error) {
	var err error
	f := new(pushDatabaseOpts)
	udb, err := newPushRedisDB(conf)
	if udb == nil || err != nil {
		return nil, err
	}
	f.db = NewCachedUniqushDatabase(udb, udb, conf)
	if f.db == nil {
		return nil, errors.New("Cannot create cached database")
	}
	return f, nil
}
*/

// NewPushDatabaseWithoutCache creates a push database implementation communicating with redis without any in-memory caching
func NewPushDatabaseWithoutCache(conf *DatabaseConfig) (PushDatabase, error) {
	var err error
	f := new(pushDatabaseOpts)
	f.db, err = newPushRedisDB(conf)
	if f.db == nil || err != nil {
		return nil, fmt.Errorf("Failed to create database: %v", err)
	}
	return f, nil
}

// FlushCache will save PSPs and subscriptions. NOTE: This is unnecessary if the database used is configured to auto-save.
func (f *pushDatabaseOpts) FlushCache() error {
	f.dblock.Lock()
	defer f.dblock.Unlock()
	return f.db.FlushCache()
}

func (f *pushDatabaseOpts) RemovePushServiceProviderFromService(service string, pushServiceProvider *push.PushServiceProvider) error {
	name := pushServiceProvider.Name()
	if name == "" {
		return errors.New("InvalidPushServiceProvider")
	}
	db := f.db
	f.dblock.Lock()
	defer f.dblock.Unlock()
	err := db.RemovePushServiceProviderFromService(service, name)
	if err != nil {
		return fmt.Errorf("Error removing the psp: %v", err)
	}
	err = db.RemovePushServiceProvider(name)
	if err != nil {
		return fmt.Errorf("Error removing the psp label: %v", err)
	}
	return nil
}

func (f *pushDatabaseOpts) AddPushServiceProviderToService(service string, pushServiceProvider *push.PushServiceProvider) error {
	if pushServiceProvider == nil {
		return nil
	}
	name := pushServiceProvider.Name()
	if len(name) == 0 {
		return errors.New("InvalidPushServiceProvider")
	}
	f.dblock.Lock()
	defer f.dblock.Unlock()

	/*
	 * Patch by Victor Lang (PR #201)
	 * Before adding a new psp to a service, try to verify that there is no redundant PSP of same type (GCM, FCM, APNS, or ADM)
	 * that was already created for the given service.
	 * Currently, redundant psp to service will result in problem when API clients attempt to subscribe and push later.
	 *
	 * However, allow /addpsp to be used to update an existing PSP, as long as none of the fixed data for the PSP changes
	 */
	expsps, err := f.db.GetPushServiceProvidersByService(service)
	if err != nil {
		return fmt.Errorf("Error in AddPushServiceProviderToService querying list of PSPs for service %s: %v", service, err)
	}

	for _, pspitem := range expsps {
		pushpsp, perr := f.db.GetPushServiceProvider(pspitem)
		if perr != nil {
			return fmt.Errorf("Error in AddPushServiceProviderToService retrieving existing PSP %s for service %s with name: %v", pspitem, service, perr)
		}
		// Check if the existing PSP has the same push service type
		if pushpsp.PushServiceName() == pushServiceProvider.PushServiceName() {
			/*
			 * The service already has a PSP of the same push service type.
			 *
			 * The same fixed data are allowed under this situation in case the user wants to update the changeable VolatileData of a PSP,
			 * but we disallow adding a different PSP of the same type.
			 *
			 * Because the psp's fixed data currently is used to generate a unique pushpeer name,
			 * we directly compare the Name() of pushpeer and reject the new PSP if the name is different.
			 */
			if pushpsp.PushPeer.Name() != pushServiceProvider.PushPeer.Name() {
				return fmt.Errorf(
					"A different PSP for service %s already exists with different fixed data as push service type %s (It has a separate subscriber list). Please double check the list of current PSPs with the /psps API. Note that this error could be worked around by removing the old PSP, but that would delete subscriptions",
					service,
					pushServiceProvider.PushServiceName(),
				)
			}
		}
	}

	e := f.db.SetPushServiceProvider(pushServiceProvider)
	if e != nil {
		return fmt.Errorf("Error associating psp with name: %v", e)
	}
	return f.db.AddPushServiceProviderToService(service, pushServiceProvider.Name())
}

func (f *pushDatabaseOpts) AddDeliveryPointToService(service string,
	subscriber string,
	deliveryPoint *push.DeliveryPoint) (*push.PushServiceProvider, error) {
	if deliveryPoint == nil {
		return nil, nil
	}
	if len(deliveryPoint.Name()) == 0 {
		return nil, errors.New("InvalidDeliveryPoint")
	}
	f.dblock.Lock()
	defer f.dblock.Unlock()

	psp, err := f.resolveProviderForSubscribe(service, deliveryPoint)
	if err != nil {
		return nil, err
	}

	if e := f.db.SetDeliveryPoint(deliveryPoint); e != nil {
		return nil, fmt.Errorf("failed to save new info for delivery point: %v", e)
	}
	if e := f.db.AddDeliveryPointToServiceSubscriber(service, subscriber, deliveryPoint.Name()); e != nil {
		return nil, fmt.Errorf("failed to add delivery point to subscriber: %v", e)
	}
	if e := f.db.SetPushServiceProviderOfServiceDeliveryPoint(service, deliveryPoint.Name(), psp.Name()); e != nil {
		return nil, fmt.Errorf("failed to set psp of delivery point: %v", e)
	}
	return psp, nil
}

func (f *pushDatabaseOpts) RemoveDeliveryPointFromService(service string,
	subscriber string,
	deliveryPoint *push.DeliveryPoint) error {
	if deliveryPoint.Name() == "" {
		return errors.New("InvalidDeliveryPoint")
	}
	f.dblock.Lock()
	defer f.dblock.Unlock()
	err := f.db.RemoveDeliveryPointFromServiceSubscriber(service, subscriber, deliveryPoint.Name())
	if err != nil {
		return fmt.Errorf("Failed to remove delivery point: %v", err)
	}
	err = f.db.RemovePushServiceProviderOfServiceDeliveryPoint(service, deliveryPoint.Name())
	if err != nil {
		return fmt.Errorf("Failed to remove psp info for delivery point: %v", err)
	}
	return nil
}

// orphanedDeliveryPoint is a name in a subscriber's set whose record has gone.
type orphanedDeliveryPoint struct {
	service string
	name    string
}

// GetPushServiceProviderDeliveryPointPairs fetches all of the delivery points of
// subscriber for a given service. If dpNames is not empty, the results are
// limited to that subset.
//
// A delivery point this cannot resolve to a provider is skipped and logged, not
// deleted. That distinction is the whole point: a read must not destroy data.
// The previous behaviour deleted the delivery point whenever its provider was
// missing, which turned /rmpsp into an unrecoverable operation -- every device
// in the service was silently unsubscribed by the next push, and re-adding the
// provider did not bring them back.
//
// The one case that is still cleaned up is a name in the subscriber's set whose
// delivery.point:<name> record is already gone. There is no data left to lose
// there, and leaving it means /subscriptions never stops reporting a device
// that does not exist.
func (f *pushDatabaseOpts) GetPushServiceProviderDeliveryPointPairs(service string,
	subscriber string, dpNamesRequested []string, logger log.Logger) ([]PushServiceProviderDeliveryPointPair, error) {
	logger = orDiscard(logger)
	pairs, orphans, err := f.collectDeliveryPointPairs(service, subscriber, dpNamesRequested, logger)
	if err != nil {
		return nil, err
	}

	// Deliberately outside the read lock. Cleaning up under RLock is what the
	// previous code did, and RLock admits concurrent readers, so two of them
	// finding the same orphan would both decrement its counter. Taking the
	// write lock afterwards costs an uncontended lock on a path that almost
	// never has orphans to clean.
	if len(orphans) > 0 {
		f.forgetOrphanedDeliveryPoints(subscriber, orphans, logger)
	}
	return pairs, nil
}

// collectDeliveryPointPairs does the read pass, returning the pairs it resolved
// and the delivery points whose records have vanished.
func (f *pushDatabaseOpts) collectDeliveryPointPairs(service string, subscriber string,
	dpNamesRequested []string, logger log.Logger) ([]PushServiceProviderDeliveryPointPair, []orphanedDeliveryPoint, error) {
	f.dblock.RLock()
	defer f.dblock.RUnlock()

	dpnames, err := f.db.GetDeliveryPointsNameByServiceSubscriber(service, subscriber)
	if err != nil {
		return nil, nil, fmt.Errorf("could not list delivery points for service %s, subscriber %s: %v", service, subscriber, err)
	}
	if dpnames == nil {
		return nil, nil, nil
	}
	pairs := make([]PushServiceProviderDeliveryPointPair, 0, len(dpnames))
	var orphans []orphanedDeliveryPoint
	// One provider lookup per service, not per delivery point.
	providerCache := make(map[string][]*push.PushServiceProvider, len(dpnames))

	dpNamesSubset := make(map[string]bool, len(dpNamesRequested))
	for _, name := range dpNamesRequested {
		dpNamesSubset[name] = true
	}

	for srv, dpList := range dpnames {
		for _, dpName := range dpList {
			if len(dpNamesSubset) != 0 && !dpNamesSubset[dpName] {
				// If we request a subset of delivery points, don't fetch or return data for the ones that weren't requested.
				continue
			}
			dp, e0 := f.db.GetDeliveryPoint(dpName)
			if e0 != nil {
				if isErrCausedByMissingKey(e0) {
					orphans = append(orphans, orphanedDeliveryPoint{service: srv, name: dpName})
					continue
				}
				return nil, nil, fmt.Errorf("failed to get delivery point info for %s: %v", dpName, e0)
			}
			if dp == nil {
				continue
			}

			psp, e1 := f.resolveProvider(srv, dp, providerCache, logger)
			if e1 != nil {
				return nil, nil, e1
			}
			if psp == nil {
				// Already logged by resolveProvider. The delivery point stays
				// where it is, so restoring the provider restores the device.
				continue
			}

			pairs = append(pairs, PushServiceProviderDeliveryPointPair{psp, dp})
		}
	}

	return pairs, orphans, nil
}

// serviceProviders lists a service's providers, caching within one read pass.
//
// The cache matters: without it this is a SMEMBERS plus a GET per provider for
// every single delivery point, and a subscriber with a dozen devices would pay
// for the same lookup a dozen times. A service's providers cannot change
// underneath a pass that holds the read lock.
func (f *pushDatabaseOpts) serviceProviders(service string,
	cache map[string][]*push.PushServiceProvider, logger log.Logger) ([]*push.PushServiceProvider, error) {
	// Normalised here as well as at the entry points, because this is reached
	// from the write path too, and that one has no logger of its own to pass
	// down. The one statement below that logs runs only when the database is
	// already inconsistent, so a nil logger here would be a panic waiting on a
	// rare branch -- the same shape of bug as the one the read path's teardown
	// had, which is reason enough to stop relying on callers getting it right.
	logger = orDiscard(logger)

	if providers, cached := cache[service]; cached {
		return providers, nil
	}

	names, err := f.db.GetPushServiceProvidersByService(service)
	if err != nil {
		return nil, fmt.Errorf("could not list push service providers for service %s: %v", service, err)
	}

	providers := make([]*push.PushServiceProvider, 0, len(names))
	for _, name := range names {
		psp, e := f.db.GetPushServiceProvider(name)
		if e != nil {
			if isErrCausedByMissingKey(e) {
				// A name in srv-2-psp with no record behind it. /rmpsp removes
				// both, so this means an interrupted write rather than normal
				// operation; skipping it is right, and the consistency check
				// reports it.
				logger.Infof("Service %q lists push service provider %q, which has no record; ignoring it", service, name)
				continue
			}
			return nil, fmt.Errorf("failed to get information about psp %s: %v", name, e)
		}
		if psp == nil {
			continue
		}
		providers = append(providers, psp)
	}

	cache[service] = providers
	return providers, nil
}

// resolveProvider finds the push service provider a delivery point sends through.
//
// The provider is *derived* from the service and the delivery point's push
// service type rather than read from srv.dp-2-psp. That index is not a source
// of truth: AddDeliveryPointToService computes exactly this answer at subscribe
// time and then stores it, so it is a cache of a pure function -- given the
// invariant that a service has at most one provider per push service type,
// which AddPushServiceProviderToService has enforced since PR #201.
//
// Deriving it is what decouples a device from its provider's credentials. The
// stored name embeds a hash of the provider's fixed data, so as long as it was
// authoritative, changing a credential meant every device pointed at a provider
// that no longer existed.
//
// The index is still consulted, but only to break a tie that should not exist:
// data written before PR #201 may have several providers of one type in a
// service, and srv-2-psp is an unordered set, so picking a match arbitrarily
// would be nondeterministic between reads.
//
// A nil provider with a nil error means "skip this delivery point": the reason
// has been logged, and it is not an error for the whole query. A real error
// means the database itself is unreachable, which the caller must not paper
// over by returning a short list of devices as though the rest had unsubscribed.
func (f *pushDatabaseOpts) resolveProvider(service string, dp *push.DeliveryPoint,
	cache map[string][]*push.PushServiceProvider, logger log.Logger) (*push.PushServiceProvider, error) {
	providers, err := f.serviceProviders(service, cache, logger)
	if err != nil {
		return nil, err
	}

	wanted := dp.PushServiceName()
	matches := candidateProviders(providers, wanted)

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		logger.Infof(
			"Delivery point %q of service %q is a %s subscription, but that service has no %s push service provider; "+
				"skipping it. Add one with /addpsp to make this device reachable again.",
			dp.Name(), service, wanted, wanted)
		return nil, nil
	}

	return f.disambiguateProvider(service, dp, matches, logger)
}

// candidateProviders returns the providers of one push service type.
func candidateProviders(providers []*push.PushServiceProvider, pushServiceType string) []*push.PushServiceProvider {
	matches := make([]*push.PushServiceProvider, 0, 1)
	for _, psp := range providers {
		if psp.PushServiceName() == pushServiceType {
			matches = append(matches, psp)
		}
	}
	return matches
}

// resolveProviderForSubscribe picks the provider a new subscription binds to.
//
// This has to agree with resolveProvider. Where it does not, a device is bound
// to one provider at subscribe time and served by another at push time, and the
// binding written here -- the very tie-breaker the read path falls back on --
// makes the disagreement permanent. Picking the first match out of srv-2-psp,
// which is an unordered set, is exactly the guess the read path was taught not
// to make.
//
// Where the read path skips a delivery point it cannot resolve, this refuses:
// /subscribe has a caller waiting for an answer, and a device accepted but
// unreachable is worse than one that was rejected with a reason.
func (f *pushDatabaseOpts) resolveProviderForSubscribe(service string,
	dp *push.DeliveryPoint) (*push.PushServiceProvider, error) {
	// A cache of one entry: this is a single lookup under the write lock, not a
	// pass over a subscriber's devices.
	//
	// Nothing to log to. AddDeliveryPointToService takes no logger, and the one
	// thing serviceProviders would report -- a provider name in srv-2-psp with
	// no record behind it -- costs nothing here: if the service has another
	// provider of the device's type the subscribe succeeds anyway, and if it
	// does not, the error returned below says so and /subscribe logs it.
	// /checkdb is what names the dangling provider.
	providers, err := f.serviceProviders(service, make(map[string][]*push.PushServiceProvider, 1), discardLogger)
	if err != nil {
		return nil, err
	}
	if len(providers) == 0 {
		// Capitalised, and staying that way: this is the error /subscribe has
		// always returned to clients for an unknown service. It moved here
		// unchanged from AddDeliveryPointToService, and rewording it would
		// break whatever is matching on it.
		return nil, fmt.Errorf("Cannot Find Service %s", service) //nolint:revive,staticcheck
	}

	matches := candidateProviders(providers, dp.PushServiceName())
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		// Preserved verbatim, like the message above: this is what /subscribe
		// has always answered when a service has no provider of the device's
		// type.
		return nil, fmt.Errorf("Cannot Find Push Service Provider with Type %s", dp.PushServiceName()) //nolint:revive,staticcheck
	}

	// Several providers of one type: data written before PR #201. An existing
	// binding is honoured so that re-subscribing a device already known to this
	// service cannot move it to a different provider.
	bound, err := f.db.GetPushServiceProviderNameByServiceDeliveryPoint(service, dp.Name())
	if err != nil && !isErrCausedByMissingKey(err) {
		return nil, fmt.Errorf("failed to get psp name for dp %s: %v", dp.Name(), err)
	}
	for _, psp := range matches {
		if psp.Name() == bound {
			return psp, nil
		}
	}

	return nil, fmt.Errorf(
		"service %s has %d push service providers of type %s, so there is no single provider to subscribe this "+
			"delivery point to. Run /checkdb, and remove all but one of them with /rmpsp",
		service, len(matches), dp.PushServiceName())
}

// disambiguateProvider picks between several providers of one push service type.
//
// Only reachable for a service that predates the one-provider-per-type rule.
// The stored binding is the tie-breaker because it records the choice made when
// the device subscribed, which is the closest thing to an intended answer.
func (f *pushDatabaseOpts) disambiguateProvider(service string, dp *push.DeliveryPoint,
	matches []*push.PushServiceProvider, logger log.Logger) (*push.PushServiceProvider, error) {
	pspname, err := f.db.GetPushServiceProviderNameByServiceDeliveryPoint(service, dp.Name())
	if err != nil && !isErrCausedByMissingKey(err) {
		return nil, fmt.Errorf("failed to get psp name for dp %s: %v", dp.Name(), err)
	}

	for _, psp := range matches {
		if psp.Name() == pspname {
			logger.Infof(
				"Service %q has %d push service providers of type %s; using the one delivery point %q was subscribed to (%s). "+
					"Run the consistency check: only one provider per type is supported.",
				service, len(matches), dp.PushServiceName(), dp.Name(), pspname)
			return psp, nil
		}
	}

	// Several candidates and nothing to choose between them. Guessing would
	// send to a different provider on different reads, which is worse than not
	// sending: a device would appear to work intermittently.
	logger.Infof(
		"Service %q has %d push service providers of type %s and delivery point %q is bound to none of them; "+
			"skipping it. Run the consistency check.",
		service, len(matches), dp.PushServiceName(), dp.Name())
	return nil, nil
}

// forgetOrphanedDeliveryPoints removes names whose delivery point record is gone.
func (f *pushDatabaseOpts) forgetOrphanedDeliveryPoints(subscriber string, orphans []orphanedDeliveryPoint, logger log.Logger) {
	f.dblock.Lock()
	defer f.dblock.Unlock()

	for _, orphan := range orphans {
		// Re-check under the write lock. Another goroutine may have cleaned this
		// up between the read pass and here, and the counter must not be
		// decremented twice for one delivery point.
		if _, err := f.db.GetDeliveryPoint(orphan.name); err == nil {
			continue
		} else if !isErrCausedByMissingKey(err) {
			continue
		}
		logger.Infof("Removing delivery point %q of service %q from subscriber %q: its record no longer exists",
			orphan.name, orphan.service, subscriber)
		f.db.RemoveMissingDeliveryPointFromServiceSubscriber(orphan.service, subscriber, orphan.name, logger)
		if err := f.db.RemovePushServiceProviderOfServiceDeliveryPoint(orphan.service, orphan.name); err != nil {
			logger.Errorf("Could not remove the provider binding for orphaned delivery point %q of service %q: %v",
				orphan.name, orphan.service, err)
		}
	}
}

// discardLogger swallows everything written to it.
//
// A logger built over a nil writer: log.NewLogger substitutes a writer that
// discards, so this needs no type of its own. Package-level because the read
// path allocating one per call would be silly.
var discardLogger = log.NewLogger(nil, "", log.LOGLEVEL_SILENT)

// orDiscard turns a nil logger into one that discards, at the boundary.
//
// Called once by each exported method that accepts a logger, so that nothing
// below has to think about it. The alternative -- a nil check at each call
// site -- was tried first and was worse than no defence at all: it covered
// this file only, while RemoveMissingDeliveryPointFromServiceSubscriber, which
// the same logger is handed to, logs unconditionally. So a nil logger got past
// the checks that looked like protection and panicked in the one place that
// reports a redis failure, which is to say on the error path, which is to say
// exactly where a panic is least welcome and least likely to be noticed first.
func orDiscard(logger log.Logger) log.Logger {
	if logger == nil {
		return discardLogger
	}
	return logger
}

func (f *pushDatabaseOpts) ModifyPushServiceProvider(psp *push.PushServiceProvider) error {
	if len(psp.Name()) == 0 {
		return nil
	}
	f.dblock.Lock()
	defer f.dblock.Unlock()
	return addErrorSource("ModifyPushServiceProvider", f.db.SetPushServiceProvider(psp))
}

func (f *pushDatabaseOpts) GetServiceNames() ([]string, error) {
	f.dblock.RLock()
	defer f.dblock.RUnlock()
	serviceNames, err := f.db.GetServiceNames()
	if err != nil {
		return nil, fmt.Errorf("GetServiceNames: %v", err)
	}
	return serviceNames, nil
}

func (f *pushDatabaseOpts) GetPushServiceProviderConfigs() ([]*push.PushServiceProvider, error) {
	serviceNames, err := f.GetServiceNames()
	if err != nil {
		return nil, err
	}
	f.dblock.RLock()
	defer f.dblock.RUnlock()
	var pspNames []string
	for _, serviceName := range serviceNames {
		pspsForService, err := f.db.GetPushServiceProvidersByService(serviceName)
		if err != nil {
			return nil, fmt.Errorf("GetPushServiceProvidersByService couldn't get psps for service %q: %v", serviceName, err)
		}
		pspNames = append(pspNames, pspsForService...)
	}
	psps, errs := f.db.GetPushServiceProviderConfigs(pspNames)
	if len(errs) > 0 {
		return nil, fmt.Errorf("GetServiceNames has invalid configs: %v", errs)
	}
	return psps, nil
}

func (f *pushDatabaseOpts) ModifyDeliveryPoint(dp *push.DeliveryPoint) error {
	if len(dp.Name()) == 0 {
		return nil
	}
	f.dblock.Lock()
	defer f.dblock.Unlock()
	return addErrorSource("ModifyDeliveryPoint", f.db.SetDeliveryPoint(dp))
}

func (f *pushDatabaseOpts) GetSubscriptions(services []string, user string, logger log.Logger) ([]map[string]string, error) {
	// Note: GetSubscriptions() reads only SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX+service and DELIVERY_POINT_PREFIX+dpName in the common case.
	// GetSubscriptions() does not read from the push service providers.

	// If a delivery point was unexpectedly missing,
	// then GetSubscriptions() would write to SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX+service and DELIVERY_POINT_COUNTER_PREFIX + dpName
	// (We don't get errors for "cleaning up count for delivery point" in the last day).
	// If this lock is disabled, the /subscriptions API and related APIs (e.g. /push) are much faster and no longer have a single bottleneck.

	// f.dblock.RLock()
	// defer f.dblock.RUnlock()
	// End note.
	subs, err := f.db.GetSubscriptions(services, user, orDiscard(logger))
	if err != nil {
		return nil, fmt.Errorf("GetSubscriptions: %v", err)
	}
	return subs, nil
}

func (f *pushDatabaseOpts) RebuildServiceSet() error {
	f.dblock.Lock()
	defer f.dblock.Unlock()
	return f.db.RebuildServiceSet()
}

func addErrorSource(fnName string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %v", fnName, err)
}
