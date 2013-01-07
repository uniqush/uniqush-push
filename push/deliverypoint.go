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
	"github.com/uniqush/mempool"
)

type DeliveryPoint struct {
	PushPeer
	objPool *mempool.ObjectMemoryPool
}

func NewEmptyDeliveryPoint() *DeliveryPoint {
	ret := new(DeliveryPoint)
	ret.InitPushPeer()
	return ret
}

func (dp *DeliveryPoint) Copy() *DeliveryPoint {
	var ret *DeliveryPoint
	if dp.objPool == nil {
		ret = NewEmptyDeliveryPoint()
	} else {
		ret = dp.objPool.Get().(*DeliveryPoint)
	}
	dp.copyPushPeer(&ret.PushPeer)
	ret.objPool = dp.objPool
	return ret
}

func (dp *DeliveryPoint) Recycle() {
	if dp.objPool != nil {
		dp.objPool.Recycle(dp)
	}
}
