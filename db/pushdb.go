/*
 * Copyright 2011 Nan Deng
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
 *
 */

package db

import (
	"errors"
	"fmt"
	"sync"

	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/push"
)

type PushServiceProviderDeliveryPointPair struct {
	PushServiceProvider *push.PushServiceProvider
	DeliveryPoint       *push.DeliveryPoint
}

// You may always want to use a front desk to get data from db
type PushDatabase interface {

	// The push service provider may by anonymous whose Name is empty string
	// For anonymous push service provider, it will be added to database
	// and its Name will be set
	RemovePushServiceProviderFromService(service string, push_service_provider *push.PushServiceProvider) error

	// The push service provider may by anonymous whose Name is empty string
	// For anonymous push service provider, it will be added to database
	// and its Name will be set
	AddPushServiceProviderToService(service string,
		push_service_provider *push.PushServiceProvider) error

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

	GetPushServiceProviderDeliveryPointPairs(service string,
		subscriber string) ([]PushServiceProviderDeliveryPointPair, error)

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

func NewPushDatabaseWithoutCache(conf *DatabaseConfig) (PushDatabase, error) {
	var err error
	f := new(pushDatabaseOpts)
	f.db, err = newPushRedisDB(conf)
	if f.db == nil || err != nil {
		return nil, fmt.Errorf("Failed to create database: %v", err)
	}
	return f, nil
}

func (f *pushDatabaseOpts) FlushCache() error {
	f.dblock.Lock()
	defer f.dblock.Unlock()
	return f.db.FlushCache()
}

func (f *pushDatabaseOpts) RemovePushServiceProviderFromService(service string, push_service_provider *push.PushServiceProvider) error {
	name := push_service_provider.Name()
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

func (f *pushDatabaseOpts) AddPushServiceProviderToService(service string,
	push_service_provider *push.PushServiceProvider) error {
	if push_service_provider == nil {
		return nil
	}
	name := push_service_provider.Name()
	if len(name) == 0 {
		return errors.New("InvalidPushServiceProvider")
	}
	f.dblock.Lock()
	defer f.dblock.Unlock()
	e := f.db.SetPushServiceProvider(push_service_provider)
	if e != nil {
		return fmt.Errorf("Error associating psp with name: %v", e)
	}
	return f.db.AddPushServiceProviderToService(service, push_service_provider.Name())
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
		return nil, errors.New(fmt.Sprintf("Cannot Find Service %s", service))
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
	return nil, errors.New(fmt.Sprintf("Cannot Find Push Service Provider with Type %s", delivery_point.PushServiceName()))
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

func (f *pushDatabaseOpts) GetPushServiceProviderDeliveryPointPairs(service string,
	subscriber string) ([]PushServiceProviderDeliveryPointPair, error) {
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

	for srv, dpList := range dpnames {
		for _, dpName := range dpList {
			dp, e0 := f.db.GetDeliveryPoint(dpName)
			if e0 != nil {
				return nil, fmt.Errorf("Failed to get delivery point info for %s: %v", dpName, e0)
			}
			if dp == nil {
				continue
			}

			pspname, e := f.db.GetPushServiceProviderNameByServiceDeliveryPoint(srv, dpName)
			if e != nil {
				return nil, fmt.Errorf("Failed to get psp name for dp %s: %v", dpName, e)
			}

			if len(pspname) == 0 {
				continue
			}

			psp, e1 := f.db.GetPushServiceProvider(pspname)
			if e1 != nil {
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
	psps, err := f.db.GetServiceNames()
	if err != nil {
		return nil, fmt.Errorf("GetServiceNames: %v", err)
	}
	return psps, nil
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
	f.dblock.RLock()
	defer f.dblock.RUnlock()
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
