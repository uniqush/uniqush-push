package db

import (
    "redis"
    "os"
    "fmt"
    "strconv"
    "strings"
    "uniqush"
)

type UniqushRedisDB struct {
    client *redis.Client
}

const (
    DELIVERY_POINT_PREFIX string = "delivery.point:"
    PUSH_SERVICE_PROVIDER_PREFIX string = "push.service.provider:"
    SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX string = "sudmap:"
    SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_RPVIDER_PREFIX string = "sdpspmap:"
    SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX string = "pspsmap:"
    DELIVERY_POINT_COUNTER_PREFIX string = "delivery.point.counter:"
)

func NewUniqushRedisDB(c *DatabaseConfig) *UniqushRedisDB {
    if c == nil {
        return nil
    }
    if strings.ToLower(c.Engine) != "redis" {
        return nil
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
    return ret
}

func keyValueToDeliveryPoint(name string, value []byte) *uniqush.DeliveryPoint {
    dp := new(uniqush.DeliveryPoint)
    return dp.Unmarshal(name, value)
    /*
    v := string(value)
    var substr string
    var ostype int
    fmt.Sscanf(v, "%d.%s", &ostype, &substr)
    switch (ostype) {
    case uniqush.OSTYPE_ANDROID:
        fields := strings.Split(substr, ":")
        if len(fields) < 2 {
            return nil
        }
        return uniqush.NewAndroidDeliveryPoint(name, fields[0], fields[1])
    }
    return nil
    */
}

func keyValueToPushServiceProvider(name string, value []byte) *uniqush.PushServiceProvider {
    psp := new(uniqush.PushServiceProvider)
    return psp.Unmarshal(name, value)
    /*
    v := string(value)
    var substr string
    var srvtype int
    fmt.Sscanf(v, "%d.%s", &srvtype, &substr)
    switch (srvtype) {
    case uniqush.SRVTYPE_C2DM:
        fields := strings.Split(substr, ":")
        if len(fields) < 2 {
            return nil
        }
        return uniqush.NewC2DMServiceProvider(name, fields[0], fields[1])
    }
    return nil
    */
}

func deliveryPointToValue(dp *uniqush.DeliveryPoint) []byte {
    return dp.Marshal()
    /*
    switch (dp.OSID()) {
    case uniqush.OSTYPE_ANDROID:
        str := fmt.Sprintf("%d.%s:%s", dp.OSID(), dp.GoogleAccount(), dp.RegistrationID())
        return []byte(str)
    }
    return nil
    */
}

func pushServiceProviderToValue(psp *uniqush.PushServiceProvider) []byte {
    /*
    switch (psp.ServiceID()) {
    case uniqush.SRVTYPE_C2DM:
        str := fmt.Sprintf("%d.%s:%s", psp.ServiceID(), psp.SenderID(), psp.AuthToken())
        return []byte(str)
    }
    return nil
    */
    return psp.Marshal()
}

func (r *UniqushRedisDB) GetDeliveryPoint(name string) (*uniqush.DeliveryPoint, os.Error) {
    b, err := r.client.Get(DELIVERY_POINT_PREFIX + name)
    if err != nil {
        return nil, err
    }
    if b == nil {
        return nil, nil
    }
    return keyValueToDeliveryPoint(name, b), nil
}

func (r *UniqushRedisDB) SetDeliveryPoint(dp *uniqush.DeliveryPoint) os.Error {
    err := r.client.Set(DELIVERY_POINT_PREFIX + dp.Name, deliveryPointToValue(dp))
    return err
}

func (r *UniqushRedisDB) GetPushServiceProvider(name string) (*uniqush.PushServiceProvider, os.Error) {
    b, err := r.client.Get(PUSH_SERVICE_PROVIDER_PREFIX + name)
    if err != nil {
        return nil, err
    }
    if b == nil {
        return nil, nil
    }
    return keyValueToPushServiceProvider(name, b), nil
}

func (r *UniqushRedisDB) SetPushServiceProvider(psp *uniqush.PushServiceProvider) os.Error {
    return r.client.Set(PUSH_SERVICE_PROVIDER_PREFIX + psp.Name, pushServiceProviderToValue(psp))
}

func (r *UniqushRedisDB) RemoveDeliveryPoint(dp *uniqush.DeliveryPoint) os.Error {
    _, err := r.client.Del(DELIVERY_POINT_PREFIX + dp.Name)
    return err
}

func (r *UniqushRedisDB) RemovePushServiceProvider(psp *uniqush.PushServiceProvider) os.Error {
    _, err := r.client.Del(PUSH_SERVICE_PROVIDER_PREFIX + psp.Name)
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
        _, e0 := r.client.Del(DELIVERY_POINT_PREFIX + dp)
        return e0
    }
    return e
}

func (r *UniqushRedisDB) SetPushServiceProviderOfServiceDeliveryPoint (srv, dp, psp string) os.Error {
    return r.client.Set(SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_RPVIDER_PREFIX + srv + ":" + dp, []byte(psp))
}

func (r *UniqushRedisDB) RemovePushServiceProviderOfServiceDeliveryPoint (srv, dp, psp string) os.Error {
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
