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

package push

import (
	"fmt"
	"testing"
)

// testPushServiceType implements the PushServiceType interface and must
// implement the functions below even when they do nothing.
type testPushServiceType struct {
	name string
}

var _ PushServiceType = &testPushServiceType{}

func newTestPushServiceType() *testPushServiceType {
	ret := new(testPushServiceType)
	ret.name = "testService"
	return ret
}

func (pst *testPushServiceType) SetErrorReportChan(errChan chan<- Error) {
}

func (pst *testPushServiceType) Name() string {
	return pst.name
}

func (pst *testPushServiceType) Finalize() {
}

func (pst *testPushServiceType) BuildPushServiceProviderFromMap(kv map[string]string, psp *PushServiceProvider) error {
	for k, v := range kv {
		psp.FixedData[k] = v
	}
	return nil
}

func (pst *testPushServiceType) BuildDeliveryPointFromMap(kv map[string]string, dp *DeliveryPoint) error {
	for k, v := range kv {
		dp.FixedData[k] = v
	}
	return nil
}

func (pst *testPushServiceType) Push(*PushServiceProvider, <-chan *DeliveryPoint, chan<- *Result, *Notification) {
	fmt.Println("Push!")
}

func (pst *testPushServiceType) Preview(*Notification) ([]byte, Error) {
	fmt.Println("Preview!")
	return []byte("{}"), nil
}

func (pst *testPushServiceType) SetPushServiceConfig(c *PushServiceConfig) {
}

func TestPushPeer(t *testing.T) {
	pp := new(PushPeer)
	tpst := newTestPushServiceType()
	pp.pushServiceType = tpst
	pp.FixedData = map[string]string{
		"senderid":  "uniqush.go@gmail.com",
		"authtoken": "fasdf",
		"service":   "testServiceName",
	}

	pp.VolatileData = map[string]string{
		"realauthtoken": "fsfad",
	}

	t.Logf("Name: %s\n", pp.Name())

	str := pp.Marshal()
	t.Logf("Marshal: %s\n", string(str))

	psm := GetPushServiceManager()

	psm.RegisterPushServiceType(tpst)

	psp, err := psm.BuildPushServiceProviderFromBytes(str)
	if err != nil {
		t.Errorf("BuildPushServiceProviderFromBytes failed: %v\n", err)
		return
	}
	t.Logf("Push Service: %s\n", psp.String())
	t.Logf("PSP Name: %v\n", psp.Name())
	t.Logf("PSP Marshal: %s\n", string(psp.Marshal()))
}

func TestCompatability(t *testing.T) {
	tpst := newTestPushServiceType()
	psm := GetPushServiceManager()
	psm.RegisterPushServiceType(tpst)

	pspm := map[string]string{
		"pushservicetype": tpst.Name(),
		"senderid":        "uniqush.go@gmail.com",
		"authtoken":       "fsafds",
		"service":         "testServiceName",
	}

	dpm := map[string]string{
		"pushservicetype": "testService",
		"regid":           "fdsafas",
		"subscriber":      "subscriber.1234",
	}

	psp, err := psm.BuildPushServiceProviderFromMap(pspm)
	if err != nil {
		t.Fatalf("BuildPushServiceProviderFromMap failed: %v", err)
	}
	dp, err := psm.BuildDeliveryPointFromMap(dpm)
	if err != nil {
		t.Fatalf("BuildDeliveryPointFromMap failed: %v", err)
	}

	serviceNamePSP := psp.PushServiceName()
	serviceNameDP := dp.PushServiceName()
	if serviceNamePSP != serviceNameDP {
		t.Errorf("Should be compatible, but %q != %q\n", serviceNamePSP, serviceNameDP)
	}
}
