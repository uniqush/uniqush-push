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

package db

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/push"
)

const (
	DELIVERY_POINT_ID = "delivery_point_id" // temporary variable in Subscription responses. This is the internal identifier for a delivery point(subscription).
)

type PushServiceProviderDeliveryPointPair struct {
	PushServiceProvider *push.PushServiceProvider
	DeliveryPoint       *push.DeliveryPoint
}

// isErrCausedByMissingKey checks if an error is caused by a missing redis key. It uses string comparisons because err's type may be erased, and doesn't exist to begin with.
func isErrCausedByMissingKey(err error) bool {
	// TODO - fix this check.
	// This would be a redis.redisError with Err = "redis: nil", and could be detected in pushredisdb.go
	// return strings.Contains(err.Error(), "Redis Error: Key does not exist")
	return strings.Contains(err.Error(), "redis: nil") // redisv3 check.
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
		delivery_point *push.DeliveryPoint) (*push.PushServiceProvider, error)

	// The delivery point may be anonymous whose Name is empty string
	// For anonymous delivery point, it will be added to database and its Name will be set
	// Return value: selected push service provider, error
	RemoveDeliveryPointFromService(service string,
		subscriber string,
		delivery_point *push.DeliveryPoint) error

	ModifyDeliveryPoint(dp *push.DeliveryPoint) error

	GetPushServiceProviderDeliveryPointPairs(service string, subscriber string, dpNamesRequested []string) ([]PushServiceProviderDeliveryPointPair, error)

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
					"A different PSP for service %s already exists with different fixed data as push service type %s (It has a separate subscriber list). Please double check the list of current PSPs with the /psps API. Note that this error could be worked around by removing the old PSP, but that would delete subscriptions.",
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
	delivery_point *push.DeliveryPoint) (*push.PushServiceProvider, error) {
	if delivery_point == nil {
		return nil, nil
	}
	if len(delivery_point.Name()) == 0 {
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
		if psp.PushServiceName() == delivery_point.PushServiceName() {
			err = f.db.SetDeliveryPoint(delivery_point)
			if err != nil {
				return nil, fmt.Errorf("Failed to save new info for delivery point: %v", err)
			}
			err = f.db.AddDeliveryPointToServiceSubscriber(service, subscriber, delivery_point.Name())
			if err != nil {
				return nil, fmt.Errorf("Failed to add delivery point to subscriber: %v", err)
			}
			err = f.db.SetPushServiceProviderOfServiceDeliveryPoint(service, delivery_point.Name(), psp.Name())
			if err != nil {
				return nil, fmt.Errorf("Failed to set psp of delivery point: %v", err)
			}
			return psp, nil
		}
	}
	return nil, fmt.Errorf("Cannot Find Push Service Provider with Type %s", delivery_point.PushServiceName())
}

func (f *pushDatabaseOpts) RemoveDeliveryPointFromService(service string,
	subscriber string,
	delivery_point *push.DeliveryPoint) error {
	if delivery_point.Name() == "" {
		return errors.New("InvalidDeliveryPoint")
	}
	f.dblock.Lock()
	defer f.dblock.Unlock()
	err := f.db.RemoveDeliveryPointFromServiceSubscriber(service, subscriber, delivery_point.Name())
	if err != nil {
		return fmt.Errorf("Failed to remove delivery point: %v", err)
	}
	err = f.db.RemovePushServiceProviderOfServiceDeliveryPoint(service, delivery_point.Name())
	if err != nil {
		return fmt.Errorf("Failed to remove psp info for delivery point: %v", err)
	}
	return nil
}

// Fetch all of the delivery points of subscriber for a given service. If dpNames is not empty, limit the results to fetch to that subset.
func (f *pushDatabaseOpts) GetPushServiceProviderDeliveryPointPairs(service string,
	subscriber string, dpNamesRequested []string) ([]PushServiceProviderDeliveryPointPair, error) {
	f.dblock.RLock()
	defer f.dblock.RUnlock()
	dpnames, err := f.db.GetDeliveryPointsNameByServiceSubscriber(service, subscriber)
	if err != nil {
		return nil, fmt.Errorf("Could not list delivery points for service %s, subscriber %s: %v", service, subscriber, err)
	}
	if dpnames == nil {
		return nil, nil
	}
	ret := make([]PushServiceProviderDeliveryPointPair, 0, len(dpnames))

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
					f.db.RemoveDeliveryPoint(dpName)
					continue
				}
				return nil, fmt.Errorf("Failed to get delivery point info for %s: %v", dpName, e0)
			}
			if dp == nil {
				continue
			}

			pspname, e := f.db.GetPushServiceProviderNameByServiceDeliveryPoint(srv, dpName)
			if e != nil {
				if isErrCausedByMissingKey(e) {
					f.db.RemoveDeliveryPoint(dpName)
					continue
				}
				return nil, fmt.Errorf("Failed to get psp name for dp %s: %v", dpName, e)
			}

			if len(pspname) == 0 {
				continue
			}

			psp, e1 := f.db.GetPushServiceProvider(pspname)
			if e1 != nil {
				// If the error was caused because the PSP for the dpName no longer exists, then ignore and remove that delivery point.
				if isErrCausedByMissingKey(e1) {
					e2 := f.db.RemoveDeliveryPoint(dpName)
					e3 := f.db.RemovePushServiceProviderOfServiceDeliveryPoint(srv, dpName)
					if e2 != nil {
						return nil, fmt.Errorf("Failed to remove dp %s with invalid psp %s: %v", dpName, pspname, e2)
					}
					if e3 != nil {
						return nil, fmt.Errorf("Failed to remove pspname %s for dp %s (PSP no longer exists): %v", pspname, dpName, e3)
					}
					continue
				}
				return nil, fmt.Errorf("Failed to get information about psp %s: %v", pspname, e1)
			}
			if psp == nil {
				continue
			}

			ret = append(ret, PushServiceProviderDeliveryPointPair{psp, dp})
		}
	}

	return ret, nil
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
	subs, err := f.db.GetSubscriptions(services, user, logger)
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
