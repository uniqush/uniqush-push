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

package libuniqushpush

import (
	"errors"
	"strings"
)

type nullPushFailureHandler struct{}

func (f *nullPushFailureHandler) OnPushFail(pst PushServiceType, id string, err error) {
}

type serviceTypeObjPool struct {
	pst     PushServiceType
	pspPool *ObjectMemoryPool
	dpPool  *ObjectMemoryPool
}

type PushServiceManager struct {
	serviceTypes map[string]*serviceTypeObjPool
	pfp          PushFailureHandler
}

var (
	pushServiceManager *PushServiceManager
)

func init() {
	//    pushServiceManager = newPushServiceManager()
}

/* This is a singleton */
func newPushServiceManager() *PushServiceManager {
	ret := new(PushServiceManager)
	ret.serviceTypes = make(map[string]*serviceTypeObjPool, 5)
	ret.pfp = &nullPushFailureHandler{}
	return ret
}

func GetPushServiceManager() *PushServiceManager {
	if pushServiceManager == nil {
		pushServiceManager = newPushServiceManager()
	}
	return pushServiceManager
}

func (m *PushServiceManager) SetAsyncFailureProcessor(pfp PushFailureHandler) {
	if pfp != nil {
		m.pfp = pfp
	}
}

func newPushServiceProvider() interface{} {
	return NewEmptyPushServiceProvider()
}

func newDeliveryPoint() interface{} {
	return NewEmptyDeliveryPoint()
}

func (m *PushServiceManager) RegisterPushServiceType(pt PushServiceType) error {
	name := pt.Name()
	pair := new(serviceTypeObjPool)
	pair.pspPool = NewObjectMemoryPool(1024, newPushServiceProvider)
	pair.dpPool = NewObjectMemoryPool(1024, newDeliveryPoint)
	pair.pst = pt
	m.serviceTypes[name] = pair
	pt.SetAsyncFailureHandler(m.pfp)
	return nil
}

func (m *PushServiceManager) BuildPushServiceProviderFromMap(kv map[string]string) (psp *PushServiceProvider, err error) {
	if ptname, ok := kv["pushservicetype"]; ok {
		if pair, ok := m.serviceTypes[ptname]; ok {
			pspif := pair.pspPool.Get()
			psp = pspif.(*PushServiceProvider)
			pst := pair.pst
			err = pst.BuildPushServiceProviderFromMap(kv, psp)
			psp.objPool = pair.pspPool
			if err != nil {
				psp.recycle()
				return nil, err
			}
			psp.pushServiceType = pst
			return
		}
		return nil, errors.New("Unknown Push Service Type")
	}
	return nil, errors.New("No Push Service Type Specified")
}

func (m *PushServiceManager) BuildPushServiceProviderFromBytes(value []byte) (psp *PushServiceProvider, err error) {
	s := string(value)
	parts := strings.SplitN(s, ":", 2)
	if len(parts) >= 2 {
		ptname := parts[0]
		if pair, ok := m.serviceTypes[ptname]; ok {
			// XXX potential secrurity risk:
			// all data in pspPool are not cleared.
			// It may easily get some data from the previous
			// struct if there are some fields which have not
			// been over written.
			pspif := pair.pspPool.Get()
			psp = pspif.(*PushServiceProvider)
			psp.objPool = pair.pspPool
			psp.pushServiceType = pair.pst
			err = psp.Unmarshal([]byte(parts[1]))
			if err != nil {
				psp.recycle()
				psp = nil
				return
			}
			return
		}
		return nil, errors.New("Unknown Push Service Type")
	}
	return nil, errors.New("No Push Service Type Specified")
}

func (m *PushServiceManager) BuildDeliveryPointFromMap(kv map[string]string) (dp *DeliveryPoint, err error) {
	if ptname, ok := kv["pushservicetype"]; ok {
		if pair, ok := m.serviceTypes[ptname]; ok {
			dpif := pair.dpPool.Get()
			dp = dpif.(*DeliveryPoint)
			dp.objPool = pair.dpPool
			pst := pair.pst
			err = pst.BuildDeliveryPointFromMap(kv, dp)
			if err != nil {
				dp.recycle()
				return nil, err
			}
			dp.pushServiceType = pst
			return
		}
		return nil, errors.New("Unknown Push Service Type")
	}
	return nil, errors.New("No Push Service Type Specified")
}

func (m *PushServiceManager) BuildDeliveryPointFromBytes(value []byte) (dp *DeliveryPoint, err error) {
	s := string(value)
	parts := strings.SplitN(s, ":", 2)
	if len(parts) >= 2 {
		ptname := parts[0]
		if pair, ok := m.serviceTypes[ptname]; ok {
			dpif := pair.dpPool.Get()
			dp = dpif.(*DeliveryPoint)
			dp.objPool = pair.dpPool
			pst := pair.pst
			dp.pushServiceType = pst
			err = dp.Unmarshal([]byte(parts[1]))
			if err != nil {
				dp.recycle()
				dp = nil
				return
			}
			return
		}
		return nil, errors.New("Unknown Push Service Type")
	}
	return nil, errors.New("No Push Service Type Specified")
}

func (m *PushServiceManager) Push(psp *PushServiceProvider, dp *DeliveryPoint, n *Notification) (id string, err error) {
	if psp.pushServiceType != nil {
		id, err = psp.pushServiceType.Push(psp, dp, n)
		return
	}
	return "", NewInvalidPushServiceProviderError(psp, errors.New("Unknown Service Type"))
}

func (m *PushServiceManager) Finalize() {
	for _, t := range m.serviceTypes {
		t.pst.Finalize()
	}
}
