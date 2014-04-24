package db

import "testing"

var redisTestConfig = &DatabaseConfig{
	Engine: "redis",
	Host:   "localhost",
}

func TestRedis(t *testing.T) {
	db, err := GetPushDatabase(redisTestConfig)
	if err != nil {
		t.Fatal(err)
	}
	testAddDelProvider(db, t)
}
