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

package uniqush

import (
    "os"
    "crypto/sha1"
    "fmt"
)

type PushServiceProviderDeliveryPointPair struct {
    PushServiceProvider *PushServiceProvider
    DeliveryPoint *DeliveryPoint
}

// You may always want to use a front desk to get data from db
type DatabaseFrontDeskIf interface {

    // The push service provider may by anonymous whose Name is empty string
    // For anonymous push service provider, it will be added to database
    // and its Name will be set
    RemovePushServiceProviderFromService(service string, push_service_provider *PushServiceProvider) os.Error

    // The push service provider may by anonymous whose Name is empty string
    // For anonymous push service provider, it will be added to database
    // and its Name will be set
    AddPushServiceProviderToService (service string,
                                     push_service_provider *PushServiceProvider) os.Error

    ModifyPushServiceProvider (psp *PushServiceProvider) os.Error

    // The delivery point may be anonymous whose Name is empty string
    // For anonymous delivery point, it will be added to database and its Name will be set
    // Return value: selected push service provider, error
    AddDeliveryPointToService (service string,
                            subscriber string,
                            delivery_point *DeliveryPoint,
                            prefered_service int) (*PushServiceProvider, os.Error)

    // The delivery point may be anonymous whose Name is empty string
    // For anonymous delivery point, it will be added to database and its Name will be set
    // Return value: selected push service provider, error
    RemoveDeliveryPointFromService (service string,
                                    subscriber string,
                                    delivery_point *DeliveryPoint) os.Error

    ModifyDeliveryPoint(dp *DeliveryPoint) os.Error

    GetPushServiceProviderDeliveryPointPairs (service string,
                                              subscriber string)([]PushServiceProviderDeliveryPointPair, os.Error)

    FlushCache() os.Error
}

func genDeliveryPointName(sub string, dp *DeliveryPoint) {
    hash := sha1.New()
    key := "delivery.point:" + sub + ":" + dp.UniqStr()
    hash.Write([]byte(key))
    dp.Name = fmt.Sprintf("%x", hash.Sum())
}

func genPushServiceProviderName(srv string, psp *PushServiceProvider) {
    hash := sha1.New()
    key := "push.service.provider:" + srv + ":" + psp.UniqStr()
    hash.Write([]byte(key))
    psp.Name = fmt.Sprintf("%x", hash.Sum())
}

type DatabaseFrontDesk struct {
    db UniqushDatabase
}

func NewDatabaseFrontDesk(conf *DatabaseConfig) (DatabaseFrontDeskIf, os.Error) {
    var err os.Error
    f := new(DatabaseFrontDesk)
    udb, err := NewUniqushRedisDB(conf)
    if udb == nil || err != nil{
        return nil, err
    }
    f.db = NewCachedUniqushDatabase(udb, udb, conf)
    if f.db == nil {
        return nil, os.NewError("Cannot create cached database")
    }
    return f, nil
}

func NewDatabaseFrontDeskWithoutCache(conf *DatabaseConfig) (DatabaseFrontDeskIf, os.Error){
    var err os.Error
    f := new(DatabaseFrontDesk)
    f.db, err = NewUniqushRedisDB(conf)
    if f.db == nil || err != nil{
        return nil, err
    }
    return f, nil
}

func (f *DatabaseFrontDesk)FlushCache() os.Error {
    return f.db.FlushCache()
}

func (f *DatabaseFrontDesk)RemovePushServiceProviderFromService (service string, push_service_provider *PushServiceProvider) os.Error {
    if len(push_service_provider.Name) == 0 {
        genPushServiceProviderName(service, push_service_provider)
    }
    name := push_service_provider.Name
    db := f.db
    return db.RemovePushServiceProviderFromService(service, name)
}


