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

package uniqush

import (
    "testing"
    "fmt"
    "os"
)

type testPushServiceType struct {
	name string
}

func newTestPushServiceType() *testPushServiceType {
	ret := new(testPushServiceType)
	ret.name = "testService"
	return ret
}

func (t *testPushServiceType) Name() string {
	return t.name
}

func (t *testPushServiceType) BuildPushServiceProviderFromMap(kv map[string]string) (*PushServiceProvider, os.Error) {
	psp := new(PushServiceProvider)
	psp.FixedData = make(map[string]string, len(kv))
	psp.VolatileData = make(map[string]string)
	for k, v := range kv {
		psp.FixedData[k] = v
	}
	return psp, nil
}

func (t *testPushServiceType) BuildPushServiceProviderFromString(str string) (*PushServiceProvider, os.Error) {
    return nil, nil
}

func (t *testPushServiceType) BuildDeliveryPointFromMap(kv map[string]string) (*DeliveryPoint, os.Error) {
    return nil, nil
}

func (t *testPushServiceType) BuildDeliveryPointFromString(str string) (*DeliveryPoint, os.Error) {
    return nil, nil
}

func (t *testPushServiceType) Push(psp *PushServiceProvider, dp *DeliveryPoint, n *Notification) (string, os.Error) {
    fmt.Print("Push!\n")
    return "", nil
}

func TestPushPeer(t *testing.T) {
    pp := new(PushPeer)
    tpst := newTestPushServiceType()
    pp.serviceTypeId = -1
    pp.pushServiceType = tpst
    pp.FixedData = make(map[string]string, 2)
    pp.FixedData["senderid"] = "uniqush.go@gmail.com"
    pp.FixedData["authtoken"] = "fasdf"

    pp.VolatileData = make(map[string]string, 1)
    pp.VolatileData["realauthtoken"] = "fsfad"

    fmt.Printf("Name: %s\n", pp.Name("myname"))

    str := pp.Marshal()
    fmt.Printf("Marshal: %s\n", string(str))

    psm := GetPushServiceManager()

    psm.RegisterPushServiceType(tpst)

    psp, err := psm.BuildPushServiceProviderFromBytes(str)
    if err != nil {
        t.Errorf("%v\n", err)
        return
    }
    fmt.Printf("Push Service: %s", psp.ToString())
    value := psp.Marshal()
    fmt.Printf("PSP Marshal: %s\n", string(value))
}

