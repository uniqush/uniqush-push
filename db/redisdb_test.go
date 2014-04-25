package db

import (
	"reflect"
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

var redisTestConfig = &DatabaseConfig{
	Engine:   "redis",
	Host:     "localhost",
	Database: "1",
}

func TestRedis(t *testing.T) {
	db, err := GetPushDatabase(redisTestConfig)
	if err != nil {
		t.Fatal(err)
	}
	testAddDelProvider(db, t)
}

func TestRedisPairDeliveryPoint(t *testing.T) {
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
	err = db.AddProvider(p)
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
	err = db.AddProvider(p1)
	if err != nil {
		t.Fatal(err)
	}

	p2 := &simpleProvider{
		ApiKey: "key2",
	}
	err = db.AddProvider(p2)
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
	err = db.AddProvider(p)
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
