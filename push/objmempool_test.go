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
	"testing"
)

func newMap() interface{} {
	ret := make(map[string]string, 100)
	return ret
}

func BenchmarkWithMemPool(b *testing.B) {
	pool := NewObjectMemoryPool(100, newMap)
	objlist := make([]interface{}, 100)
	top := 0

	userPattern := []int{1, 1, 0, 0,
		1, 0, 1, 0, 1, 1, 0, 0,
		1, 1, 1, 0, 1, 0, 1, 0, 0, 0}

	for i := 0; i < 100000; i++ {
		for _, p := range userPattern {
			switch p {
			case 1:
				objlist[top] = pool.Get()
				top++
			case 0:
				obj := objlist[top-1]
				pool.Recycle(obj)
				top--
			}
		}
	}
}

func BenchmarkNoMemPool(b *testing.B) {
	top := 0

	userPattern := []int{1, 1, 0, 0,
		1, 0, 1, 0, 1, 1, 0, 0,
		1, 1, 1, 0, 1, 0, 1, 0, 0, 0}

	for i := 0; i < 100000; i++ {
		objlist := make([]interface{}, 100)
		for _, p := range userPattern {
			switch p {
			case 1:
				objlist[top] = newMap()
				top++
			case 0:
				top--
			}
		}
	}
}
