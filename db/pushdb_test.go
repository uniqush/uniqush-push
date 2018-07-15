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
	"reflect"
	"testing"

	"github.com/uniqush/uniqush-push/push"
	apns_mocks "github.com/uniqush/uniqush-push/srv/apns/http_api/mocks"
)

var dbconf *DatabaseConfig

func getTestDatabaseConfig() *DatabaseConfig {
	return &DatabaseConfig{
		Host:   "localhost",
		Port:   6379,
		Name:   "0",
		Engine: "redis",
	}
}

func connectDatabase() (db PushDatabase, err error) {
	return NewPushDatabaseWithoutCache(getTestDatabaseConfig())
}

func clearRedisData() {
	conf := getTestDatabaseConfig()
	client, err := newPushRedisDB(conf)
	if err != nil {
		panic(err)
	}

	client.client.FlushDb() // Flush the database which pushredisdb.go used.
}

func TestConnectAndDelete(t *testing.T) {
	_, err := connectDatabase()
	if err != nil {
		t.Fatalf("Error: %v", err)
	}
	clearRedisData()
}

func TestInsertAndGetPushServiceProviders(t *testing.T) {
	client, err := connectDatabase()
	if err != nil {
		t.Fatalf("Error: %v", err)
	}
	clearRedisData()

	psm := push.GetMockPushServiceManager()

	pst := &apns_mocks.MockPushServiceType{}
	err = psm.RegisterPushServiceType(pst)
	if err != nil {
		panic(err)
	}

	serviceName := "pushdb_test_service"
	psp_data := map[string]string{
		"service":         serviceName,
		"pushservicetype": pst.Name(), // "apns"
		"cert":            "fakecert.cert",
		"key":             "fakecert.key",
		"addr":            "gateway.push.apple.com:2195",
		"skipverify":      "true",
		"bundleid":        "fakebundleid",
	}
	psp, err := psm.BuildPushServiceProviderFromMap(psp_data)
	if err != nil {
		t.Fatalf("Could not create a mock PSP: %v", err)
	}

	err = client.AddPushServiceProviderToService(serviceName, psp)
	if err != nil {
		t.Fatalf("Could not add the mock PSP: %v", err)
	}
	client_impl, ok := client.(*pushDatabaseOpts)
	if !ok {
		t.Fatalf("Unexpected struct implementing push.PushDatabase")
	}
	stored_services_names, err := client_impl.db.GetPushServiceProvidersByService(serviceName)
	if err != nil {
		t.Fatalf("Failed to fetch stored_services_names from the db")
	}
	expected_stored_services_names := []string{psp.Name()}
	if !reflect.DeepEqual(expected_stored_services_names, stored_services_names) {
		t.Errorf("Expected %v to equal %v", expected_stored_services_names, stored_services_names)
	}
}
