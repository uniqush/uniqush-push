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
	"strconv"
	"strings"

	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/push"
	redis5 "gopkg.in/redis.v5"
)

type PushRedisDB struct {
	client *redis5.Client
	psm    *push.PushServiceManager
}

var _ pushRawDatabase = &PushRedisDB{}

const (
	DELIVERY_POINT_PREFIX                                  string = "delivery.point:"         // STRING (prefix of)- Maps the delivery point name to a json blob of information about a delivery point.
	PUSH_SERVICE_PROVIDER_PREFIX                           string = "push.service.provider:"  // STRING (prefix of) - Maps a push service provider name to a json blob of information about it.
	SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX           string = "srv.sub-2-dp:"           // SET (prefix of) - Maps a service name + subscriber to a set of delivery point names
	SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_PROVIDER_PREFIX string = "srv.dp-2-psp:"           // STRING (prefix of) - Maps a service name + delivery point name to the push service provider
	SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX               string = "srv-2-psp:"              // SET (prefix of) - Maps a service name to a set of PSP names
	DELIVERY_POINT_COUNTER_PREFIX                          string = "delivery.point.counter:" // STRING (prefix of) - Maps a delivery point name to the number of subcribers(summed across each service).
	SERVICES_SET                                           string = "services{0}"             // SET - This is a set of service names.
)

func newPushRedisDB(c *DatabaseConfig) (*PushRedisDB, error) {
	if c == nil {
		return nil, errors.New("Invalid Database Config")
	}
	if strings.ToLower(c.Engine) != "redis" {
		return nil, errors.New("Unsupported Database Engine")
	}

	if c.Host == "" {
		c.Host = "localhost"
	}
	if c.Port <= 0 {
		c.Port = 6379
	}
	if c.Name == "" {
		c.Name = "0"
	}

	db, err := strconv.ParseInt(c.Name, 10, 64)
	if err != nil {
		db = 0
	}
	client := redis5.NewClient(&redis5.Options{
		Addr:     fmt.Sprintf("%s:%d", c.Host, c.Port),
		Password: c.Password,
		DB:       int(db),
	})

	ret := new(PushRedisDB)
	ret.client = client
	ret.psm = c.PushServiceManager
	if ret.psm == nil {
		ret.psm = push.GetPushServiceManager()
	}
	return ret, nil
}

func (r *PushRedisDB) keyValueToDeliveryPoint(name string, value []byte) (dp *push.DeliveryPoint, err error) {
	psm := r.psm
	dp, err = psm.BuildDeliveryPointFromBytes(value)
	if err != nil {
		dp = nil
	}
	return
}

func (r *PushRedisDB) keyValueToPushServiceProvider(name string, value []byte) (psp *push.PushServiceProvider, err error) {
	psm := r.psm
	psp, err = psm.BuildPushServiceProviderFromBytes(value)
	if err != nil {
		psp = nil
	}
	return
}

func deliveryPointToValue(dp *push.DeliveryPoint) []byte {
	return dp.Marshal()
}

func pushServiceProviderToValue(psp *push.PushServiceProvider) []byte {
	return psp.Marshal()
}

func (r *PushRedisDB) mgetStrings(keys ...string) ([][]byte, error) {
	data, err := r.client.MGet(keys...).Result()
	if err != nil {
		return nil, err
	}
	results := make([][]byte, len(data))
	for i, result := range data {
		if r, ok := result.(string); ok {
			results[i] = []byte(r)
		} else if result == nil {
			results[i] = nil
		} else {
			// Nil?
			return nil, fmt.Errorf("Unexpected mget result type got %T %#v", result, result)
		}
	}
	return results, nil
}

func (r *PushRedisDB) mgetRawDeliveryPoints(deliveryPointNames ...string) ([][]byte, error) {
	var deliveryPointKeys []string
	for _, deliveryPointName := range deliveryPointNames {
		deliveryPointKeys = append(deliveryPointKeys, DELIVERY_POINT_PREFIX+deliveryPointName)
	}

	deliveryPointData, err := r.mgetStrings(deliveryPointKeys...)
	if err != nil {
		return nil, fmt.Errorf("Error getting deliveryPointKeys: %v", err)
	}
	return deliveryPointData, nil
}

func (r *PushRedisDB) GetDeliveryPoint(name string) (*push.DeliveryPoint, error) {
	b, err := r.client.Get(DELIVERY_POINT_PREFIX + name).Bytes()
	if err != nil {
		return nil, fmt.Errorf("GetDeliveryPoint failed: %v", err)
	}
	if len(b) == 0 {
		return nil, nil
	}
	return r.keyValueToDeliveryPoint(name, b)
}

