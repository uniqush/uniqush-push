package db

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/uniqush/uniqush-push/push"
)

func init() {
	RegisterDatabase("redis", newRedisPushDatabase)
}

type redisPushDatabase struct {
	pool      *redis.Pool
	isCache   bool
	cacheType int
}

func newRedisPushDatabase(dbconfig *DatabaseConfig) (db PushDatabase, err error) {
	host := "localhost"
	port := 6379
	password := ""
	dbname := "0"

	if dbconfig != nil {
		if len(dbconfig.Host) > 0 {
			host = dbconfig.Host
		}
		if len(dbconfig.Password) > 0 {
			password = dbconfig.Password
		}
		if len(dbconfig.Database) > 0 {
			dbname = dbconfig.Database
		}
		if dbconfig.Port > 0 {
			port = dbconfig.Port
		}
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%v", port))
	dial := func() (redis.Conn, error) {
		c, err := redis.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		if len(password) > 0 {
			if _, err := c.Do("AUTH", password); err != nil {
				c.Close()
				return nil, err
			}
		}
		if _, err := c.Do("SELECT", dbname); err != nil {
			c.Close()
			return nil, err
		}
		return c, err
	}
	testOnBorrow := func(c redis.Conn, t time.Time) error {
		_, err := c.Do("PING")
		return err
	}

	pool := &redis.Pool{
		MaxIdle:      3,
		IdleTimeout:  240 * time.Second,
		Dial:         dial,
		TestOnBorrow: testOnBorrow,
	}

	c, err := dial()
	if err != nil {
		err = fmt.Errorf("unable to connect to redis: %v", err)
		return
	}
	defer c.Close()
	_, err = c.Do("PING")
	if err != nil {
		err = fmt.Errorf("unable to connect to redis: %v", err)
		return
	}

	ret := new(redisPushDatabase)
	ret.pool = pool
	ret.isCache = dbconfig.IsCache
	ret.cacheType = dbconfig.CacheType
	db = ret
	return
}

// Provider's key:
//   provider:<push service>:<service>:<uniqid>
// Delivery Point's key:
//   dp:<push service>:<service>:<provider uniqid>:<subscriber>
// Pair's key:
//   pair:<service>:<subscriber>

func buildPairLoopUpKey(service, subscriber string) string {
	return fmt.Sprintf("pair:%v:%v", service, subscriber)
}

func buildPairKey(pair *ProviderDeliveryPointPair) string {
	return fmt.Sprintf("pair:%v:%v", pair.DeliveryPoint.Service(), pair.DeliveryPoint.Subscriber())
}

func buildProviderKey(provider push.Provider) string {
	return fmt.Sprintf("provider:%v:%v:%v", provider.PushService(), provider.Service(), provider.UniqId())
}

func buildProviderKeyFromDeliveryPoint(dp push.DeliveryPoint) string {
	return fmt.Sprintf("provider:%v:%v:%v", dp.PushService(), dp.Service(), dp.Provider())
}

func buildProviderLoopUpKeyFromDeliveryPoint(dp push.DeliveryPoint) string {
	return fmt.Sprintf("provider:%v:%v:*", dp.PushService(), dp.Service())
}

func buildDeliveryPointKeyFromPair(pair *ProviderDeliveryPointPair) string {
	return fmt.Sprintf("dp:%v:%v:%v:%v:%v",
		pair.DeliveryPoint.PushService(),
		pair.DeliveryPoint.Service(),
		pair.Provider.UniqId(),
		pair.DeliveryPoint.Subscriber(),
		pair.DeliveryPoint.UniqId())
}

func buildDeliveryPointLookUpKey(provider push.Provider, uniqId string) string {
	return fmt.Sprintf("dp:%v:%v:%v:*:%v", provider.PushService(),
		provider.Service(), provider.UniqId(), uniqId)
}

func (self *redisPushDatabase) AddProvider(provider push.Provider) error {
	// Cache will never store this info
	if self.isCache {
		return nil
	}
	conn := self.pool.Get()
	defer conn.Close()

	key := buildProviderKey(provider)
	ps, err := push.GetPushService(provider)
	if err != nil {
		return err
	}
	data, err := ps.MarshalProvider(provider)
	if err != nil {
		return err
	}
	reply, err := conn.Do("SET", key, data)
	if err != nil {
		return err
	}
	n, err := redis.String(reply, err)
	if err != nil {
		return err
	}
	if n != "OK" {
		err = fmt.Errorf("Trying to add provider %v. received message: %v", provider.UniqId(), n)
		return err
	}
	return nil
}

func (self *redisPushDatabase) DelProvider(provider push.Provider) error {
	if self.isCache {
		return nil
	}
	conn := self.pool.Get()
	defer conn.Close()

	key := buildProviderKey(provider)
	ps, err := push.GetPushService(provider)
	if err != nil {
		return err
	}
	data, err := ps.MarshalProvider(provider)
	if err != nil {
		return err
	}
	reply, err := conn.Do("DEL", key, data)
	if err != nil {
		return err
	}
	n, err := redis.Int(reply, err)
	if err != nil {
		return err
	}
	if n != 1 {
		err = fmt.Errorf("Trying to del provider %v. But deleted %v values in redis", provider.UniqId(), n)
		return err
	}
	return nil
}

func (self *redisPushDatabase) getProviderWithKey(pkey string, ps push.PushService, conn redis.Conn) (p push.Provider, err error) {
	reply, err := conn.Do("GET", pkey)
	if err != nil {
		return
	}
	data, err := redis.Bytes(reply, err)
	if err != nil {
		return
	}
	ep := ps.EmptyProvider()
	err = ps.UnmarshalProvider(data, ep)
	if err != nil {
		return
	}
	p = ep
	return
}

func (self *redisPushDatabase) pairDp(dp push.DeliveryPoint) (p push.Provider, err error) {
	lookupkey := buildProviderLoopUpKeyFromDeliveryPoint(dp)
	// In redis 2.8, SCAN was introduced and is a better choice than KEYS.
	// However, this version is currently not widely used.
	// Once 2.8 is in most distro's repo, we should change to SCAN instead of KEYS
	ps, err := push.GetPushService(dp)
	if err != nil {
		return
	}

	conn := self.pool.Get()
	defer conn.Close()

	reply, err := conn.Do("KEYS", lookupkey)
	if err != nil {
		return
	}
	values, err := redis.Values(reply, err)
	if err != nil {
		return
	}
	if len(values) == 0 {
		err = fmt.Errorf("unable to find a %v provider for %v under service %v.",
			dp.PushService(), dp.UniqId(), dp.Service())
		return
	}

	pkey := ""
	for _, v := range values {
		pkey, err = redis.String(v, err)
		if err != nil {
			err = fmt.Errorf("unable to recover the provider's key for delivery point %v", dp.UniqId())
			return
		}
		p, err = self.getProviderWithKey(pkey, ps, conn)
		if err != nil {
			continue
		}
	}
	if err != nil {
		p = nil
		return
	}
	return
}

func (self *redisPushDatabase) pairDeliveryPoints(pairs ...*ProviderDeliveryPointPair) error {
	for _, pair := range pairs {
		if pair.Provider != nil {
			continue
		}
		if pair.DeliveryPoint == nil {
			err := fmt.Errorf("empty delivery point found in the pair.")
			return err
		}
		if self.isCache {
			return fmt.Errorf("redis is used as cache but delivery point %v is not paired with a provider.", pair.DeliveryPoint.UniqId())
		}
		if pair.DeliveryPoint.Provider() != "" {
			ps, err := push.GetPushService(pair.DeliveryPoint)
			if err != nil {
				return err
			}
			conn := self.pool.Get()
			pkey := buildProviderKeyFromDeliveryPoint(pair.DeliveryPoint)
			pair.Provider, err = self.getProviderWithKey(pkey, ps, conn)
			if err != nil {
				conn.Close()
				return err
			}
			conn.Close()
		} else {
			var err error
			pair.Provider, err = self.pairDp(pair.DeliveryPoint)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (self *redisPushDatabase) AddPairs(pairs ...*ProviderDeliveryPointPair) (newpairs []*ProviderDeliveryPointPair, err error) {
	err = self.pairDeliveryPoints(pairs...)
	if err != nil {
		return
	}
	conn := self.pool.Get()
	defer conn.Close()
	err = conn.Send("MULTI")
	if err != nil {
		return
	}
	for _, pair := range pairs {
		if pair.Provider == nil {
			conn.Do("DISCARD")
			err = fmt.Errorf("unable to pair delivery point %v (and its weird)", pair.DeliveryPoint.UniqId())
			return
		}
		if pair.Provider.PushService() != pair.DeliveryPoint.PushService() {
			conn.Do("DISCARD")
			err = fmt.Errorf("paired delivery point %v under %v with provider %v under %v", pair.DeliveryPoint.UniqId(), pair.DeliveryPoint.PushService(), pair.Provider.UniqId(), pair.Provider.PushService())
			return
		}

		pair.DeliveryPoint.PairProvider(pair.Provider)
		pairkey := buildPairKey(pair)
		var data []byte
		data, err = pair.Bytes()
		if err != nil {
			conn.Do("DISCARD")
			return
		}
		err = conn.Send("SADD", pairkey, data)
		if err != nil {
			conn.Do("DISCARD")
			return
		}

		if !self.isCache {
			var ps push.PushService
			ps, err = push.GetPushService(pair)
			if err != nil {
				conn.Do("DISCARD")
				return
			}
			dpkey := buildDeliveryPointKeyFromPair(pair)
			var ddata []byte
			ddata, err = ps.MarshalDeliveryPoint(pair.DeliveryPoint)
			if err != nil {
				conn.Do("DISCARD")
				return
			}
			err = conn.Send("SET", dpkey, ddata)
			if err != nil {
				conn.Do("DISCARD")
				return
			}
		}
	}
	_, err = conn.Do("EXEC")
	if err != nil {
		err = fmt.Errorf("unable to exec: %v", err)
		return
	}

	newpairs = pairs
	return
}

func hasWildcard(str string) bool {
	return strings.Contains(str, "*") || strings.Contains(str, "?")
}

func (self *redisPushDatabase) getPairsByKey(key string) (pairs []*ProviderDeliveryPointPair, err error) {
	conn := self.pool.Get()
	defer conn.Close()

	reply, err := conn.Do("SMEMBERS", key)
	if err != nil {
		err = fmt.Errorf("unable to get pair %v: %v", key, err)
		return
	}
	values, err := redis.Values(reply, err)
	if err != nil {
		err = fmt.Errorf("unable to read pair %v: %v", key, err)
		return
	}
	ret := make([]*ProviderDeliveryPointPair, 0, len(values))
	var data []byte
	for i, v := range values {
		data, err = redis.Bytes(v, err)
		if err != nil {
			err = fmt.Errorf("unable to convert pair %v's %v data into bytes: %v", key, i, err)
			return
		}
		pair := new(ProviderDeliveryPointPair)
		err = pair.Load(data)
		if err != nil {
			err = fmt.Errorf("unable to load pair %v(%v): %v", key, i, err)
			return
		}
		if pair.DeliveryPoint != nil && pair.Provider != nil {
			ret = append(ret, pair)
		}
	}
	pairs = ret
	return
}

func (self *redisPushDatabase) LoopUpPairs(service, subscriber string) (pairs []*ProviderDeliveryPointPair, err error) {
	keys := make([]string, 0, 10)
	// If it has wild card, we need to treat it differently.
	if hasWildcard(service) || hasWildcard(subscriber) {
		// If redis is used as a cache, then it is not able to handle wild card.
		// Because the cache may only contain partial data and may not be
		// able to enumerate all matched services/subscribers.
		// Use the next layer to handle it.
		if self.isCache && self.cacheType != CACHE_TYPE_ALWAYS_IN {
			err = fmt.Errorf("cache is not able to handle wild card.")
			return
		}
		lookupkey := buildPairLoopUpKey(service, subscriber)

		conn := self.pool.Get()
		defer conn.Close()

		var reply interface{}
		var values []interface{}

		// XXX use SCAN when 2.8 is widely used
		reply, err = conn.Do("KEYS", lookupkey)
		if err != nil {
			err = fmt.Errorf("unable to lookup pairs under pattern %v: %v", lookupkey, err)
			return
		}
		values, err = redis.Values(reply, err)
		if err != nil {
			err = fmt.Errorf("unable to read pair keys under pattern %v: %v", lookupkey, err)
			return
		}
		for _, v := range values {
			var key string
			key, err = redis.String(v, err)
			if err != nil {
				err = fmt.Errorf("unable to recover pair key under pattern %v: %v", lookupkey, err)
				return
			}
			keys = append(keys, key)
		}
	} else {
		keys = append(keys, buildPairLoopUpKey(service, subscriber))
	}

	rets := make([][]*ProviderDeliveryPointPair, len(keys))
	N := 0
	for i, key := range keys {
		rets[i], err = self.getPairsByKey(key)
		if err != nil {
			return
		}
		N += len(rets[i])
	}

	pairs = make([]*ProviderDeliveryPointPair, 0, N)
	for _, ps := range rets {
		pairs = append(pairs, ps...)
	}
	return
}

func (self *redisPushDatabase) DelDeliveryPoint(provider push.Provider, dp push.DeliveryPoint) error {
	pairkey := buildPairLoopUpKey(dp.Service(), dp.Subscriber())
	if self.isCache && self.cacheType != CACHE_TYPE_ALWAYS_IN {
		// In this case, we only need to remove all pairs in the cache
		// and let the next read operation load the new pairs from the backend database.
		conn := self.pool.Get()
		defer conn.Close()
		_, err := conn.Do("DEL", pairkey)
		if err != nil {
			return err
		}
		return nil
	}

	pair := &ProviderDeliveryPointPair{
		Provider:      provider,
		DeliveryPoint: dp,
	}

	if provider == nil {
		// In this case, redis should store all pairs.
		pairs, err := self.getPairsByKey(pairkey)
		if err != nil {
			err = fmt.Errorf("unable to delete the delivery point %v: %v", dp.UniqId(), err)
			return err
		}

		for _, p := range pairs {
			if p.DeliveryPoint.UniqId() == dp.UniqId() {
				if p.Provider == nil {
					panic("pair has a nil provider")
				}
				pair = p
			}
		}
		if pair.Provider == nil {
			return nil
		}
	}

	data, err := pair.Bytes()
	if err != nil {
		return err
	}

	conn := self.pool.Get()
	defer conn.Close()

	err = conn.Send("MULTI")
	if err != nil {
		err = fmt.Errorf("unable to del the delivery point %v. wrong with MULTI: %v", dp.UniqId(), err)
		return err
	}

	err = conn.Send("SREM", pairkey, data)
	if err != nil {
		err = fmt.Errorf("unable to del the delivery point %v: %v", dp.UniqId(), err)
		return err
	}
	if !self.isCache {
		dpkey := buildDeliveryPointKeyFromPair(pair)
		err = conn.Send("DEL", dpkey)
		if err != nil {
			err = fmt.Errorf("unable to del the delivery point %v: %v", dp.UniqId(), err)
			return err
		}
	}

	_, err = conn.Do("EXEC")
	if err != nil {
		err = fmt.Errorf("del dp %v error. unable to exec: %v", dp.UniqId(), err)
		return err
	}
	return nil
}

func (self *redisPushDatabase) UpdateProvider(provider push.Provider) error {
	// Update all pairs.
	// XXX This is really time consuming.
	pairs, err := self.LoopUpPairs(provider.Service(), "*")
	if err != nil {
		return err
	}
	conn := self.pool.Get()
	defer conn.Close()
	err = conn.Send("MULTI")
	if err != nil {
		return err
	}
	// Update keys related to this provider
	for _, pair := range pairs {
		pairkey := buildPairKey(pair)

		if pair.Provider.UniqId() == provider.UniqId() {
			data, err := pair.Bytes()
			if err != nil {
				conn.Do("DISCARD")
				return err
			}
			err = conn.Send("SREM", pairkey, data)
			if err != nil {
				conn.Do("DISCARD")
				return err
			}
			pair.Provider = provider
			// The delivery point is changed.
			// We need to update the database correspondingly.
			data, err = pair.Bytes()
			if err != nil {
				conn.Do("DISCARD")
				return err
			}
			err = conn.Send("SADD", pairkey, data)
			if err != nil {
				conn.Do("DISCARD")
				return err
			}
			if !self.isCache && pair.DeliveryPoint.PairProvider(provider) {
				var ps push.PushService
				ps, err = push.GetPushService(pair)
				if err != nil {
					conn.Do("DISCARD")
					return err
				}
				dpkey := buildDeliveryPointKeyFromPair(pair)
				var ddata []byte
				ddata, err = ps.MarshalDeliveryPoint(pair.DeliveryPoint)
				if err != nil {
					conn.Do("DISCARD")
					return err
				}
				err = conn.Send("SET", dpkey, ddata)
				if err != nil {
					conn.Do("DISCARD")
					return err
				}
			}
		}
	}

	// Finally, update the provider
	if !self.isCache {
		pkey := buildProviderKey(provider)
		ps, err := push.GetPushService(provider)
		if err != nil {
			conn.Do("DISCARD")
			return err
		}
		data, err := ps.MarshalProvider(provider)
		if err != nil {
			conn.Do("DISCARD")
			return err
		}
		err = conn.Send("SET", pkey, data)
		if err != nil {
			conn.Do("DISCARD")
			return err
		}
	}
	_, err = conn.Do("EXEC")
	if err != nil {
		return err
	}
	return nil
}

func (self *redisPushDatabase) UpdateDeliveryPoint(dp push.DeliveryPoint) error {
	pairs, err := self.LoopUpPairs(dp.Service(), dp.Subscriber())
	if err != nil {
		return err
	}
	conn := self.pool.Get()
	defer conn.Close()
	err = conn.Send("MULTI")
	if err != nil {
		return err
	}
	// Update keys related to this delivery point
	for _, pair := range pairs {
		pairkey := buildPairKey(pair)
		if pair.DeliveryPoint.UniqId() == dp.UniqId() {
			data, err := pair.Bytes()
			if err != nil {
				conn.Do("DISCARD")
				return err
			}
			err = conn.Send("SREM", pairkey, data)
			if err != nil {
				conn.Do("DISCARD")
				return err
			}
			pair.DeliveryPoint = dp
			data, err = pair.Bytes()
			if err != nil {
				conn.Do("DISCARD")
				return err
			}
			err = conn.Send("SADD", pairkey, data)
			if err != nil {
				conn.Do("DISCARD")
				return err
			}
			if !self.isCache {
				dpkey := buildDeliveryPointKeyFromPair(pair)
				ps, err := push.GetPushService(dp)
				if err != nil {
					conn.Do("DISCARD")
					return err
				}
				data, err := ps.MarshalProvider(dp)
				if err != nil {
					conn.Do("DISCARD")
					return err
				}
				err = conn.Send("SET", dpkey, data)
				if err != nil {
					conn.Do("DISCARD")
					return err
				}
			}
		}
	}

	_, err = conn.Do("EXEC")
	if err != nil {
		return err
	}
	return nil

}
