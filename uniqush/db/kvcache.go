package db

// This file defined interfaces for key-value caches
// and some simple implementations

import (
    "os"
    "sync"
    "time"
)

// This should be some database-related struct
// to flush dirty data into database
type KeyValueFlusher interface {
    Flush(key string, value *interface{}) os.Error
}

// This interface defines bahaviors of a cache
// Like: if we should store this data; if we need to remove some data
type KeyValueCacheStrategy interface {
    // This should be called on cache hit.
    Hit(key string)

    // This should be called by cache when a key is inserted
    Added(key string)

    // This should be called when a key is removed
    Removed(key string)

    // This should be called on cache miss.
    Miss(key string)

    // This should return all keys which need to be removed
    GetObsoleted() []string

    // This should tell the cache if add the key into cache
    ShouldAdd(key string) bool

    // Nothing fancy
    ShouldFlush() bool

    Flushed()
}

// This is the interface to represent a key-value storage
type KeyValueStorage interface {
    Add(key string, v *interface{}) os.Error
    Remove(key string) os.Error
    Get(key string) (*interface{}, os.Error)
    Len() (int, os.Error)
}

/**** In Memory Storage ****/

type InMemoryKeyValueStorage struct {
    data map[string]*interface{}
}

const (
    default_cache_size int = 100
)

func NewInMemoryKeyValueStorage(size int) *InMemoryKeyValueStorage {
    s := new(InMemoryKeyValueStorage)
    if size <= 0 {
        size = default_cache_size
    }
    s.data = make(map[string]*interface{}, size)
    return s
}

func (s *InMemoryKeyValueStorage) Remove(key string) os.Error {
    s.data[key] = nil, false
    return nil
}

func (s *InMemoryKeyValueStorage) Get(key string) (v *interface{}, err os.Error) {
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

/******* Key Value Cache **************/

type kvdata struct {
    key string
    value *interface{}
}

type KeyValueCache struct {
    storage KeyValueStorage
    strategy KeyValueCacheStrategy
    flusher KeyValueFlusher
    dirty_list []kvdata
    rwlock sync.RWMutex
}

const (
    default_dirty_list_size = 50
)

func NewKeyValueCache(storage KeyValueStorage,
                      strategy KeyValueCacheStrategy,
                      flusher KeyValueFlusher) *KeyValueCache {
    c := new(KeyValueCache)
    c.storage = storage
    c.strategy = strategy
    c.dirty_list = make([]kvdata, 0, default_dirty_list_size)
    c.flusher = flusher
    return c
}

func (c *KeyValueCache) remove() os.Error {
    c.rwlock.Lock()
    defer c.rwlock.Unlock()

    rmlist := c.strategy.GetObsoleted()
    var err os.Error
    for _, k := range rmlist {
        err = c.storage.Remove(k)
        c.strategy.Removed(k)
        if err != nil {
            return err
        }
    }
    return nil
}

func (c *KeyValueCache) flush() os.Error {
    c.rwlock.Lock()
    defer c.rwlock.Unlock()

    if need_flush := c.strategy.ShouldFlush(); need_flush {
        for _, d := range c.dirty_list {
            err := c.flusher.Flush(d.key, d.value)
            if err != nil {
                return err
            }
        }
        c.strategy.Flushed()
    }
    return nil
}

// The caller could Show a key value pair to a cache,
// and let the cache decide if it want to add this pair into the cache.
// A cache make this decision based on its strategy
func (c *KeyValueCache) Show(key string, v *interface{}) os.Error {
    var err os.Error
    if should_add := c.strategy.ShouldAdd(key); should_add {

        c.rwlock.Lock()
        c.strategy.Added(key)
        err := c.storage.Add(key, v)
        c.dirty_list = append(c.dirty_list, kvdata{key, v})
        c.rwlock.Unlock()

        if err != nil {
            return err
        }

    }
    err = c.remove()
    err = c.flush()
    if err != nil {
        return err
    }
    return nil
}

func (c *KeyValueCache) Get(key string) (v *interface{}, err os.Error) {
    c.rwlock.RLock()
    v, err = c.storage.Get(key)
    if err != nil {
        v = nil
        c.rwlock.RUnlock()
        return
    }

    // Cache miss
    if v == nil {
        c.strategy.Miss(key)
    }

    // Cache hit
    c.strategy.Hit(key)
    c.rwlock.RUnlock()

    err = c.remove()
    err = c.flush()
    return
}

/*************** Strategies ************************/

type PeriodFlushStrategy struct {
    last_flush_time int64
    period int64
}

func (s *PeriodFlushStrategy) ShouldFlush() bool {
    current_time := time.Seconds()
    if current_time - s.last_flush_time >= s.period {
        return true
    }
    return false
}

func (s *PeriodFlushStrategy) Flushed() {
    s.last_flush_time = time.Seconds()
}

type AlwaysInCacheStrategy struct {
}

func (s *AlwaysInCacheStrategy) Hit(string) {
    return
}

func (s *AlwaysInCacheStrategy) Added(string) {
    return
}

func (s *AlwaysInCacheStrategy) Removed(string) {
    return
}

func (s *AlwaysInCacheStrategy) Miss(string) {
    return
}

func (s *AlwaysInCacheStrategy) ShouldAdd(key string) bool {
    return true
}

func (s *AlwaysInCacheStrategy) GetObsoleted() []string {
    return nil
}

type AlwaysInCachePeriodFlushStrategy struct {
    AlwaysInCacheStrategy
    PeriodFlushStrategy
}

func NewAlwaysInCachePeriodFlush(period int64) KeyValueCacheStrategy {
    s := new(AlwaysInCachePeriodFlushStrategy)
    s.last_flush_time = time.Seconds()
    s.period = period
    var ret KeyValueCacheStrategy
    ret = s
    return ret
}

type LRUStrategy struct {
    q []string
    max int
}

func (s *LRUStrategy) Hit(key string) {
    return
}

func (s *LRUStrategy) Miss(key string) {
    return
}

func (s *LRUStrategy) Added(key string) {
    s.q = append(s.q, key)
}

func (s *LRUStrategy) Removed(key string) {
}