func (r *PushRedisDB) SetDeliveryPoint(dp *push.DeliveryPoint) error {
	err := r.client.Set(DELIVERY_POINT_PREFIX+dp.Name(), deliveryPointToValue(dp), 0).Err()
	return err
}

func (r *PushRedisDB) GetPushServiceProvider(name string) (*push.PushServiceProvider, error) {
	b, err := r.client.Get(PUSH_SERVICE_PROVIDER_PREFIX + name).Bytes()
	if err != nil {
		return nil, fmt.Errorf("GetPushServiceProvider failed: %v", err)
	}
	if len(b) == 0 {
		return nil, nil
	}
	return r.keyValueToPushServiceProvider(name, b)
}

func (r *PushRedisDB) GetPushServiceProviderConfigs(names []string) ([]*push.PushServiceProvider, []error) {
	if len(names) == 0 {
		return nil, nil
	}
	keys := make([]string, len(names))
	for i, name := range names {
		keys[i] = PUSH_SERVICE_PROVIDER_PREFIX + name
	}
	values, err := r.mgetStrings(keys...)
	if err != nil {
		return nil, []error{fmt.Errorf("GetPushServiceProviderConfigs: %v", err)}
	}
	errors := make([]error, 0)
	psps := make([]*push.PushServiceProvider, 0)
	for i, value := range values {
		if value == nil {
			errors = append(errors, fmt.Errorf("Missing a PushServiceProvider for %q, key %q", names[i], keys[i]))
			continue
		}
		psp, err := r.keyValueToPushServiceProvider(names[i], value)
		if err != nil {
			errors = append(errors, fmt.Errorf("Invalid psp for %s: %v", names[i], err))
		} else {
			psps = append(psps, psp)
		}
	}
	return psps, errors
}

func (r *PushRedisDB) SetPushServiceProvider(psp *push.PushServiceProvider) error {
	if err := r.client.Set(PUSH_SERVICE_PROVIDER_PREFIX+psp.Name(), pushServiceProviderToValue(psp), 0).Err(); err != nil {
		return fmt.Errorf("SetPushServiceProvider %q failed: %v", psp.Name(), err)
	}
	return nil
}

func (r *PushRedisDB) RemoveDeliveryPoint(dp string) error {
	err := r.client.Del(DELIVERY_POINT_PREFIX + dp).Err()
	if err != nil {
		return fmt.Errorf("RemoveDP %q failed: %v", dp, err)
	}
	return nil
}

func (r *PushRedisDB) RemovePushServiceProvider(psp string) error {
	err := r.client.Del(PUSH_SERVICE_PROVIDER_PREFIX + psp).Err()
	if err != nil {
		return fmt.Errorf("RemovePSP %q failed: %v", psp, err)
	}
	return nil
}

func (r *PushRedisDB) GetDeliveryPointsNameByServiceSubscriber(srv, usr string) (map[string][]string, error) {
	keys := make([]string, 1)
	if !strings.Contains(usr, "*") && !strings.Contains(srv, "*") {
		keys[0] = SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX + srv + ":" + usr
	} else {
		var err error
		keys, err = r.client.Keys(SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX + srv + ":" + usr).Result()
		if err != nil {
			return nil, fmt.Errorf("GetDPsNameByServiceSubscriber dp lookup '%s:%s' failed: %v", srv, usr, err)
		}
	}

	ret := make(map[string][]string, len(keys))
	for _, k := range keys {
		m, err := r.client.SMembers(k).Result()
		if err != nil {
			return nil, fmt.Errorf("GetDPsNameByServiceSubscriber smembers %q failed: %v", k, err)
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
			dpl = append(dpl, bm)
			ret[s] = dpl
		}
	}
	return ret, nil
}

func (r *PushRedisDB) GetPushServiceProviderNameByServiceDeliveryPoint(srv, dp string) (string, error) {
	b, err := r.client.Get(SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_PROVIDER_PREFIX + srv + ":" + dp).Result()
	if err != nil {
		return "", fmt.Errorf("GetPSPNameByServiceDP failed: %v", err)
	}
	return b, nil
}

func (r *PushRedisDB) AddDeliveryPointToServiceSubscriber(srv, sub, dp string) error {
	i, err := r.client.SAdd(SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX+srv+":"+sub, dp).Result()
	if err != nil {
		return fmt.Errorf("AddDPToServiceSubscriber failed: %v", err)
	}
	if i == 0 { // Already exists
		return nil
	}
	err = r.client.Incr(DELIVERY_POINT_COUNTER_PREFIX + dp).Err()
	if err != nil {
		return fmt.Errorf("AddDPToServiceSubscriber count tracking failed: %v", err)
	}
	return nil
}

