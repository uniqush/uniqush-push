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
	//"testing"
	"fmt"
	"sync/atomic"
)

type fakePST struct {
	nextid uint32
}

func (self *fakePST) Name() string {
	return "fakePST"
}

func (self *fakePST) BuildPushServiceProviderFromMap(data map[string]string, psp *PushServiceProvider) error {
	if name, ok := data["name"]; !ok {
		return fmt.Errorf("Malformed data to construct psp: No name")
	} else {
		psp.FixedData["name"] = name
	}
	return nil
}

func (self *fakePST) BuildDeliveryPointFromMap(data map[string]string, dp *DeliveryPoint) error {
	if name, ok := data["name"]; !ok {
		return fmt.Errorf("Malformed data to construct dp: No name")
	} else {
		dp.FixedData["name"] = name
	}
	return nil
}

func (self *fakePST) Finalize() {
}

func (self *fakePST) Push(psp *PushServiceProvider, dpChan <-chan *DeliveryPoint, resChan chan<- *PushResult, notif *Notification) {
	for dp := range dpChan {
		mid := atomic.AddUint32(&(self.nextid), 1)
		msgid := fmt.Sprintf("%v", mid)
		res := &PushResult{Provider: psp, Destination: dp, Content: notif, MsgId: msgid}
		resChan <- res
	}
}
