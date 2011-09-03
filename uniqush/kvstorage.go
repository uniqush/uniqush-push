/*
 *  Uniqush by Nan Deng
 *  Copyright (C) 2010 Nan Deng
 *
 *  This software is free software; you can redistribute it and/or
 *  modify it under the terms of the GNU Lesser General Public
 *  License as published by the Free Software Foundation; either
 *  version 3.0 of the License, or (at your option) any later version.
 *
 *  This software is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 *  Lesser General Public License for more details.
 *
 *  You should have received a copy of the GNU Lesser General Public
 *  License along with this software; if not, write to the Free Software
 *  Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA  02111-1307  USA
 *
 *  Nan Deng <monnand@gmail.com>
 *
 */

package uniqush

import (
    "os"
)

// This is the interface to represent a key-value storage
type KeyValueStorage interface {
    // return value: old value and error
    Set(key string, v interface{}) (oldv interface{}, err os.Error)
    Remove(key string) (interface{}, os.Error)
    Get(key string) (interface{}, os.Error)
    Keys() ([]string, os.Error)
    Len() (int, os.Error)
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

func (s *InMemoryKeyValueStorage) Remove(key string) (interface{}, os.Error) {
    v, has := s.data[key]
    if !has {
        return nil, nil
    }
    s.data[key] = nil, false
    return v, nil
}

func (s *InMemoryKeyValueStorage) Set(key string,
                                      v interface{}) (oldv interface{}, err os.Error) {
    oldv, err = s.Get(key)
    s.data[key] = v, true
    return
}

func (s *InMemoryKeyValueStorage) Get(key string) (v interface{}, err os.Error) {
    var has bool
    v, has = s.data[key]
    if !has {
        return nil, nil
    }
    return v, nil
}

func (s *InMemoryKeyValueStorage) Len() (l int, err os.Error) {
    return len(s.data), nil
}

func (s *InMemoryKeyValueStorage) Keys() (keys []string, err os.Error) {
    keys = make([]string, 0, len(s.data))

    for k, _ := range s.data {
        keys = append(keys, k)
    }
    err = nil

    return
}