func (r *PushRedisDB) RemoveDeliveryPointFromServiceSubscriber(srv, sub, dp string) error {
	j, err := r.client.SRem(SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX+srv+":"+sub, dp).Result()
	if err != nil {
		return fmt.Errorf("Removing the delivery point pointer %q from \"%s:%s\" failed", dp, srv, sub)
	}
	if j == 0 {
		return nil
	}
	i, e := r.client.Decr(DELIVERY_POINT_COUNTER_PREFIX + dp).Result()
	if e != nil {
		return fmt.Errorf("Failed to decrement number of subscribers using dp %q: %v", dp, e)
	}
	if i <= 0 {
		e0 := r.client.Del(DELIVERY_POINT_COUNTER_PREFIX + dp).Err()
		if e0 != nil {
			return fmt.Errorf("Failed to remove counter for %q: %v", dp, e0)
		}
		e1 := r.client.Del(DELIVERY_POINT_PREFIX + dp).Err()
		if e1 != nil {
			return fmt.Errorf("Failed to remove delivery point info for %q: %v", dp, e1)
		}
	}
	return nil
}

// removeMissingDeliveryPointFromServiceSubscriber removes any associations from a subscription list to a dp with missing subscriptions.
func (r *PushRedisDB) removeMissingDeliveryPointFromServiceSubscriber(service, subscriber, dpName string, logger log.Logger) {
	// Precondition: DELIVERY_POINT_PREFIX + dp was already missing. No need to remove it.
	e0 := r.client.SRem(SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX+service+":"+subscriber, dpName).Err()
	if e0 != nil {
		logger.Errorf("Error cleaning up delivery point with missing data for dp %q service %q FROM user %q's delivery points: %v", dpName, subscriber, service, e0)
	}
	e1 := r.client.Del(DELIVERY_POINT_COUNTER_PREFIX + dpName).Err() // TODO: Err instead
	if e1 != nil {
		logger.Errorf("Error cleaning up count for delivery point with missing data for delivery point %q (while processing subscriber %q, service %q): %v", dpName, subscriber, service, e1)
	}
}

func (r *PushRedisDB) SetPushServiceProviderOfServiceDeliveryPoint(srv, dp, psp string) error {
	err := r.client.Set(SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_PROVIDER_PREFIX+srv+":"+dp, psp, 0).Err()
	if err != nil {
		return fmt.Errorf("SetPSPOfServiceDP failed for \"%s:%s\": %v", srv, dp, err)
	}
	return nil
}

func (r *PushRedisDB) RemovePushServiceProviderOfServiceDeliveryPoint(srv, dp string) error {
	err := r.client.Del(SERVICE_DELIVERY_POINT_TO_PUSH_SERVICE_PROVIDER_PREFIX + srv + ":" + dp).Err()
	if err != nil {
		return fmt.Errorf("RemovePSPOfServiceDP failed for \"%s:%s\": %v", srv, dp, err)
	}
	return err
}

