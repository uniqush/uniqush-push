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
	"testing"

	"github.com/uniqush/uniqush-push/push"
	apns_mocks "github.com/uniqush/uniqush-push/srv/apns/http_api/mocks"
	"github.com/uniqush/uniqush-push/testutil"
)

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

const ServiceName = "pushdb_test_service"
const OtherServiceName = "pushdb_other_test_service"

func defaultMockPSPData() map[string]string {
	return map[string]string{
		"service":         ServiceName,
		"pushservicetype": "apns",
		"cert":            "fakecert.cert",
		"key":             "fakecert.key",
		"addr":            "gateway.push.apple.com:2195",
		"skipverify":      "true",
		"bundleid":        "",
	}
}

func connectDatabaseAndClearRedisData(t *testing.T) PushDatabase {
	t.Helper()
	client, err := connectDatabase()
	if err != nil {
		t.Fatalf("Error connecting to redis for test: %v", err)
	}
	client.(*pushDatabaseOpts).db.(*PushRedisDB).client.FlushDB()
	return client
}

func TestConnectAndClearCache(t *testing.T) {
	connectDatabaseAndClearRedisData(t)
}

func TestInsertAndGetPushServiceProviders(t *testing.T) {
	client := connectDatabaseAndClearRedisData(t)

	psm := initializePushServiceManagerForTest()

	pst := &apns_mocks.MockPushServiceType{}
	err := psm.RegisterPushServiceType(pst)
	if err != nil {
		panic(err)
	}

	pspData := defaultMockPSPData()
	psp, err := psm.BuildPushServiceProviderFromMap(pspData)
	if err != nil {
		t.Fatalf("Could not create a mock PSP: %v", err)
	}

	err = client.AddPushServiceProviderToService(ServiceName, psp)
	if err != nil {
		t.Fatalf("Could not add the mock PSP: %v", err)
	}
	storedServicesNames, err := client.(*pushDatabaseOpts).db.GetPushServiceProvidersByService(ServiceName)
	if err != nil {
		t.Fatalf("Failed to fetch stored_services_names from the db")
	}
	pspName := psp.Name()
	testutil.ExpectStringEquals(t, "apns:3c82df754225cdf5fc5379402952ebeeb2a9e6b0", pspName, "should have deterministic psp name")
	testutil.ExpectEquals(t, []string{pspName}, storedServicesNames, "should be able to fetch the new service from the db")
}

func TestInsertPushServiceProvidersDifferentServices(t *testing.T) {
	client := connectDatabaseAndClearRedisData(t)

	psm := initializePushServiceManagerForTest()

	pst := &apns_mocks.MockPushServiceType{}
	err := psm.RegisterPushServiceType(pst)
	if err != nil {
		t.Fatalf("apns PST already exists: %v", err)
	}

	// Test adding the first service type
	{
		pspData := defaultMockPSPData()
		psp, err := psm.BuildPushServiceProviderFromMap(pspData)
		if err != nil {
			t.Fatalf("Could not create a mock PSP: %v", err)
		}

		err = client.AddPushServiceProviderToService(ServiceName, psp)
		if err != nil {
			t.Fatalf("Could not add the mock PSP: %v", err)
		}
		storedServicesNames, err := client.(*pushDatabaseOpts).db.GetPushServiceProvidersByService(ServiceName)
		if err != nil {
			t.Fatalf("Failed to fetch stored_services_names from the db")
		}
		pspName := psp.Name()
		testutil.ExpectStringEquals(t, "apns:3c82df754225cdf5fc5379402952ebeeb2a9e6b0", pspName, "should have deterministic PSP name")
		testutil.ExpectEquals(t, []string{pspName}, storedServicesNames, "should be able to fetch the new service from the db")
	}

	{
		otherPSPData := defaultMockPSPData()
		otherPSPData["service"] = OtherServiceName
		otherPSPData["cert"] = "otherfakecert.cert"
		otherPSPData["key"] = "otherfakecert.key"
		otherPSP, err := psm.BuildPushServiceProviderFromMap(otherPSPData)
		if err != nil {
			t.Fatalf("Could not create a mock PSP: %v", err)
		}

		err = client.AddPushServiceProviderToService(OtherServiceName, otherPSP)
		if err != nil {
			t.Fatalf("Could not add the mock PSP: %v", err)
		}
		storedServicesNames, err := client.(*pushDatabaseOpts).db.GetPushServiceProvidersByService(OtherServiceName)
		if err != nil {
			t.Fatalf("Failed to fetch stored_services_names from the db")
		}
		otherPSPName := otherPSP.Name()
		testutil.ExpectStringEquals(t, "apns:ce1f6634c47c49f0b5d9c5d609e55544979d2172", otherPSPName, "should have deterministic PSP name")
		testutil.ExpectEquals(t, []string{otherPSPName}, storedServicesNames, "should be able to fetch the new service from the db")
	}
}

