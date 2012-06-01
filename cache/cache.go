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
	"time"
	"sync"
	"container/list"
)

type Flusher interface {
	Add(key string, value interface{})
	Remove(key string)
}

type dirtyElement struct {
	modified bool
	removed bool
	key string
	value interface{}
}

type Cache struct {
	mu sync.Mutex
	flushPeriod time.Duration
	data map[string]*list.Element
	dirtyList *list.List
	list *list.List
	capacity uint32
	size uint32
	flusher Flusher
	maxNrDirty int
}

func (c *Cache) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flusher == nil {
		return
	}
	for e := c.dirtyList.Front(); e != nil; e = e.Next() {
		if de, ok := e.Value.(dirtyElement); ok {
			if de.removed {
				c.flusher.Remove(de.key)
			} else if de.modified {
				c.flusher.Add(de.key, de.value)
			}
		}
	}
	c.dirtyList = list.New()
}

func (c *Cache) checkAndFlush() {
	c.mu.Lock()
	if c.maxNrDirty > 0 && c.dirtyList.Len() >= c.maxNrDirty {
		c.mu.Unlock()
		c.flush()
	} else {
		c.mu.Unlock()
	}
}

func New(capacity uint32, maxNrDirty int, flushPeriod time.Duration, flusher Flusher) {
	cache := new(Cache)

	cache.flushPeriod = flushPeriod
	cache.capacity = capacity
	cache.data = make(map[string]*list.Element, cache.capacity)
	cache.list = list.New()
	cache.dirtyList = list.New()
	cache.flusher = flusher
	cache.maxNrDirty = maxNrDirty

	if flushPeriod.Seconds() > 0.9 {
		go func () {
			time.Sleep(flushPeriod)
			cache.flush()
		}()
	}
}

func (c *Cache) Get(key string) interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.data[key]; ok {
		value := elem.Value
		c.list.MoveToFront(elem)
		return value
	}
	return nil
}

func (c *Cache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.checkAndFlush()
	defer c.mu.Unlock()

	if len(c.data) > int(c.capacity) {
		return
	}
	de := &dirtyElement {
		modified: true,
		removed: false,
		key: key,
		value: value,
	}
	if e, ok := c.data[key]; ok {
		e.Value = value
		c.list.MoveToFront(e)
		c.dirtyList.PushBack(de)
	} else {
		elem := c.list.PushFront(value)
		c.data[key] = elem
		c.dirtyList.PushBack(de)
	}
	return
}

func (c *Cache) Delete(key string) interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	de := &dirtyElement {
		modified: false,
		removed: true,
		key: key,
		value: nil,
	}
	c.dirtyList.PushBack(de)
	if elem, ok := c.data[key]; ok {
		delete(c.data, key)
		return elem.Value
	}
	return nil
}

