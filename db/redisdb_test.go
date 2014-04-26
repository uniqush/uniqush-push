package db

import (
	"reflect"
	"testing"

	"github.com/garyburd/redigo/redis"
	"github.com/uniqush/uniqush-push/push"
)

var redisTestConfig = &DatabaseConfig{
	Engine:   "redis",
	Host:     "localhost",
	Database: "1",
}

func redisClearDatabase(config *DatabaseConfig, t *testing.T) {
	db, err := GetPushDatabase(redisTestConfig)
	if err != nil {
		t.Fatal(err)
	}
	redisdb := db.(*redisPushDatabase)
	conn := redisdb.pool.Get()
	defer conn.Close()

	_, err = conn.Do("FLUSHDB")
	if err != nil {
		t.Fatal(err)
	}
	return
}

func redisIntegrityTest(config *DatabaseConfig, t *testing.T) {
	db, err := GetPushDatabase(redisTestConfig)
	if err != nil {
		t.Fatal(err)
	}
	redisdb := db.(*redisPushDatabase)
	conn := redisdb.pool.Get()
	defer conn.Close()

	reply, err := conn.Do("KEYS", "*")
	if err != nil {
		t.Fatal(err)
	}
	values, err := redis.Values(reply, err)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) > 0 {
		t.Fatalf("%v values are still in the redis database %v",
			len(values), config.Database)
	}
}

func TestRedis(t *testing.T) {
	db, err := GetPushDatabase(redisTestConfig)
	if err != nil {
		t.Fatal(err)
	}
	testPushDatabaseImpl(db, t,
		func() {
			redisClearDatabase(redisTestConfig, t)
		},
		func() {
			redisIntegrityTest(redisTestConfig, t)
		})
}

func TestLayeredRedis(t *testing.T) {
	var cacheConfig DatabaseConfig
	cacheConfig = *redisTestConfig
	cacheConfig.Database = "2"
	cacheConfig.IsCache = true
	cache, err := GetPushDatabase(&cacheConfig)
	if err != nil {
		t.Fatal(err)
	}
	var dbConfig DatabaseConfig
	dbConfig = *redisTestConfig
	dbConfig.Database = "1"
	database, err := GetPushDatabase(&dbConfig)
	if err != nil {
		t.Fatal(err)
	}

	db := NewLayeredPushDatabase(cache, database)

	testPushDatabaseImpl(db, t,
		func() {
			redisClearDatabase(&cacheConfig, t)
			redisClearDatabase(&dbConfig, t)
		},
		func() {
			redisIntegrityTest(&cacheConfig, t)
			redisIntegrityTest(&dbConfig, t)
		})
	cacheConfig.CacheType = CACHE_TYPE_LRU
	cache, err = GetPushDatabase(&cacheConfig)
	if err != nil {
		t.Fatal(err)
	}
	db = NewLayeredPushDatabase(cache, database)
	testPushDatabaseImpl(db, t,
		func() {
			redisClearDatabase(&cacheConfig, t)
			redisClearDatabase(&dbConfig, t)
		},
		func() {
			redisIntegrityTest(&cacheConfig, t)
			redisIntegrityTest(&dbConfig, t)
		})
}

func TestRedisPairDeliveryPoint(t *testing.T) {
	redisClearDatabase(redisTestConfig, t)
	defer redisIntegrityTest(redisTestConfig, t)
	db, err := GetPushDatabase(redisTestConfig)
	if err != nil {
		t.Fatal(err)
	}

	ps := &simplePushService{}
	ps.This = ps
	push.RegisterPushService(ps)

	p := &simpleProvider{
		ApiKey: "key",
	}
	err = db.UpdateProvider(p)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		err = db.DelProvider(p)
		if err != nil {
			t.Fatal(err)
		}
	}()

	dp := &simpleDeliveryPoint{
		DevToken: "tokentoken",
	}
	rdb := db.(*redisPushDatabase)

	pair := &ProviderDeliveryPointPair{}
	pair.DeliveryPoint = dp
	err = rdb.pairDeliveryPoints(pair)

	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(p, pair.Provider) {
		t.Fatalf("%+v != %+v", p, pair.Provider)
	}
}

func TestRedisPairDeliveryPointWithNoProvider(t *testing.T) {
	redisClearDatabase(redisTestConfig, t)
	defer redisIntegrityTest(redisTestConfig, t)
	db, err := GetPushDatabase(redisTestConfig)
	if err != nil {
		t.Fatal(err)
	}

	ps := &simplePushService{}
	ps.This = ps
	push.RegisterPushService(ps)

	dp := &simpleDeliveryPoint{
		DevToken: "tokentoken",
	}
	rdb := db.(*redisPushDatabase)

	pair := &ProviderDeliveryPointPair{}
	pair.DeliveryPoint = dp
	err = rdb.pairDeliveryPoints(pair)

	if err == nil {
		t.Fatal("should fail")
	}
}

func TestRedisPairDeliveryPointWithSpecifiedProvider(t *testing.T) {
	redisClearDatabase(redisTestConfig, t)
	defer redisIntegrityTest(redisTestConfig, t)
	db, err := GetPushDatabase(redisTestConfig)
	if err != nil {
		t.Fatal(err)
	}

	ps := &simplePushService{}
	ps.This = ps
	push.RegisterPushService(ps)

	p1 := &simpleProvider{
		ApiKey: "key1",
	}
	err = db.UpdateProvider(p1)
	if err != nil {
		t.Fatal(err)
	}

	p2 := &simpleProvider{
		ApiKey: "key2",
	}
	err = db.UpdateProvider(p2)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		err = db.DelProvider(p1)
		if err != nil {
			t.Fatal(err)
		}
		err = db.DelProvider(p2)
		if err != nil {
			t.Fatal(err)
		}
	}()

	dp := &simpleDeliveryPoint{
		DevToken:     "tokentoken",
		ProviderName: p1.UniqId(),
	}
	rdb := db.(*redisPushDatabase)

	pair := &ProviderDeliveryPointPair{}
	pair.DeliveryPoint = dp
	err = rdb.pairDeliveryPoints(pair)

	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(p1, pair.Provider) {
		t.Fatalf("%+v != %+v", p1, pair.Provider)
	}
}

func TestPairDeliveryPointWithUnknownProvider(t *testing.T) {
	redisClearDatabase(redisTestConfig, t)
	defer redisIntegrityTest(redisTestConfig, t)
	db, err := GetPushDatabase(redisTestConfig)
	if err != nil {
		t.Fatal(err)
	}

	ps := &simplePushService{}
	ps.This = ps
	push.RegisterPushService(ps)

	p := &simpleProvider{
		ApiKey: "key",
	}
	err = db.UpdateProvider(p)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		err = db.DelProvider(p)
		if err != nil {
			t.Fatal(err)
		}
	}()

	dp := &simpleDeliveryPoint{
		DevToken:     "tokentoken",
		ProviderName: p.UniqId() + "notyou",
	}
	rdb := db.(*redisPushDatabase)

	pair := &ProviderDeliveryPointPair{}
	pair.DeliveryPoint = dp
	err = rdb.pairDeliveryPoints(pair)

	if err == nil {
		t.Fatal("should fail")
	}
}
