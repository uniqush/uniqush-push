package db

import (
    "time"
)

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
