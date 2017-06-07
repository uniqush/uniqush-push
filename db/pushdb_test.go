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
	"fmt"
	"strconv"
	"testing"

	redis5 "gopkg.in/redis.v5"
)

var dbconf *DatabaseConfig

func init() {
	dbconf = new(DatabaseConfig)
	dbconf.Host = ""
	dbconf.Port = 0
	dbconf.Name = "0"
	dbconf.Engine = "redis"
}

func connectDatabase() (db PushDatabase, err error) {
	db, err = NewPushDatabaseWithoutCache(dbconf)
	return
}

func clearData() {
	c := dbconf
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

	client.FlushDb() // Flush the database which pushredisdb.go used.
}

func TestConnectAndDelete(t *testing.T) {
	_, err := connectDatabase()
	if err != nil {
		t.Errorf("Error: %v", err)
	}
	clearData()
}

func TestInsertAndGetPushServiceProviders(t *testing.T) {
}
