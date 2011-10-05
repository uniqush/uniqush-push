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
)

type nullPushFailureProcessor struct {}

func (f *nullPushFailureProcessor) OnPushFail(pst PushServiceType, id string, err os.Error) {
}

type PushServiceManager struct {
    serviceTypes map[string]PushServiceType
    pfp PushFailureProcessor
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
    ret.serviceTypes = make(map[string]PushServiceType, 5)
    ret.pfp = &nullPushFailureProcessor{}
    return ret
}


func GetPushServiceManager() *PushServiceManager {
    return pushServiceManager
}

func (m *PushServiceManager) SetAsyncFailureProcessor(pfp PushFailureProcessor) {
    if pfp != nil {
        m.pfp = pfp
    }
}

func (m *PushServiceManager) RegisterPushServiceType(pt PushServiceType) os.Error {
    name := pt.Name()
    m.serviceTypes[name] = pt
    pt.SetAsyncFailureProcessor(m.pfp)
    return nil
}

func (m *PushServiceManager) BuildPushServiceProviderFromMap(kv map[string]string) (psp *PushServiceProvider, err os.Error) {
    if ptname, ok := kv["pushservicetype"]; ok {
        if pst, ok := m.serviceTypes[ptname]; ok {
            psp, err = pst.BuildPushServiceProviderFromMap(kv)
            if err != nil {
                return nil, err
            }
            if psp == nil {
                return nil, os.NewError("Cannot Build Push Service Provider")
            }
            psp.pushServiceType = pst
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
        if pst, ok := m.serviceTypes[ptname]; ok {
            psp = NewEmptyPushServiceProvider()
            psp.pushServiceType = pst
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
        if pst, ok := m.serviceTypes[ptname]; ok {
            dp, err = pst.BuildDeliveryPointFromMap(kv)
            if err != nil {
                return nil, err
            }
            if dp == nil {
                return nil, os.NewError("Cannot Build Delivery Point")
            }
            dp.pushServiceType = pst
            return
        }
        return nil, os.NewError("Unknown Push Service Type")
    }
    return nil, os.NewError("No Push Service Type Specified")
}

func (m *PushServiceManager) BuildDeliveryPointFromBytes(value []byte) (dp *DeliveryPoint, err os.Error) {
    s := string(value)
    parts := strings.SplitN(s, ":", 2)
    if len(parts) >= 2 {
        ptname := parts[0]
        if pst, ok := m.serviceTypes[ptname]; ok {
            dp = NewEmptyDeliveryPoint()
            dp.pushServiceType = pst
            err = dp.Unmarshal([]byte(parts[1]))
            if err != nil {
                dp = nil
                return
            }
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

