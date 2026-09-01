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
	pspnames, err := f.db.GetPushServiceProvidersByService(service)
	if err != nil {
		return nil, fmt.Errorf("Cannot list services for %s: %v", service, err)
	}
	if pspnames == nil {
		return nil, fmt.Errorf("Cannot Find Service %s", service)
	}

	for _, pspname := range pspnames {
		psp, e := f.db.GetPushServiceProvider(pspname)
		if e != nil {
			return nil, fmt.Errorf("Failed to get information for psp %s: %v", pspname, e)
		}
		if psp == nil {
			continue
		}
		if psp.PushServiceName() == deliveryPoint.PushServiceName() {
			err = f.db.SetDeliveryPoint(deliveryPoint)
			if err != nil {
				return nil, fmt.Errorf("Failed to save new info for delivery point: %v", err)
			}
			err = f.db.AddDeliveryPointToServiceSubscriber(service, subscriber, deliveryPoint.Name())
			if err != nil {
				return nil, fmt.Errorf("Failed to add delivery point to subscriber: %v", err)
			}
			err = f.db.SetPushServiceProviderOfServiceDeliveryPoint(service, deliveryPoint.Name(), psp.Name())
			if err != nil {
				return nil, fmt.Errorf("Failed to set psp of delivery point: %v", err)
			}
			return psp, nil
		}
	}
	return nil, fmt.Errorf("Cannot Find Push Service Provider with Type %s", deliveryPoint.PushServiceName())
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

			psp, e1 := f.resolveProvider(srv, dpName, logger)
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

// resolveProvider finds the push service provider a delivery point sends through.
//
// A nil provider with a nil error means "skip this delivery point": the reason
// has been logged, and it is not an error for the whole query. A real error
// means the database itself is unreachable, which the caller must not paper
// over by returning a short list of devices as though the rest had unsubscribed.
func (f *pushDatabaseOpts) resolveProvider(service, dpName string, logger log.Logger) (*push.PushServiceProvider, error) {
	pspname, err := f.db.GetPushServiceProviderNameByServiceDeliveryPoint(service, dpName)
	if err != nil && !isErrCausedByMissingKey(err) {
		return nil, fmt.Errorf("failed to get psp name for dp %s: %v", dpName, err)
	}
	if len(pspname) == 0 {
		logger.Infof("Delivery point %q of service %q has no push service provider recorded; skipping it", dpName, service)
		return nil, nil
	}

	psp, err := f.db.GetPushServiceProvider(pspname)
	if err != nil {
		if isErrCausedByMissingKey(err) {
			logger.Infof(
				"Delivery point %q of service %q refers to push service provider %q, which no longer exists; "+
					"skipping it. Re-add that provider with /addpsp to make this device reachable again.",
				dpName, service, pspname)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get information about psp %s: %v", pspname, err)
	}
	if psp == nil {
		return nil, nil
	}
	return psp, nil
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
