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

package pushsys

import (
	"github.com/monnand/uniqush/mempool"
)

type Notification struct {
	/* We don't want too complicated
	   Message string
	   Badge int
	   Image string
	   Sound string

	   // Defined but not used now
	   IsLoc bool
	   Delay bool
	*/
	Data map[string]string
	pool *notificationPool
}

type notificationPool struct {
	pools      []*mempool.ObjectMemoryPool
	maxNrPools int
	minMapLen  int
}

func newNotificationPool(n, l int) *notificationPool {
	ret := new(notificationPool)
	if n <= 0 {
		n = 16
	}
	if l <= 0 {
		l = 2
	}
	ret.maxNrPools = n
	ret.minMapLen = l
	ret.pools = make([]*mempool.ObjectMemoryPool, ret.maxNrPools)

	for i := 0; i < n; i++ {
		ret.pools[i] = mempool.NewObjectMemoryPool(1024, newEmptyNotif)
	}

	return ret
}

func (p *notificationPool) get(n int) *Notification {
	if n <= 0 {
		return NewEmptyNotification()
	}
	if n < p.minMapLen ||
		n >= p.minMapLen+p.maxNrPools {
		return NewEmptyNotification()
	}
	mapif := p.pools[n-p.minMapLen].Get()
	ret := mapif.(*Notification)
	ret.pool = p
	return ret
}

func (p *notificationPool) recycle(m *Notification) {
	n := len(m.Data)
	if n < p.minMapLen ||
		n >= p.minMapLen+p.maxNrPools {
		return
	}
	p.pools[n-p.minMapLen].Recycle(m)
}

func newEmptyNotif() interface{} {
	n := new(Notification)
	n.Data = make(map[string]string, 10)
	return n
}

func NewEmptyNotification() *Notification {
	n := new(Notification)
	n.Data = make(map[string]string, 10)
	return n
}

func (n *Notification) IsEmpty() bool {
	if len(n.Data) == 0 {
		return true
	}
	return false
}

func (n *Notification) Recycle() {
	if n.pool == nil {
		return
	}
	n.pool.recycle(n)
}
