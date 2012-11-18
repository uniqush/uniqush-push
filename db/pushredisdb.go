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
	redis "github.com/monnand/goredis"
	. "github.com/uniqush/uniqush-push/push"
	"strconv"
	"strings"
)

type PushRedisDB struct {
	client *redis.Client
	psm    *PushServiceManager
}

const (
	DELIVERY_POINT_PREFIX                                 string = "delivery.point:"
	PUSH_SERVICE_PROVIDER_PREFIX                          string = "push.service.provider:"
	SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX          string = "srv.sub-2-dp:"
	SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_RPVIDER_PREFIX string = "srv.dp-2-psp:"
	SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX              string = "srv-2-psp:"
	DELIVERY_POINT_COUNTER_PREFIX                         string = "delivery.point.counter:"
)

func newPushRedisDB(c *DatabaseConfig) (*PushRedisDB, error) {
	if c == nil {
		return nil, errors.New("Invalid Database Config")
	}
	if strings.ToLower(c.Engine) != "redis" {
		return nil, errors.New("Unsupported Database Engine")
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
	var err error
	client.Db, err = strconv.Atoi(c.Name)
	if err != nil {
		client.Db = 0
	}

	ret := new(PushRedisDB)
	ret.client = &client
	ret.psm = c.PushServiceManager
	if ret.psm == nil {
		ret.psm = GetPushServiceManager()
	}
	return ret, nil
}

func (r *PushRedisDB) keyValueToDeliveryPoint(name string, value []byte) *DeliveryPoint {
	psm := r.psm
	dp, err := psm.BuildDeliveryPointFromBytes(value)
	if err != nil {
		return nil
	}
	return dp
}

func (r *PushRedisDB) keyValueToPushServiceProvider(name string, value []byte) *PushServiceProvider {
	psm := r.psm
	psp, err := psm.BuildPushServiceProviderFromBytes(value)
	if err != nil {
		return nil
	}
	return psp
}

func deliveryPointToValue(dp *DeliveryPoint) []byte {
	return dp.Marshal()
}

func pushServiceProviderToValue(psp *PushServiceProvider) []byte {
	return psp.Marshal()
}

func (r *PushRedisDB) GetDeliveryPoint(name string) (*DeliveryPoint, error) {
	b, err := r.client.Get(DELIVERY_POINT_PREFIX + name)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	return r.keyValueToDeliveryPoint(name, b), nil
}

func (r *PushRedisDB) SetDeliveryPoint(dp *DeliveryPoint) error {
	err := r.client.Set(DELIVERY_POINT_PREFIX+dp.Name(), deliveryPointToValue(dp))
	return err
}

func (r *PushRedisDB) GetPushServiceProvider(name string) (*PushServiceProvider, error) {
	b, err := r.client.Get(PUSH_SERVICE_PROVIDER_PREFIX + name)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	return r.keyValueToPushServiceProvider(name, b), nil
}

func (r *PushRedisDB) SetPushServiceProvider(psp *PushServiceProvider) error {
	return r.client.Set(PUSH_SERVICE_PROVIDER_PREFIX+psp.Name(), pushServiceProviderToValue(psp))
}

func (r *PushRedisDB) RemoveDeliveryPoint(dp string) error {
	_, err := r.client.Del(DELIVERY_POINT_PREFIX + dp)
	return err
}

func (r *PushRedisDB) RemovePushServiceProvider(psp string) error {
	_, err := r.client.Del(PUSH_SERVICE_PROVIDER_PREFIX + psp)
	return err
}

func (r *PushRedisDB) GetDeliveryPointsNameByServiceSubscriber(srv, usr string) (map[string][]string, error) {
	keys := make([]string, 1)
	if !strings.Contains(usr, "*") && !strings.Contains(srv, "*") {
		keys[0] = SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX + srv + ":" + usr
	} else {
		var err error
		keys, err = r.client.Keys(SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX + srv + ":" + usr)
		if err != nil {
			return nil, err
		}
	}

	ret := make(map[string][]string, len(keys))
	for _, k := range keys {
		m, err := r.client.Smembers(k)
		if err != nil {
			return nil, err
		}
		if m == nil {
			continue
		}
		elem := strings.Split(k, ":")
		s := elem[1]
		if l, ok := ret[s]; !ok || l == nil {
			ret[s] = make([]string, 0, len(keys))
		}
		for _, bm := range m {
			dpl := ret[s]
			dpl = append(dpl, string(bm))
			ret[s] = dpl
		}
	}
	return ret, nil
}

func (r *PushRedisDB) GetPushServiceProviderNameByServiceDeliveryPoint(srv, dp string) (string, error) {
	b, err := r.client.Get(SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_RPVIDER_PREFIX + srv + ":" + dp)
	if err != nil {
		return "", err
	}
	if b == nil {
		return "", nil
	}
	return string(b), nil
}

func (r *PushRedisDB) AddDeliveryPointToServiceSubscriber(srv, sub, dp string) error {
	i, err := r.client.Sadd(SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX+srv+":"+sub, []byte(dp))
	if err != nil {
		return err
	}
	if i == false {
		return nil
	}
	_, err = r.client.Incr(DELIVERY_POINT_COUNTER_PREFIX + dp)
	if err != nil {
		return err
	}
	return nil
}

func (r *PushRedisDB) RemoveDeliveryPointFromServiceSubscriber(srv, sub, dp string) error {
	j, err := r.client.Srem(SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX+srv+":"+sub, []byte(dp))
	if err != nil {
		return err
	}
	if j == false {
		return nil
	}
	i, e := r.client.Decr(DELIVERY_POINT_COUNTER_PREFIX + dp)
	if e != nil {
		return e
	}
	if i <= 0 {
		_, e0 := r.client.Del(DELIVERY_POINT_COUNTER_PREFIX + dp)
		if e0 != nil {
			return e0
		}
		_, e1 := r.client.Del(DELIVERY_POINT_PREFIX + dp)
		return e1
	}
	return e
}

func (r *PushRedisDB) SetPushServiceProviderOfServiceDeliveryPoint(srv, dp, psp string) error {
	return r.client.Set(SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_RPVIDER_PREFIX+srv+":"+dp, []byte(psp))
}

func (r *PushRedisDB) RemovePushServiceProviderOfServiceDeliveryPoint(srv, dp string) error {
	_, err := r.client.Del(SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_RPVIDER_PREFIX + srv + ":" + dp)
	return err
}

func (r *PushRedisDB) GetPushServiceProvidersByService(srv string) ([]string, error) {
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

func (r *PushRedisDB) RemovePushServiceProviderFromService(srv, psp string) error {
	_, err := r.client.Srem(SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX+srv, []byte(psp))
	return err
}

func (r *PushRedisDB) AddPushServiceProviderToService(srv, psp string) error {
	_, err := r.client.Sadd(SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX+srv, []byte(psp))
	return err
}

func (r *PushRedisDB) FlushCache() error {
	return r.client.Save()
}