func (f *DatabaseFrontDesk) AddPushServiceProviderToService (service string,
                                     push_service_provider *PushServiceProvider) os.Error {
    if push_service_provider == nil {
        return nil
    }
    if len(push_service_provider.Name) == 0 {
        genPushServiceProviderName(service, push_service_provider)
        e := f.db.SetPushServiceProvider(push_service_provider)
        if e != nil {
            return e
        }
    }
    return f.db.AddPushServiceProviderToService(service, push_service_provider.Name)
}

func (f *DatabaseFrontDesk) AddDeliveryPointToService (service string,
                                                       subscriber string,
                                                       delivery_point *DeliveryPoint,
                                                       prefered_service int) (*PushServiceProvider, os.Error) {
    if delivery_point == nil {
        return nil, nil
    }
    pspnames, err := f.db.GetPushServiceProvidersByService(service)
    if err != nil {
        return nil, err
    }
    if pspnames == nil {
        return nil, nil
    }
    var first_fit *PushServiceProvider
    var found *PushServiceProvider

    if len(delivery_point.Name) == 0 {
        genDeliveryPointName(subscriber, delivery_point)
        err = f.db.SetDeliveryPoint(delivery_point)
        if err != nil {
            return nil, err
        }
    }

    for _, pspname := range pspnames {
        psp, e := f.db.GetPushServiceProvider(pspname)
        if e != nil {
            return nil, e
        }
        if psp == nil {
            continue
        }
        if first_fit == nil && psp.IsCompatible(&delivery_point.OSType) {
            if prefered_service < 0 {
                found = psp
                break
            }
            first_fit = psp
        }
        if prefered_service > 0 {
            if psp.ServiceID() == prefered_service {
                found = psp
                break
            }
        }
    }

    if found == nil {
        found = first_fit
    }

    if found == nil {
        return nil, nil
    }

    err = f.db.AddDeliveryPointToServiceSubscriber(service, subscriber, delivery_point.Name)
    if err != nil {
        return nil, err
    }

    err = f.db.SetPushServiceProviderOfServiceDeliveryPoint(service, delivery_point.Name, found.Name)
    if err != nil {
        return nil, err
    }
    return found, nil
}

func (f *DatabaseFrontDesk) RemoveDeliveryPointFromService (service string,
                                                            subscriber string,
                                                            delivery_point *DeliveryPoint) os.Error {
    if delivery_point.Name == "" {
        genDeliveryPointName(subscriber, delivery_point)
    }
    err := f.db.RemoveDeliveryPointFromServiceSubscriber(service, subscriber, delivery_point.Name)
    if err != nil {
        return err
    }
    err = f.db.RemovePushServiceProviderOfServiceDeliveryPoint(service, delivery_point.Name)
    return err
}

func (f *DatabaseFrontDesk) GetPushServiceProviderDeliveryPointPairs (service string,
                                              subscriber string) ([]PushServiceProviderDeliveryPointPair, os.Error) {
    dpnames, err := f.db.GetDeliveryPointsNameByServiceSubscriber(service, subscriber)
    if err != nil {
        return nil, err
    }
    if dpnames == nil {
        return nil, nil
    }
    ret := make([]PushServiceProviderDeliveryPointPair, 0, len(dpnames))

    for _, d := range dpnames {
        pspname , e := f.db.GetPushServiceProviderNameByServiceDeliveryPoint(service, d)
        if e != nil {
            return nil, e
        }

        if len(pspname) == 0 {
            continue
        }

        dp, e0 := f.db.GetDeliveryPoint(d)
        if e0 != nil {
            return nil, e0
        }
        if dp == nil {
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

    return ret, nil
}

func (f *DatabaseFrontDesk) ModifyPushServiceProvider(psp *PushServiceProvider) os.Error {
    if len(psp.Name) == 0 {
        return nil
    }
    return f.db.SetPushServiceProvider(psp)
}

func (f *DatabaseFrontDesk) ModifyDeliveryPoint(dp *DeliveryPoint) os.Error {
    if len(dp.Name) == 0 {
        return nil
    }
    return f.db.SetDeliveryPoint(dp)
}
