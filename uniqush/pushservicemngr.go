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
    "os"
    "strings"
    "fmt"
)

type PushServiceManager struct {
    serviceTypes []PushServiceType
    name2id map[string]int
}

var (
    pushServiceManager *PushServiceManager
)

func init() {
    pushServiceManager = newPushServiceManager()
}


/* This is a singleton */
func newPushServiceManager() *PushServiceManager {
    ret := new(PushServiceManager)
    ret.serviceTypes = make([]PushServiceType, 0, 5)
    ret.name2id = make(map[string]int, 5)
    return ret
}


func GetPushServiceManager() *PushServiceManager {
    return pushServiceManager
}

func (m *PushServiceManager) RegisterPushServiceType(pt PushServiceType) os.Error {
    name := pt.Name()
    fmt.Printf("Service Type: %s\n", name)
    if id, ok := m.name2id[name]; ok {
        m.serviceTypes[id] = pt
        return nil
    }
    id := len(m.serviceTypes)
    m.serviceTypes = append(m.serviceTypes, pt)
    m.name2id[name] = id
    return nil
}

func (m *PushServiceManager) BuildPushServiceProviderFromMap(kv map[string]string) (psp *PushServiceProvider, err os.Error) {
    if ptname, ok := kv["pushservicetype"]; ok {
        if id, ok := m.name2id[ptname]; ok {
            pst := m.serviceTypes[id]
            psp, err = pst.BuildPushServiceProviderFromMap(kv)
            psp.pushServiceType = pst
            psp.serviceTypeId = id
            return
        }
        return nil, os.NewError("Unknown Push Service Type")
    }
    return nil, os.NewError("No Push Service Type Specified")
}

func (m *PushServiceManager) BuildPushServiceProviderFromBytes(value []byte) (psp *PushServiceProvider, err os.Error) {
    s := string(value)
    parts := strings.SplitN(s, ":", 2)
    if len(parts) >= 2 {
        ptname := parts[0]
        if id, ok := m.name2id[ptname]; ok {
            pst := m.serviceTypes[id]
            psp = NewEmptyPushServiceProvider()
            psp.pushServiceType = pst
            psp.serviceTypeId = id
            err = psp.Unmarshal([]byte(parts[1]))
            if err != nil {
                psp = nil
                return
            }
            return
        }
        return nil, os.NewError("Unknown Push Service Type")
    }
    return nil, os.NewError("No Push Service Type Specified")
}

func (m *PushServiceManager) BuildDeliveryPointFromMap(kv map[string]string) (dp *DeliveryPoint, err os.Error) {
    if ptname, ok := kv["pushservicetype"]; ok {
        if id, ok := m.name2id[ptname]; ok {
            pst := m.serviceTypes[id]
            dp, err = pst.BuildDeliveryPointFromMap(kv)
            dp.serviceTypeId = id
            dp.pushServiceType = pst
            return
        }
        return nil, os.NewError("Unknown Push Service Type")
    }
    return nil, os.NewError("No Push Service Type Specified")
}

func (m *PushServiceManager) BuildDeliveryPointFromString(s string) (dp *DeliveryPoint, err os.Error) {
    parts := strings.SplitN(s, ":", 2)
    if len(parts) >= 2 {
        ptname := parts[0]
        if id, ok := m.name2id[ptname]; ok {
            dp.serviceTypeId = id
            pst := m.serviceTypes[id]
            dp, err = pst.BuildDeliveryPointFromString(parts[1])
            dp.serviceTypeId = id
            dp.pushServiceType = pst
            return
        }
        return nil, os.NewError("Unknown Push Service Type")
    }
    return nil, os.NewError("No Push Service Type Specified")
}

func (m* PushServiceManager) Push(psp *PushServiceProvider, dp *DeliveryPoint, n *Notification) (id string, err os.Error) {
    if psp.pushServiceType != nil {
        id, err = psp.pushServiceType.Push(psp, dp, n)
        return
    }
    return "", NewInvalidPushServiceProviderError(psp)
}

