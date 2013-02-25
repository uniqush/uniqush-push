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

type PushServiceProvider struct {
	PushPeer
	objPool *mempool.ObjectMemoryPool
}

func NewEmptyPushServiceProvider() *PushServiceProvider {
	psp := new(PushServiceProvider)
	psp.InitPushPeer()
	return psp
}

func (psp *PushServiceProvider) Copy() *PushServiceProvider {
	var ret *PushServiceProvider
	if psp.objPool == nil {
		ret = NewEmptyPushServiceProvider()
	} else {
		ret = psp.objPool.Get().(*PushServiceProvider)
	}
	psp.copyPushPeer(&ret.PushPeer)
	ret.objPool = psp.objPool
	return ret
}

func (psp *PushServiceProvider) Recycle() {
	if psp.objPool != nil {
		psp.clear()
		psp.objPool.Recycle(psp)
	}
}
