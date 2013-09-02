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
	"errors"
	"fmt"
	"strings"
	"sync"
)

type serviceType struct {
	pst PushServiceType
}

type PushServiceManager struct {
	serviceTypes map[string]*serviceType
	errChan      chan<- error
}

var (
	pushServiceManager *PushServiceManager
)

/* This is a singleton */
func newPushServiceManager() *PushServiceManager {
	ret := new(PushServiceManager)
	ret.serviceTypes = make(map[string]*serviceType, 5)
	return ret
}

func GetPushServiceManager() *PushServiceManager {
	if pushServiceManager == nil {
		pushServiceManager = newPushServiceManager()
	}
	return pushServiceManager
}

func newPushServiceProvider() interface{} {
	return NewEmptyPushServiceProvider()
}

func newDeliveryPoint() interface{} {
	return NewEmptyDeliveryPoint()
}

func (m *PushServiceManager) RegisterPushServiceType(pt PushServiceType) error {
	name := pt.Name()
	pair := new(serviceType)
	if m.errChan != nil {
		pt.SetErrorReportChan(m.errChan)
	}
	pair.pst = pt
	m.serviceTypes[name] = pair
	return nil
}

func (m *PushServiceManager) BuildPushServiceProviderFromMap(kv map[string]string) (psp *PushServiceProvider, err error) {
	if ptname, ok := kv["pushservicetype"]; ok {
		if pair, ok := m.serviceTypes[ptname]; ok {
			// XXX We are not ready to use pool
			// pspif := pair.pspPool.Get()
			// psp = pspif.(*PushServiceProvider)
			// psp.objPool = pair.pspPool
			psp = NewEmptyPushServiceProvider()
			pst := pair.pst
			err = pst.BuildPushServiceProviderFromMap(kv, psp)
			if err != nil {
				return nil, err
			}
			if _, ok := psp.FixedData["service"]; !ok {
				err = fmt.Errorf("Bad Push Service Provider Implementation: service field is mandatory")
				psp = nil
				return
			}
			psp.pushServiceType = pst
			return
		}
		return nil, fmt.Errorf("Unknown Push Service Type: %v", ptname)
	}
	return nil, errors.New("No Push Service Type Specified")
}

func (m *PushServiceManager) BuildPushServiceProviderFromBytes(value []byte) (psp *PushServiceProvider, err error) {
	s := string(value)
	parts := strings.SplitN(s, ":", 2)
	if len(parts) >= 2 {
		ptname := parts[0]
		if pair, ok := m.serviceTypes[ptname]; ok {
			// XXX We are not ready to use pool
			// pspif := pair.pspPool.Get()
			// psp = pspif.(*PushServiceProvider)
			// psp.objPool = pair.pspPool

			psp = NewEmptyPushServiceProvider()
			psp.pushServiceType = pair.pst
			err = psp.Unmarshal([]byte(parts[1]))
			if err != nil {
				psp = nil
				return
			}
			if _, ok := psp.FixedData["service"]; !ok {
				err = fmt.Errorf("Bad Push Service Provider Implementation: service field is mandatory")
				psp = nil
				return
			}
			return
		}
		return nil, fmt.Errorf("Unknown Push Service Type: %v", ptname)
	}
	return nil, errors.New("No Push Service Type Specified")
}

func (m *PushServiceManager) BuildDeliveryPointFromMap(kv map[string]string) (dp *DeliveryPoint, err error) {
	if ptname, ok := kv["pushservicetype"]; ok {
		if pair, ok := m.serviceTypes[ptname]; ok {
			// dpif := pair.dpPool.Get()
			// dp = dpif.(*DeliveryPoint)
			// dp.objPool = pair.dpPool

			dp = NewEmptyDeliveryPoint()
			pst := pair.pst
			err = pst.BuildDeliveryPointFromMap(kv, dp)
			if err != nil {
				return nil, err
			}
			dp.pushServiceType = pst
			if _, ok := dp.FixedData["subscriber"]; !ok {
				err = fmt.Errorf("Bad Delivery Point Implementation: subscriber field is mandatory")
				dp = nil
				return
			}
			return
		}
		return nil, fmt.Errorf("Unknown Push Service Type: %v", ptname)
	}
	return nil, errors.New("No Push Service Type Specified")
}

func (m *PushServiceManager) BuildDeliveryPointFromBytes(value []byte) (dp *DeliveryPoint, err error) {
	s := string(value)
	parts := strings.SplitN(s, ":", 2)
	if len(parts) >= 2 {
		ptname := parts[0]
		if pair, ok := m.serviceTypes[ptname]; ok {
			// dpif := pair.dpPool.Get()
			// dp = dpif.(*DeliveryPoint)
			// dp.objPool = pair.dpPool

			dp = NewEmptyDeliveryPoint()
			pst := pair.pst
			dp.pushServiceType = pst
			err = dp.Unmarshal([]byte(parts[1]))
			if err != nil {
				dp = nil
				return
			}
			return
		}
		return nil, fmt.Errorf("Unknown Push Service Type: %v", ptname)
	}
	return nil, errors.New("No Push Service Type Specified")
}

func (m *PushServiceManager) Push(psp *PushServiceProvider, dpQueue <-chan *DeliveryPoint, resQueue chan<- *PushResult, notif *Notification) {
	wg := new(sync.WaitGroup)

	if psp.pushServiceType != nil {
		wg.Add(1)
		go func() {
			psp.pushServiceType.Push(psp, dpQueue, resQueue, notif)
			wg.Done()
		}()
	} else {
		r := new(PushResult)
		r.Provider = psp
		r.Destination = nil
		r.MsgId = ""
		r.Content = notif
		r.Err = fmt.Errorf("InvalidPushServiceProvider")
		resQueue <- r
	}

	wg.Wait()
}

func (m *PushServiceManager) SetErrorReportChan(errChan chan<- error) {
	m.errChan = errChan
	for _, t := range m.serviceTypes {
		t.pst.SetErrorReportChan(errChan)
	}
}

func (m *PushServiceManager) Finalize() {
	for _, t := range m.serviceTypes {
		t.pst.Finalize()
	}
}