func (r *PushRedisDB) GetPushServiceProvidersByService(srv string) ([]string, error) {
	m, err := r.client.SMembers(SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX + srv).Result()
	if err != nil {
		return nil, fmt.Errorf("GetPSPsByService failed for %q: %v", srv, err)
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
	// TODO: pipelined
	err := r.client.SRem(SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX+srv, psp).Err()
	if err != nil {
		return fmt.Errorf("RemovePSPFromService failed for psp %q of service %q: %v", psp, srv, err)
	}
	// Unfortunately, a service name might be associated with multiple push service providers, so the check seems to be needed. (/addpsp allows psps with the same service name but different pushservicetypes, if I understand correctly)
	exists, err := r.client.Exists(SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX + srv).Result()
	if err != nil {
		return fmt.Errorf("Unable to determine if service %q still exists after removing psp %q: %v", srv, psp, err)
	}
	if !exists {
		err := r.client.SRem(SERVICES_SET, srv).Err() // Non-essential. Used to list services in API.
		if err != nil {
			return fmt.Errorf("Unable to remove %q from set of services", srv)
		}
	}
	return nil
}

func (r *PushRedisDB) AddPushServiceProviderToService(srv, psp string) error {
	// TODO: pipelined
	r.client.SAdd(SERVICES_SET, srv).Err() // Non-essential. Used to list services in API.
	err := r.client.SAdd(SERVICE_TO_PUSH_SERVICE_PROVIDERS_PREFIX+srv, psp).Err()
	if err != nil {
		return fmt.Errorf("AddPSPToService failed for psp %q of service %q: %v", psp, srv, err)
	}
	return nil
}

func (r *PushRedisDB) GetServiceNames() ([]string, error) {
	serviceList, err := r.client.SMembers(SERVICES_SET).Result()
	if err != nil {
		return nil, fmt.Errorf("Could not get services from redis: %v", err)
	}
	return serviceList, nil
}

// RebuildServiceSet builds the set of unique service
func (r *PushRedisDB) RebuildServiceSet() error {
	// Run KEYS, then replace the PSP set with the result of KEYS.
	// If any step fails, then return an error.
	pspKeys, err := r.client.Keys(PUSH_SERVICE_PROVIDER_PREFIX + "*").Result()
	if err != nil {
		return fmt.Errorf("Failed to fetch PSPs using redis KEYS command: %v", err)
	}

	if len(pspKeys) == 0 {
		return nil
	}

	pspNames := make([]string, len(pspKeys))
	N := len(PUSH_SERVICE_PROVIDER_PREFIX)
	for i, key := range pspKeys {
		if len(key) < N || key[:N] != PUSH_SERVICE_PROVIDER_PREFIX {
			return fmt.Errorf("KEYS %s* returned %q - this shouldn't happen", PUSH_SERVICE_PROVIDER_PREFIX, key)
		}
		pspNames[i] = key[N:]
	}

	psps, errs := r.GetPushServiceProviderConfigs(pspNames)
	if len(errs) > 0 {
		return fmt.Errorf("RebuildServiceSet: found one or more invalid psps: %v", errs)
	}
	serviceNameSet := make(map[string]bool)
	for i, psp := range psps {
		serviceName, ok := psp.FixedData["service"]
		if !ok || serviceName == "" {
			return fmt.Errorf("RebuildServiceSet: found PSP %q with empty service name: data=%v", pspNames[i], psp)
		}
		serviceNameSet[serviceName] = true
	}
	var serviceNameList []interface{}
	for serviceName := range serviceNameSet {
		serviceNameList = append(serviceNameList, serviceName)
	}
	if len(serviceNameList) > 0 {
		err := r.client.SAdd(SERVICES_SET, serviceNameList...).Err()
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *PushRedisDB) FlushCache() error {
	return r.client.Save().Err()
}

func (r *PushRedisDB) GetSubscriptions(queryServices []string, subscriber string, logger log.Logger) ([]map[string]string, error) {
	if len(queryServices) == 0 {
		definedServices, err := r.GetServiceNames()
		if err != nil {
			return nil, fmt.Errorf("GetSubscriptions: %v", err)
		}
		queryServices = definedServices
	}

	var serviceForDeliveryPointNames []string
	var deliveryPointNames []string
	for _, service := range queryServices {
		if service == "" {
			logger.Errorf("empty service defined")
			continue
		}

		deliveryPoints, err := r.client.SMembers(SERVICE_SUBSCRIBER_TO_DELIVERY_POINTS_PREFIX + service + ":" + subscriber).Result()

		if err != nil {
			return nil, fmt.Errorf("Could not get subscriber information")
		}
		if len(deliveryPoints) == 0 {
			// it is OK to not have delivery points for a service
			continue
		}
		for _, deliveryPointName := range deliveryPoints {
			deliveryPointNames = append(deliveryPointNames, deliveryPointName)
			serviceForDeliveryPointNames = append(serviceForDeliveryPointNames, service)
		}
	}

	if len(deliveryPointNames) == 0 {
		// Return empty map without error.
		return make([]map[string]string, 0), nil
	}

	deliveryPointData, err := r.mgetRawDeliveryPoints(deliveryPointNames...)
	if err != nil {
		return nil, err
	}

	// Unserialize the subscriptions. If there are any invalid subscriptions, remove them and log it.
	// serviceForDeliveryPointNames, deliveryPointNames, and deliveryPointData all use the same index i.
	var subscriptions []map[string]string
	for i, data := range deliveryPointData {
		dpName := deliveryPointNames[i]
		service := serviceForDeliveryPointNames[i]
		if data != nil {
			subscriptionData, err := push.UnserializeSubscription(data)
			if err != nil {
				logger.Errorf("Error unserializing subscription for delivery point data for dp %q user %q service %q data %v: %v", dpName, subscriber, service, subscriptionData, err)
				continue
			}
			subscriptions = append(subscriptions, subscriptionData)
		} else {
			logger.Errorf("Redis error fetching subscriber delivery point data for dp %q user %q service %q, removing...", dpName, subscriber, service)
			// The multi-get did not encounter an error, so this key is missing.
			// Try to remove this delivery point as cleanly as possible, removing counts, etc.
			r.removeMissingDeliveryPointFromServiceSubscriber(service, subscriber, dpName, logger)
		}
	}

	return subscriptions, nil
}
