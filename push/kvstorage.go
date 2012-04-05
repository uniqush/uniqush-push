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

// This is the interface to represent a key-value storage
type KeyValueStorage interface {
	// return value: old value and error
	Set(key string, v interface{}) (oldv interface{}, err error)
	Remove(key string) (interface{}, error)
	Get(key string) (interface{}, error)
	Keys() ([]string, error)
	Len() (int, error)
}

/**** In Memory Storage ****/

type InMemoryKeyValueStorage struct {
	data map[string]interface{}
}

const (
	default_cache_size int = 100
)

func NewInMemoryKeyValueStorage(init_size int) KeyValueStorage {
	s := new(InMemoryKeyValueStorage)
	if init_size <= 0 {
		init_size = default_cache_size
	}
	s.data = make(map[string]interface{}, init_size)
	return s
}

func (s *InMemoryKeyValueStorage) Remove(key string) (interface{}, error) {
	v, has := s.data[key]
	if !has {
		return nil, nil
	}
	delete(s.data, key)
	return v, nil
}

func (s *InMemoryKeyValueStorage) Set(key string,
	v interface{}) (oldv interface{}, err error) {
	oldv, err = s.Get(key)
	s.data[key] = v
	return
}

func (s *InMemoryKeyValueStorage) Get(key string) (v interface{}, err error) {
	var has bool
	v, has = s.data[key]
	if !has {
		return nil, nil
	}
	return v, nil
}

func (s *InMemoryKeyValueStorage) Len() (l int, err error) {
	return len(s.data), nil
}

func (s *InMemoryKeyValueStorage) Keys() (keys []string, err error) {
	keys = make([]string, 0, len(s.data))

	for k, _ := range s.data {
		keys = append(keys, k)
	}
	err = nil

	return
}
