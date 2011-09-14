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

package uniqush

import (
    "testing"
    "fmt"
    "rand"
)

func TestLRUCache(t *testing.T) {
    strategy := NewLRUPeriodFlushStrategy(3, 100, 0)
    storage := NewInMemoryKeyValueStorage(10)
    flusher := &FakeFlusher{}

    cache := NewKeyValueCache(storage, strategy, flusher)
    fmt.Print("Start LRU cache test ...\t")

    for i := 0; i < 10; i++ {
        str := fmt.Sprint(i)
        cache.Show(str, str)
    }

    keys, _ := cache.Keys()
    if !same(convert2string([]int{7,9,8}), keys) {
        t.Errorf("should be [7 8 9], but %v", keys)
    }

    if v, _ := cache.Get("1"); v != nil {
        t.Errorf("%v should not be in cache", v)
    }

    cache.Get("7")
    cache.Show("1", "1")

    if v, _ := cache.Get("8"); v != nil {
        keys, _ := cache.Keys()
        t.Errorf("%v should not be in cache; cache content: %v", v, keys)
    }
    fmt.Print("OK\n")
}

func BenchmarkKeyValueCache(b *testing.B) {
    strategy := NewLRUPeriodFlushStrategy(200, 100, 0)
    storage := NewInMemoryKeyValueStorage(200)
    flusher := &FakeFlusher{}

    cache := NewKeyValueCache(storage, strategy, flusher)
    for i:= 0; i < 100000; i++ {
        key_int := rand.Int() % 1000
        key := fmt.Sprintf("%d", key_int)
        v, _ := cache.Get(key)
        if v == nil {
            cache.Show(key, key)
        }
    }
}
