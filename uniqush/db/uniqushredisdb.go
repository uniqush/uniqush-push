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
)

func NewUniqushRedisDB(c *DatabaseConfig) *UniqushRedisDB {
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
        ret := uniqush.NewAndroidDeliveryPoint(name, fields[0], fields[1])
        return ret
    }
    return nil
}

func deliveryPointToValue(dp *uniqush.DeliveryPoint) []byte {
    switch (dp.OSID()) {
    case uniqush.OSTYPE_ANDROID:
        str := fmt.Sprintf("%d.%s:%s", dp.OSID(), dp.GoogleAccount(), dp.RegistrationID())
        return []byte(str)
    }
    return nil
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