func initializePushServiceManagerForTest() *push.PushServiceManager {
	psm := push.GetPushServiceManager()
	psm.ClearAllPushServiceTypesForUnitTest()
	return psm
}

func TestInsertPushServiceProvidersConflictSameService(t *testing.T) {
	client := connectDatabaseAndClearRedisData(t)

	psm := initializePushServiceManagerForTest()

	pst := &apns_mocks.MockPushServiceType{}
	err := psm.RegisterPushServiceType(pst)
	testutil.ExpectStringEquals(t, "apns", pst.Name(), "sanity check")
	if err != nil {
		t.Fatalf("apns PST already exists: %v", err)
	}

	// Test adding the first service type
	var pspName string
	{
		pspData := defaultMockPSPData()
		psp, err := psm.BuildPushServiceProviderFromMap(pspData)
		if err != nil {
			t.Fatalf("Could not create a mock PSP: %v", err)
		}

		err = client.AddPushServiceProviderToService(ServiceName, psp)
		if err != nil {
			t.Fatalf("Could not add the mock PSP: %v", err)
		}
		storedServicesNames, err := client.(*pushDatabaseOpts).db.GetPushServiceProvidersByService(ServiceName)
		if err != nil {
			t.Fatalf("Failed to fetch stored_services_names from the db")
		}
		pspName = psp.Name()
		testutil.ExpectStringEquals(t, "apns:3c82df754225cdf5fc5379402952ebeeb2a9e6b0", pspName, "should have deterministic PSP name")

		testutil.ExpectEquals(t, []string{pspName}, storedServicesNames, "should be able to fetch the old service from the db")
	}

	{
		// We create a PSP that has different FixedData from the first PSP (for historical reasons, it's impossible to change certificate file path, but you can change the contents),
		otherPSPData := defaultMockPSPData()
		otherPSPData["cert"] = "otherfakecert.cert"
		otherPSPData["key"] = "otherfakecert.key"

		otherPSP, err := psm.BuildPushServiceProviderFromMap(otherPSPData)
		if err != nil {
			t.Fatalf("Could not create a mock PSP: %v", err)
		}

		err = client.AddPushServiceProviderToService(ServiceName, otherPSP)
		if err == nil {
			t.Fatalf("Expected an error adding the conflicting mock PSP")
		}
		testutil.ExpectStringEquals(
			t,
			"A different PSP for service pushdb_test_service already exists with different fixed data as push service type apns (It has a separate subscriber list). Please double check the list of current PSPs with the /psps API. Note that this error could be worked around by removing the old PSP, but that would delete subscriptions",
			err.Error(),
			"error message should describe the conflict",
		)
		otherPSPName := otherPSP.Name()
		testutil.ExpectStringEquals(t, "apns:c5de2508902f441a5252c60044745915df2f7368", otherPSPName, "should have deterministic PSP name")
		storedServicesNames, err := client.(*pushDatabaseOpts).db.GetPushServiceProvidersByService(ServiceName)
		if err != nil {
			t.Fatalf("Failed to fetch stored_services_names from the db")
		}
		testutil.ExpectEquals(t, []string{pspName}, storedServicesNames, "should be able to fetch the originally added service (not the new service) from the db")
	}
}
