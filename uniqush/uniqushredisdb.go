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
    redis "github.com/monnand/redis.go"
    "os"
    "fmt"
    "strconv"
    "strings"
)

type UniqushRedisDB struct {
    client *redis.Client
    psm *PushServiceManager
}

const (
    DELIVERY_POINT_PREFIX string = "delivery.point:"
    PUSH_SERVICE_PROVIDER_PREFIX string = "push.service.provider:"
    SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX string = "srv.sub-2-dp:"
    SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_RPVIDER_PREFIX string = "srv.dp-2-psp:"
    SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX string = "srv-2-psp:"
    DELIVERY_POINT_COUNTER_PREFIX string = "delivery.point.counter:"
)

func NewUniqushRedisDB(c *DatabaseConfig) (*UniqushRedisDB, os.Error) {
    if c == nil {
        return nil, os.NewError("Invalid Database Config")
    }
    if strings.ToLower(c.Engine) != "redis" {
        return nil, os.NewError("Unsupported Database Engine")
    }
    var client redis.Client
    if c.Host == "" {
        c.Host = "localhost"
    }
    if c.Port <= 0 {
        c.Port = 6379
    }
    if c.Name == "" {
        c.Name = "0"
    }

    client.Addr = fmt.Sprintf("%s:%d", c.Host, c.Port)
    client.Password = c.Password
    var err os.Error
    client.Db, err = strconv.Atoi(c.Name)
    if err != nil {
        client.Db = 0
    }

    ret := new(UniqushRedisDB)
    ret.client = &client
    ret.psm = c.psm
    if ret.psm == nil {
        ret.psm = GetPushServiceManager()
    }
    return ret, nil
}

func (r *UniqushRedisDB) keyValueToDeliveryPoint(name string, value []byte) *DeliveryPoint {
    psm := r.psm
    dp, err := psm.BuildDeliveryPointFromBytes(value)
    if err != nil {
        return nil
    }
    return dp
    /*
    dp := new(DeliveryPoint)
    return dp.Unmarshal(name, value)
    v := string(value)
    var substr string
    var ostype int
    fmt.Sscanf(v, "%d.%s", &ostype, &substr)
    switch (ostype) {
    case OSTYPE_ANDROID:
        fields := strings.Split(substr, ":")
        if len(fields) < 2 {
            return nil
        }
        return NewAndroidDeliveryPoint(name, fields[0], fields[1])
    }
    return nil
    */
}

func (r *UniqushRedisDB) keyValueToPushServiceProvider(name string, value []byte) *PushServiceProvider {
    //psp := new(PushServiceProvider)
    //return psp.Unmarshal(name, value)
    /* TODO use push service manager to gen */
    psm := r.psm
    psp, err := psm.BuildPushServiceProviderFromBytes(value)
    if err != nil {
        return nil
    }
    return psp
    /*
    v := string(value)
    var substr string
    var srvtype int
    fmt.Sscanf(v, "%d.%s", &srvtype, &substr)
    switch (srvtype) {
    case SRVTYPE_C2DM:
        fields := strings.Split(substr, ":")
        if len(fields) < 2 {
            return nil
        }
        return NewC2DMServiceProvider(name, fields[0], fields[1])
    }
    return nil
    */
}

func deliveryPointToValue(dp *DeliveryPoint) []byte {
    return dp.Marshal()
}

func pushServiceProviderToValue(psp *PushServiceProvider) []byte {
    return psp.Marshal()
}

func (r *UniqushRedisDB) GetDeliveryPoint(name string) (*DeliveryPoint, os.Error) {
    b, err := r.client.Get(DELIVERY_POINT_PREFIX + name)
    if err != nil {
        return nil, err
    }
    if b == nil {
        return nil, nil
    }
    return r.keyValueToDeliveryPoint(name, b), nil
}

func (r *UniqushRedisDB) SetDeliveryPoint(dp *DeliveryPoint) os.Error {
    err := r.client.Set(DELIVERY_POINT_PREFIX + dp.Name(), deliveryPointToValue(dp))
    return err
}

