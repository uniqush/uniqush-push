/*
 * Copyright 2011-2013 Nan Deng
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// package common contains common interfaces and data types for abstractions of apns request/response push protocols.
package common

import (
	"github.com/uniqush/uniqush-push/push"
)

type PushRequest struct {
	PSP       *push.PushServiceProvider
	Devtokens [][]byte
	Payload   []byte
	MaxMsgId  uint32
	Expiry    uint32

	// DPList is a list of delivery points of the same length as Devtokens. DPList[i].FixedData["dev_token"] == string(Devtokens[i])
	DPList  []*push.DeliveryPoint
	ErrChan chan<- push.PushError
	ResChan chan<- *APNSResult
}

// Used by binary protocol.
func (self *PushRequest) GetId(idx int) uint32 {
	if idx < 0 || idx >= len(self.Devtokens) {
		return 0
	}
	startId := self.MaxMsgId - uint32(len(self.Devtokens))
	return startId + uint32(idx)
}

type APNSResult struct {
	MsgId  uint32
	Status uint8
	Err    push.PushError
}
