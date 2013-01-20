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
	redis "github.com/monnand/goredis"
	. "github.com/uniqush/uniqush-push/push"
	"strconv"
	"testing"
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
	var client redis.Client
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
	client.Addr = fmt.Sprintf("%s:%d", c.Host, c.Port)
	client.Password = c.Password
	var err error
	client.Db, err = strconv.Atoi(c.Name)
	if err != nil {
		client.Db = 0
	}
	client.Flush(true)
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