func (r *UniqushRedisDB) GetPushServiceProvider(name string) (*PushServiceProvider, os.Error) {
    b, err := r.client.Get(PUSH_SERVICE_PROVIDER_PREFIX + name)
    if err != nil {
        return nil, err
    }
    if b == nil {
        return nil, nil
    }
    return r.keyValueToPushServiceProvider(name, b), nil
}

func (r *UniqushRedisDB) SetPushServiceProvider(psp *PushServiceProvider) os.Error {
    return r.client.Set(PUSH_SERVICE_PROVIDER_PREFIX + psp.Name(), pushServiceProviderToValue(psp))
}

func (r *UniqushRedisDB) RemoveDeliveryPoint(dp *DeliveryPoint) os.Error {
    _, err := r.client.Del(DELIVERY_POINT_PREFIX + dp.Name())
    return err
}

func (r *UniqushRedisDB) RemovePushServiceProvider(psp *PushServiceProvider) os.Error {
    _, err := r.client.Del(PUSH_SERVICE_PROVIDER_PREFIX + psp.Name())
    return err
}

func (r *UniqushRedisDB) GetDeliveryPointsNameByServiceSubscriber (srv, usr string) ([]string, os.Error) {
    m, err := r.client.Smembers(SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX + srv + ":" + usr)
    if err != nil {
        return nil, err
    }
    if m == nil {
        return nil, nil
    }
    ret := make([]string, len(m))
    for i, bm := range m {
        ret[i] = string(bm)
    }

    return ret, nil
}

func (r *UniqushRedisDB) GetPushServiceProviderNameByServiceDeliveryPoint (srv, dp string) (string, os.Error) {
    b, err := r.client.Get(SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_RPVIDER_PREFIX + srv + ":"  + dp)
    if err != nil {
        return "", err
    }
    if b == nil {
        return "", nil
    }
    return string(b), nil
}

func (r *UniqushRedisDB) AddDeliveryPointToServiceSubscriber (srv, sub, dp string) os.Error {
    _, err := r.client.Sadd(SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX + srv + ":" + sub, []byte(dp))
    if err != nil {
        return err
    }
    _, err = r.client.Incr(DELIVERY_POINT_COUNTER_PREFIX + dp)
    return err
}

func (r *UniqushRedisDB) RemoveDeliveryPointFromServiceSubscriber (srv, sub, dp string) os.Error {
    _, err := r.client.Srem(SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX + srv + ":" + sub, []byte(dp))
    if err != nil {
        return err
    }
    i, e := r.client.Decr(DELIVERY_POINT_COUNTER_PREFIX + dp)
    if e != nil {
        return e
    }
    if i <= 0 {
        _, e0 := r.client.Decr(DELIVERY_POINT_COUNTER_PREFIX + dp)
        if e0 != nil {
            return e0
        }
        _, e1 := r.client.Del(DELIVERY_POINT_PREFIX + dp)
        return e1
    }
    return e
}

func (r *UniqushRedisDB) SetPushServiceProviderOfServiceDeliveryPoint (srv, dp, psp string) os.Error {
    return r.client.Set(SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_RPVIDER_PREFIX + srv + ":" + dp, []byte(psp))
}

func (r *UniqushRedisDB) RemovePushServiceProviderOfServiceDeliveryPoint (srv, dp string) os.Error {
    _, err := r.client.Del(SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_RPVIDER_PREFIX + srv + ":" + dp)
    return err
}

func (r *UniqushRedisDB) GetPushServiceProvidersByService (srv string) ([]string, os.Error) {
    m, err := r.client.Smembers(SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX + srv)
    if err != nil {
        return nil, err
    }
    if m == nil {
        return nil, nil
    }
    ret := make([]string, len(m))
    for i, bm := range m {
        ret[i] = string(bm)
    }

    return ret, nil
}

func (r *UniqushRedisDB) RemovePushServiceProviderFromService (srv, psp string) os.Error {
    _, err := r.client.Srem(SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX + srv, []byte(psp))
    return err
}

func (r *UniqushRedisDB) AddPushServiceProviderToService (srv, psp string) os.Error {
    _, err := r.client.Sadd(SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX + srv, []byte(psp))
    return err
}

func (r *UniqushRedisDB) FlushCache() os.Error {
    return r.client.Save()
}
