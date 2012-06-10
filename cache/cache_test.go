/*
 * Copyright 2012 Nan Deng
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

package cache

import (
	"testing"
	"time"
)

func TestInsertValues(t *testing.T) {
	kv := make(map[string]string)
	kv["key1"] = "1"
	kv["key2"] = "2"
	kv["key3"] = "3"
	kv["key4"] = "4"

	c := New(5, 5, 0*time.Second, nil)

	for k, v := range kv {
		c.Set(k, v)
	}

	for k, v := range kv {
		value := c.Get(k).(string)
		if value != v {
			t.Errorf("should be %v on key %v. Got %v",
				v, k, value)
		}
	}

	value := c.Get("notexist")
	if value != nil {
		t.Errorf("Got nonexist")
	}
}

func TestExceedCapacityValues(t *testing.T) {
	kv := make(map[string]string)
	kv["key1"] = "1"
	kv["key2"] = "2"
	kv["key3"] = "3"
	kv["key4"] = "4"

	keys := []string{"key1", "key2", "key3", "key4"}

	c := New(3, 5, 0*time.Second, nil)

	for _, k := range keys {
		c.Set(k, kv[k])
	}

	value := c.Get("key1")
	if value != nil {
		c.debug()
		t.Errorf("key1 inserted: %v", value)
	}
}

func TestUpdateValue(t *testing.T) {
	kv := make(map[string]string)
	kv["key1"] = "1"
	kv["key2"] = "2"
	kv["key3"] = "3"
	kv["key4"] = "4"

	c := New(5, 5, 0*time.Second, nil)

	for k, v := range kv {
		c.Set(k, v)
	}

	c.Set("key1", "111")
	if c.Get("key1").(string) != "111" {
		c.debug()
		t.Errorf("cannot update")
	}
}

func TestDeleteValue(t *testing.T) {
	kv := make(map[string]string)
	kv["key1"] = "1"
	kv["key2"] = "2"
	kv["key3"] = "3"
	kv["key4"] = "4"

	c := New(5, 5, 0*time.Second, nil)

	for k, v := range kv {
		c.Set(k, v)
	}

	c.Delete("key1")
	if c.Get("key1") != nil {
		c.debug()
		t.Errorf("cannot delete")
	}
}

type memFlusher struct {
	data map[string]interface{}
}

func newMemFlusher() *memFlusher {
	ret := new(memFlusher)
	ret.data = make(map[string]interface{})
	return ret
}

func (f *memFlusher) Add(key string, value interface{}) {
	f.data[key] = value
}

func (f *memFlusher) Remove(key string) {
	delete(f.data, key)
}

func TestFlushOnDirty(t *testing.T) {
	kv := make(map[string]string)
	kv["key1"] = "1"
	kv["key2"] = "2"
	kv["key3"] = "3"
	kv["key4"] = "4"

	f := newMemFlusher()

	c := New(5, 3, 0*time.Second, f)
	keys := []string{"key1", "key2", "key3", "key4"}

	for _, k := range keys {
		c.Set(k, kv[k])
	}

	for _, k := range keys[:3] {
		if _, ok := f.data[k]; !ok {
			t.Errorf("%v does not exist", k)
		}
	}
}

func TestFlushOnTimeOut(t *testing.T) {
	kv := make(map[string]string)
	kv["key1"] = "1"
	kv["key2"] = "2"
	kv["key3"] = "3"
	kv["key4"] = "4"

	f := newMemFlusher()
	duration := 3 * time.Second

	c := New(5, 5, duration, f)
	keys := []string{"key1", "key2", "key3", "key4"}

	for _, k := range keys {
		c.Set(k, kv[k])
	}

	time.Sleep(duration + 1*time.Second)

	for _, k := range keys {
		if _, ok := f.data[k]; !ok {
			t.Errorf("%v does not exist", k)
		}
	}
}

func TestEvictValue(t *testing.T) {
	kv := make(map[string]string)
	kv["key1"] = "1"
	kv["key2"] = "2"
	kv["key3"] = "3"
	kv["key4"] = "4"

	f := newMemFlusher()

	c := New(4, 3, 0*time.Second, f)
	keys := []string{"key1", "key2", "key3", "key4"}

	for _, k := range keys {
		c.Set(k, kv[k])
	}

	c.Get("key1")
	c.Set("key5", "xxx")
	if v := c.Get("key1"); v == nil {
		c.debug()
		t.Errorf("key1 does not exist")
	}

}

func TestAlwaysInMemoryCache(t *testing.T) {
	kv := make(map[string]string)
	kv["key1"] = "1"
	kv["key2"] = "2"
	kv["key3"] = "3"
	kv["key4"] = "4"

	c := New(-1, -1, 0*time.Second, nil)

	for k, v := range kv {
		c.Set(k, v)
	}

	for k, v := range kv {
		value := c.Get(k).(string)
		if value != v {
			t.Errorf("should be %v on key %v. Got %v",
				v, k, value)
		}
	}

	value := c.Get("notexist")
	if value != nil {
		t.Errorf("Got nonexist")
	}
}
