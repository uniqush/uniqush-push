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
	"strings"
	"sync"
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

func newPushPeer() *PushPeer {
	ret := new(PushPeer)
	ret.InitPushPeer()
	return ret
}

func (p *PushPeer) InitPushPeer() {
	p.pushServiceType = nil
	p.VolatileData = make(map[string]string, 2)
	p.FixedData = make(map[string]string, 2)
}

func (p *PushPeer) copyPushPeer(dst *PushPeer) {
	dst.pushServiceType = p.pushServiceType
	dst.VolatileData = make(map[string]string, len(p.VolatileData))
	dst.FixedData = make(map[string]string, len(p.FixedData))
	for k, v := range p.VolatileData {
		dst.VolatileData[k] = v
	}
	for k, v := range p.FixedData {
		dst.FixedData[k] = v
	}
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

func (p *PushPeer) clear() {
	p.m.Lock()
	defer p.m.Unlock()
	for k := range p.FixedData {
		delete(p.FixedData, k)
	}
	for k := range p.VolatileData {
		delete(p.VolatileData, k)
	}
	p.name = ""
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
		return nil, fmt.Errorf("UnserializeSubscription() Bad data, no ':' to split on.")
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
		// TODO: Return any data useful to uniqush users from VolatileData (in a separate PR). It will be in f[1] if it exists.
		// (E.g. if version of the app, device id for a device token (e.g. for checking if two device tokens in different pushservicetypes belong to the same device))

		return sub, nil
	}

	return nil, fmt.Errorf("UnserializeSubscription() Invalid data")
}
