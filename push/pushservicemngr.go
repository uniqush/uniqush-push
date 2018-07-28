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

	"github.com/uniqush/goconf/conf"
)

type serviceType struct {
	pst PushServiceType
}

type PushServiceManager struct {
	serviceTypes map[string]*serviceType
	errChan      chan<- Error
	configFile   *conf.ConfigFile
}

var (
	pushServiceManager *PushServiceManager
	once               sync.Once
)

/* This is a singleton */
func newPushServiceManager() *PushServiceManager {
	ret := new(PushServiceManager)
	ret.serviceTypes = make(map[string]*serviceType, 5)
	return ret
}

func GetPushServiceManager() *PushServiceManager {
	once.Do(func() {
		pushServiceManager = newPushServiceManager()
	})
	return pushServiceManager
}

func (m *PushServiceManager) ClearAllPushServiceTypesForUnitTest() {
	m.serviceTypes = make(map[string]*serviceType, 5)
}

func (m *PushServiceManager) RegisterPushServiceType(pt PushServiceType) error {
	name := pt.Name()
	pair := new(serviceType)
	if existing, ok := m.serviceTypes[name]; ok {
		return fmt.Errorf("Attempted to register handler for %q, but %#v already exists", name, existing)
	}
	if m.errChan != nil {
		pt.SetErrorReportChan(m.errChan)
	}
	if m.configFile != nil {
		pt.SetPushServiceConfig(NewPushServiceConfig(m.configFile, name))
	}
	pair.pst = pt
	m.serviceTypes[name] = pair
	return nil
}

func (m *PushServiceManager) BuildPushServiceProviderFromMap(kv map[string]string) (psp *PushServiceProvider, err error) {
	pushServiceType, ok := kv["pushservicetype"]
	if !ok {
		return nil, errors.New("No Push Service Type Specified")
	}
	pair, ok := m.serviceTypes[pushServiceType]
	if !ok {
		return nil, fmt.Errorf("BuildPushServiceProviderFromMap: Unknown Push Service Type: %v", pushServiceType)
	}
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

// BuildPushServiceProviderFromBytes will unserialize the passed in push service name+JSON (e.g. "apns:{...}") into a push service provider, or return an error.
func (m *PushServiceManager) BuildPushServiceProviderFromBytes(value []byte) (psp *PushServiceProvider, err error) {
	s := string(value)
	parts := strings.SplitN(s, ":", 2)
	if len(parts) < 2 {
		return nil, errors.New("BuildPushServiceProviderFromBytes: No Push Service Type Specified")
	}
	pushServiceType := parts[0]
	pair, ok := m.serviceTypes[pushServiceType]
	if !ok {
		return nil, fmt.Errorf("BuildPushServiceProviderFromBytes: Unknown Push Service Type: %v", pushServiceType)
	}

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

func (m *PushServiceManager) BuildDeliveryPointFromMap(kv map[string]string) (*DeliveryPoint, error) {
	pushServiceType, ok := kv["pushservicetype"]
	if !ok {
		return nil, errors.New("BuildDeliveryPointFromMap: No Push Service Type Specified")
	}
	pair, ok := m.serviceTypes[pushServiceType]
	if !ok {
		return nil, fmt.Errorf("BuildDeliveryPointFromMap: Unknown Push Service Type: %v", pushServiceType)
	}
	dp := NewEmptyDeliveryPoint()
	pst := pair.pst
	err := pst.BuildDeliveryPointFromMap(kv, dp)
	if err != nil {
		return nil, err
	}
	dp.pushServiceType = pst
	if _, ok := dp.FixedData["subscriber"]; !ok {
		return nil, fmt.Errorf("Bad Delivery Point Implementation: subscriber field is mandatory")
	}
	return dp, nil
}

// BuildDeliveryPointFromBytes will unserialize the passed in push service name+JSON (e.g. "apns:{...}") into a delivery point, or return an error.
func (m *PushServiceManager) BuildDeliveryPointFromBytes(value []byte) (*DeliveryPoint, error) {
	s := string(value)
	parts := strings.SplitN(s, ":", 2)
	if len(parts) < 2 {
		return nil, errors.New("BuildDeliveryPointFromBytes: No Push Service Type Specified")
	}
	pushServiceType := parts[0]
	pair, ok := m.serviceTypes[pushServiceType]
	if !ok {
		return nil, fmt.Errorf("BuildDeliveryPointFromBytes: Unknown Push Service Type: %v", pushServiceType)
	}

	dp := NewEmptyDeliveryPoint()
	pst := pair.pst
	dp.pushServiceType = pst
	err := dp.Unmarshal([]byte(parts[1]))
	if err != nil {
		return nil, err
	}
	return dp, nil
}

// Push will send a push to each delivery point received over the channel dpQueue, and send success/error responses over resQueue.
func (m *PushServiceManager) Push(psp *PushServiceProvider, dpQueue <-chan *DeliveryPoint, resQueue chan<- *Result, notif *Notification) {
	wg := new(sync.WaitGroup)

	if psp.pushServiceType != nil {
		wg.Add(1)
		go func() {
			psp.pushServiceType.Push(psp, dpQueue, resQueue, notif)
			wg.Done()
		}()
	} else {
		r := new(Result)
		r.Provider = psp
		r.Destination = nil
		r.MsgID = ""
		r.Content = notif
		r.Err = NewError("InvalidPushServiceProvider")
		resQueue <- r
	}

	wg.Wait()
}

// Preview will return the bytes of the serialized payload that will be sent to an external service for the given uniqush API parameters in 'notif' (adding placeholders where needed).
func (m *PushServiceManager) Preview(pushServiceType string, notif *Notification) ([]byte, Error) {
	if pst, ok := m.serviceTypes[pushServiceType]; ok && pst != nil {
		return pst.pst.Preview(notif)
	}
	return nil, NewErrorf("No push service type %q", pushServiceType)
}

func (m *PushServiceManager) SetErrorReportChan(errChan chan<- Error) {
	m.errChan = errChan
	for _, t := range m.serviceTypes {
		t.pst.SetErrorReportChan(errChan)
	}
}

func (m *PushServiceManager) SetConfigFile(c *conf.ConfigFile) {
	m.configFile = c
	for _, t := range m.serviceTypes {
		t.pst.SetPushServiceConfig(NewPushServiceConfig(m.configFile, t.pst.Name()))
	}
}

func (m *PushServiceManager) Finalize() {
	for _, t := range m.serviceTypes {
		t.pst.Finalize()
	}
}
