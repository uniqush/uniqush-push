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
	. "github.com/uniqush/uniqush-push/push"
	"sync"
)

type PushServiceProviderDeliveryPointPair struct {
	PushServiceProvider *PushServiceProvider
	DeliveryPoint       *DeliveryPoint
}

// You may always want to use a front desk to get data from db
type PushDatabase interface {

	// The push service provider may by anonymous whose Name is empty string
	// For anonymous push service provider, it will be added to database
	// and its Name will be set
	RemovePushServiceProviderFromService(service string, push_service_provider *PushServiceProvider) error

	// The push service provider may by anonymous whose Name is empty string
	// For anonymous push service provider, it will be added to database
	// and its Name will be set
	AddPushServiceProviderToService(service string,
		push_service_provider *PushServiceProvider) error

	ModifyPushServiceProvider(psp *PushServiceProvider) error

	// The delivery point may be anonymous whose Name is empty string
	// For anonymous delivery point, it will be added to database and its Name will be set
	// Return value: selected push service provider, error
	AddDeliveryPointToService(service string,
		subscriber string,
		delivery_point *DeliveryPoint) (*PushServiceProvider, error)

	// The delivery point may be anonymous whose Name is empty string
	// For anonymous delivery point, it will be added to database and its Name will be set
	// Return value: selected push service provider, error
	RemoveDeliveryPointFromService(service string,
		subscriber string,
		delivery_point *DeliveryPoint) error

	ModifyDeliveryPoint(dp *DeliveryPoint) error

	GetPushServiceProviderDeliveryPointPairs(service string,
		subscriber string) ([]PushServiceProviderDeliveryPointPair, error)

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
		return nil, err
	}
	return f, nil
}

func (f *pushDatabaseOpts) FlushCache() error {
	f.dblock.Lock()
	defer f.dblock.Unlock()
	return f.db.FlushCache()
}

func (f *pushDatabaseOpts) RemovePushServiceProviderFromService(service string, push_service_provider *PushServiceProvider) error {
	name := push_service_provider.Name()
	if name == "" {
		return errors.New("InvalidPushServiceProvider")
	}
	db := f.db
	f.dblock.Lock()
	defer f.dblock.Unlock()
	err := db.RemovePushServiceProviderFromService(service, name)
	if err != nil {
		return err
	}
	err = db.RemovePushServiceProvider(name)
	if err != nil {
		return err
	}
	return nil
}

func (f *pushDatabaseOpts) AddPushServiceProviderToService(service string,
	push_service_provider *PushServiceProvider) error {
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
		return e
	}
	return f.db.AddPushServiceProviderToService(service, push_service_provider.Name())
}

func (f *pushDatabaseOpts) AddDeliveryPointToService(service string,
	subscriber string,
	delivery_point *DeliveryPoint) (*PushServiceProvider, error) {
	if delivery_point == nil {
		return nil, nil
	}
	f.dblock.Lock()
	defer f.dblock.Unlock()
	pspnames, err := f.db.GetPushServiceProvidersByService(service)
	if err != nil {
		return nil, err
	}
	if pspnames == nil {
		return nil, errors.New(fmt.Sprintf("Cannot Find Service %s", service))
	}
	if len(delivery_point.Name()) == 0 {
		return nil, errors.New("InvalidDeliveryPoint")
	}

	for _, pspname := range pspnames {
		psp, e := f.db.GetPushServiceProvider(pspname)
		if e != nil {
			return nil, e
		}
		if psp == nil {
			continue
		}
		if psp.PushServiceName() == delivery_point.PushServiceName() {
			err = f.db.SetDeliveryPoint(delivery_point)
			if err != nil {
				return nil, err
			}
			err = f.db.AddDeliveryPointToServiceSubscriber(service, subscriber, delivery_point.Name())
			if err != nil {
				return nil, err
			}
			err = f.db.SetPushServiceProviderOfServiceDeliveryPoint(service, delivery_point.Name(), psp.Name())
			if err != nil {
				return nil, err
			}
			return psp, nil
		}
	}
	return nil, errors.New(fmt.Sprintf("Cannot Find Push Service Provider with Type %s", delivery_point.PushServiceName()))
}

func (f *pushDatabaseOpts) RemoveDeliveryPointFromService(service string,
	subscriber string,
	delivery_point *DeliveryPoint) error {
	if delivery_point.Name() == "" {
		return errors.New("InvalidDeliveryPoint")
	}
	f.dblock.Lock()
	defer f.dblock.Unlock()
	err := f.db.RemoveDeliveryPointFromServiceSubscriber(service, subscriber, delivery_point.Name())
	if err != nil {
		return err
	}
	err = f.db.RemovePushServiceProviderOfServiceDeliveryPoint(service, delivery_point.Name())
	return err
}

func (f *pushDatabaseOpts) GetPushServiceProviderDeliveryPointPairs(service string,
	subscriber string) ([]PushServiceProviderDeliveryPointPair, error) {
	f.dblock.RLock()
	defer f.dblock.RUnlock()
	dpnames, err := f.db.GetDeliveryPointsNameByServiceSubscriber(service, subscriber)
	if err != nil {
		return nil, err
	}
	if dpnames == nil {
		return nil, nil
	}
	ret := make([]PushServiceProviderDeliveryPointPair, 0, len(dpnames))

	for srv, dpl := range dpnames {
		for _, d := range dpl {
			dp, e0 := f.db.GetDeliveryPoint(d)
			if e0 != nil {
				return nil, e0
			}
			if dp == nil {
				continue
			}

			pspname, e := f.db.GetPushServiceProviderNameByServiceDeliveryPoint(srv, d)
			if e != nil {
				return nil, e
			}

			if len(pspname) == 0 {
				continue
			}

			psp, e1 := f.db.GetPushServiceProvider(pspname)
			if e1 != nil {
				return nil, e1
			}
			if psp == nil {
				continue
			}

			ret = append(ret, PushServiceProviderDeliveryPointPair{psp, dp})
		}
	}

	return ret, nil
}

func (f *pushDatabaseOpts) ModifyPushServiceProvider(psp *PushServiceProvider) error {
	if len(psp.Name()) == 0 {
		return nil
	}
	f.dblock.Lock()
	defer f.dblock.Unlock()
	return f.db.SetPushServiceProvider(psp)
}

func (f *pushDatabaseOpts) ModifyDeliveryPoint(dp *DeliveryPoint) error {
	if len(dp.Name()) == 0 {
		return nil
	}
	f.dblock.Lock()
	defer f.dblock.Unlock()
	return f.db.SetDeliveryPoint(dp)
}
