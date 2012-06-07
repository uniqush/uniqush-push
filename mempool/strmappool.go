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

type StringMapPool struct {
	pools               []*ObjectMemoryPool
	maxNrStringMapPools int
	minMapLen           int
}

func newStringMap() interface{} {
	return make(map[string]string, 8)
}

func NewStringMapPool(n int, l int) *StringMapPool {
	ret := new(StringMapPool)
	if n <= 0 {
		n = 16
	}
	if l <= 0 {
		l = 2
	}
	ret.maxNrStringMapPools = n
	ret.minMapLen = l
	ret.pools = make([]*ObjectMemoryPool, ret.maxNrStringMapPools)

	for i := 0; i < n; i++ {
		ret.pools[i] = NewObjectMemoryPool(1024, newStringMap)
	}

	return ret
}

func (p *StringMapPool) Get(n int) map[string]string {
	if n <= 0 {
		return make(map[string]string)
	}
	if n < p.minMapLen ||
		n >= p.minMapLen+p.maxNrStringMapPools {
		return make(map[string]string, n)
	}
	mapif := p.pools[n-p.minMapLen].Get()
	ret := mapif.(map[string]string)
	return ret
}

func (p *StringMapPool) Recycle(m map[string]string) {
	n := len(m)
	if n < p.minMapLen ||
		n >= p.minMapLen+p.maxNrStringMapPools {
		return
	}
	p.pools[n-p.minMapLen].Recycle(m)
}
