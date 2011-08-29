package db

import (
    "time"
    "container/list"
    //"fmt"
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

    // Called when things get dirty
    Dirty(key string)

    // All dirty things has become clear
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

func (s *PeriodFlushStrategy) Dirty(key string) {
    return
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

type timedkey struct {
    last_access_time int64
    key string
}

type LRUStrategy struct {
    q *list.List
    items map[string]*list.Element
    max int
}

func NewLRUStrategy(max int) *LRUStrategy {
    ret := new(LRUStrategy)
    if max <= 0 {
        ret.max = 1024
    } else {
        ret.max = max
    }
    ret.items = make(map[string]*list.Element, ret.max)
    ret.q = list.New()
    return ret
}

func (s *LRUStrategy) Hit(key string) {
    //fmt.Print("Hit ", key, "\n")
    if d, has := s.items[key]; has {
        s.q.MoveToFront(d)
    } else {
        t := timedkey{time.Nanoseconds(), key}
        e := s.q.PushFront(t)
        s.items[key] = e, true
    }
    return
}

func (s *LRUStrategy) Miss(key string) {
    return
}

func (s *LRUStrategy) Added(key string) {
    s.Hit(key)
}

func (s *LRUStrategy) Removed(key string) {
    //fmt.Print("Removed ", key, "\n")
    e, has := s.items[key]
    if has {
        s.q.Remove(e)
        s.items[key] = nil, false
    }
}

func (s *LRUStrategy) ShouldAdd(key string) bool {
    return true
}

func (s *LRUStrategy) GetObsoleted() []string {
    if s.q.Len() <= s.max {
        return nil
    }
    nr_removed := s.q.Len() - s.max
    e := s.q.Back()
    ret := make([]string, 0, nr_removed)

    for i := 0; i < nr_removed; i++ {
        d := e.Value.(timedkey)
        ret = append(ret, d.key)
        e = e.Prev()
    }

    //fmt.Print("You should remove ", ret, "\n")

    return ret
}

type lruPeriodFlushStrategy struct {
    *LRUStrategy
    PeriodFlushStrategy
}

func NewLRUPeriodFlushStrategy(max int, period int64) KeyValueCacheStrategy {
    lru := NewLRUStrategy(max)
    s := new(lruPeriodFlushStrategy)
    s.LRUStrategy = lru
    s.period = period

    var ret KeyValueCacheStrategy
    ret = s
    return ret
}

