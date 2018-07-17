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
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Fields in FixedData, found in each DeliveryPoint.
const (
	SERVICE    = "service"
	SUBSCRIBER = "subscriber"
	// Fields which may or may not be in VolatileData of any DeliveryPoint
	// DEVICE_ID is used by **clients of** uniqush to uniquely identify the device which a given DeliveryPoint corresponds to. This can be real or fictional, and does not affect uniqush's behavior in any way.
	// It is useful when you have multiple apps, and need to know if they're on the same device.
	DEVICE_ID     = "devid"
	OLD_DEVICE_ID = "old_devid" // Hack specific to our company. We changed our device ids.
	// SUBSCRIBE_DATE is optional, can be used by clients for seeing the most recent date when a DP was added. This is a unix timestamp.
	SUBSCRIBE_DATE = "subscribe_date"
	// APP_VERSION is optional. It should be a dot separated string of numbers, representing the version of the iOS/android/amazon app, at the last time a given subscription was added. It may be used for deciding whether to push.
	// TODO: Allow clients to specify version ranges?
	APP_VERSION = "app_version"
	LOCALE      = "locale"
)

// PushPeer implements common functionality for pushes. Other structs in this module include this struct.
type PushPeer struct {
	m               sync.Mutex // Enforces that there are no data races on Name() in multi push.
	name            string
	pushServiceType PushServiceType
	// VolatileData contains data about a push peer that may be changed by clients.
	VolatileData map[string]string
	// FixedData contains unchanging data about a push peer. FixedData is used to generate the identifier for this PushPeer.
	FixedData map[string]string
}

// PushServiceName is the name push service type this object uses (apns, gcm, etc.)
func (p *PushPeer) PushServiceName() string {
	return p.pushServiceType.Name()
}

func (p *PushPeer) String() string {
	ret := "push service type: "
	ret += p.pushServiceType.Name()
	ret += "\nFixed Data:\n"

	for k, v := range p.FixedData {
		ret += k + ": " + v + "\n"
	}
	ret += "\n"
	return ret
}

// InitPushPeer initializes fields of the base struct PushPeer
func (p *PushPeer) InitPushPeer() {
	p.pushServiceType = nil
	p.VolatileData = make(map[string]string, 2)
	p.FixedData = make(map[string]string, 2)
}

func (p *PushPeer) Name() string {
	p.m.Lock()
	defer p.m.Unlock()
	if p.name != "" {
		return p.name
	}
	hash := sha1.New()
	if p.FixedData == nil {
		return ""
	}
	b, _ := json.Marshal(p.FixedData)
	hash.Write(b)
	h := make([]byte, 0, 64)
	p.name = fmt.Sprintf("%s:%x",
		p.pushServiceType.Name(),
		hash.Sum(h))
	return p.name
}

func (p *PushPeer) Marshal() []byte {
	if p.pushServiceType == nil {
		return nil
	}
	s := make([]map[string]string, 2)
	s[0] = p.FixedData
	s[1] = p.VolatileData
	b, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	str := p.pushServiceType.Name() + ":" + string(b)
	return []byte(str)
}

func (p *PushPeer) Unmarshal(value []byte) error {
	//var f interface{}

	var f []map[string]string

	err := json.Unmarshal(value, &f)
	if err != nil {
		//fmt.Printf("Error Unmarshal: %v\n", err)
		return err
	}

	if len(f) < 2 {
		return errors.New("Invalid Push Peer")
	}

	p.FixedData = f[0]
	p.VolatileData = f[1]

	return nil
}

// DeliveryPoint contains information about a user+app+device. It contains the information about that combination needed to send pushes.
type DeliveryPoint struct {
	PushPeer
}

func NewEmptyDeliveryPoint() *DeliveryPoint {
	ret := new(DeliveryPoint)
	ret.InitPushPeer()
	return ret
}

// AddCommonData adds both mandatory and optional data, which could be present in a delivery point for any push service type. On failure, returns an error.
func (dp *DeliveryPoint) AddCommonData(kv map[string]string) error {
	if service, ok := kv["service"]; ok && len(service) > 0 {
		dp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}
	if sub, ok := kv["subscriber"]; ok && len(sub) > 0 {
		dp.FixedData["subscriber"] = sub
	} else {
		return errors.New("NoSubscriber")
	}

	if subscribeDate, ok := kv[SUBSCRIBE_DATE]; ok && len(subscribeDate) > 0 {
		if _, err := strconv.ParseFloat(subscribeDate, 64); err != nil {
			return fmt.Errorf("Invalid subscribe_date %q, expected a unix timestamp: %v", subscribeDate, err)
		}
		dp.VolatileData[SUBSCRIBE_DATE] = subscribeDate
	}
	// Volatile fields with no validation
	for _, field := range []string{DEVICE_ID, OLD_DEVICE_ID, APP_VERSION, LOCALE} {
		if value, ok := kv[field]; ok && len(value) > 0 {
			dp.VolatileData[field] = value
		}
	}
	return nil
}

// PushServiceProvider contains the data needed to send pushes to an external push notifications service provider (certificates, pushservicetype, server address, etc.).
type PushServiceProvider struct {
	PushPeer
}

func NewEmptyPushServiceProvider() *PushServiceProvider {
	psp := new(PushServiceProvider)
	psp.InitPushPeer()
	return psp
}

// IsSamePSP returns whether or not the name, FixedData, and VolatileData of two PSPs are identical.
func IsSamePSP(a *PushServiceProvider, b *PushServiceProvider) bool {
	if a.Name() != b.Name() {
		return false
	}
	if len(a.VolatileData) != len(b.VolatileData) {
		return false
	}
	for k, v := range a.VolatileData {
		if b.VolatileData[k] != v {
			return false
		}
	}
	return true
}

// UnserializeSubscription unserializes the data (of the form "<pushservicetype>:{...}") about a user's subscription, to be returned to uniqush's clients.
func UnserializeSubscription(data []byte) (map[string]string, error) {
	parts := strings.SplitN(string(data), ":", 2)

	if len(parts) != 2 {
		return nil, fmt.Errorf("UnserializeSubscription() Bad data, no ':' to split on")
	}

	var f []map[string]string
	err := json.Unmarshal([]byte(parts[1]), &f)
	if err != nil {
		return nil, err
	}

	if len(f) > 0 {
		sub := f[0]
		sub["pushservicetype"] = parts[0]
		delete(sub, "subscriber")
		if len(f) > 1 {
			volatileData := f[1]
			if devid, ok := volatileData[DEVICE_ID]; ok && len(devid) > 0 {
				sub[DEVICE_ID] = devid
			}
			if devid, ok := volatileData[OLD_DEVICE_ID]; ok && len(devid) > 0 {
				sub[OLD_DEVICE_ID] = devid
			}
			if subscribeDate, ok := volatileData[SUBSCRIBE_DATE]; ok && len(subscribeDate) > 0 {
				sub[SUBSCRIBE_DATE] = subscribeDate
			}
			if appVersion, ok := volatileData[APP_VERSION]; ok && len(appVersion) > 0 {
				sub[APP_VERSION] = appVersion
			}
		}

		return sub, nil
	}

	return nil, fmt.Errorf("UnserializeSubscription() Invalid data")
}
