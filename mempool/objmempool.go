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

package mempool

type ObjectMemoryPool struct {
	objs  []interface{}
	maxnr int
	f     func() interface{}
}

func NewObjectMemoryPool(n int, f func() interface{}) *ObjectMemoryPool {
	pool := new(ObjectMemoryPool)
	if n <= 1 {
		pool.maxnr = 0x0FFFFFFF
		pool.objs = make([]interface{}, 0, 1024)
	} else {
		pool.maxnr = n
		pool.objs = make([]interface{}, 0, n)
	}
	pool.f = f
	return pool
}

func (p *ObjectMemoryPool) Get() interface{} {
	if len(p.objs) == 0 {
		return p.f()
	}
	ret := p.objs[len(p.objs)-1]
	p.objs = p.objs[:len(p.objs)-1]
	return ret
}

func (p *ObjectMemoryPool) Recycle(o interface{}) {
	if len(p.objs) >= p.maxnr {
		return
	}
	p.objs = append(p.objs, o)
}
