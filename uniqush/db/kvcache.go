package db

// This file defined interfaces for key-value caches
// and some simple implementations

import (
    "os"
    "sync"
)

type kvdata struct {
    key string
    value interface{}
}

// This should be some database-related struct
// to flush dirty data into database
// The implementation could put the real removal and insertion action
// in either the Flush() or Add()/Remove() functions.
// The Cache will always call Flush() after a bunch of Add()/Remove()
type KeyValueFlusher interface {
    Add(key string, value interface{}) os.Error
    Remove(key string, value interface{}) os.Error
    Flush() os.Error
}


// A key-value cache is like a lazy person:
// You want him to remember something (for example, building his vocabulary)
// You provide the data in key-value forms (word - meaning)
// 
// Suppose this man holds a dictionary which has all word. But he
// will not remember all of them in his mind.
//
// At first, you pick up a word from dictionary, and show it to that person.
// (Using Show() method)
// You don't know if he has remembered this word. He has his own strategy to
// decide which word should remember.
//
// Then you ask him about the word by calling Get(). You may or may not get
// an answer. This person will never look up the word in dictionary. He only
// tells you the word he remembered.
//
// If a word's meaning changed, which means the value changed, you call 
// Modify() to tell the person this change and ask him to update his 
// dictionary also. However, because updating dictionary is really expensive,
// and it could be cheaper if you update more words at same time, moreover,
// this person is really lazy, he may wait for a while and update the
// dictionary later hopefully could get more word need to be updated.
//
// Similarly, if you remove some word by calling Remove(), this person
// may not update the dictionary immediately. Instead, he will keep a list
// of words need to be removed and remove them all once.
//
// I hope this analogue could help someone to understand the cache. Very
// simple idea. Think some database as the dictionary and the cache as the
// lazy guy.
//
// MFAQ (May be Frequently Asked Questions, because nobody asked them now)
//
// Why not let the cache find value from the
// database on cache miss? After all, we only need to define a new interface
// and the cache could call some method, say Get(), in that interface and then
// return the value. It helps a lot, doesn't it? And actually, we already
// has such interface, the KeyValueStorage could be used.
//
// Well, here is the reason: we may not use a key-value database behind the
// cache. The possible Get() method takes a string and return some value.
// If it is not a key-value database, we have to parse the string and find
// what we really want in the implementation of Get(). Parsing string
// may take a lot of time and more important: we need to write some tedious
// code to split the string, compare it, or some regular expressions, etc.
// If we want to add support such Get() feature, then we need either 1)
// to do the tedious string parsing work, or 2) to have more database- or
// even application-specific knowledge to get the value we want from the
// database. For example, we could have some function like db.GetUser()
// to get the user data. However, it is definitely not a good design
//
// In short: we don't want the cache has too much information about the
// database or application.
//
// Then another question may be in your mind: then why we need KeyValueFlusher
// to flush the data? It involves database anyway, right?
//
// The answer is simple: bacause we provide more than a key to the methods
// in KeyValueFlusher. The Set() and Remove() method in KeyValueFlusher
// take both key and value as their parameters. This means the database- or
// even application-specific data could be stored in the value and the
// implementation of KeyValueFlusher could take such data from value, then
// flush them without even taking a look at the key.
//
type KeyValueCache struct {
    storage KeyValueStorage
    strategy KeyValueCacheStrategy
    flusher KeyValueFlusher
    dirty_list []kvdata
    rm_list []kvdata
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
    c.rm_list = make([]kvdata, 0, 10)
    c.flusher = flusher
    return c
}

func (c *KeyValueCache) remove() os.Error {
    c.rwlock.Lock()
    defer c.rwlock.Unlock()

    rmlist := c.strategy.GetObsoleted()
    var err os.Error
    for _, k := range rmlist {
        _, err = c.storage.Remove(k)
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
            err := c.flusher.Add(d.key, d.value)
            if err != nil {
                return err
            }
        }

        c.dirty_list = make([]kvdata, 0, len(c.dirty_list))

        for _, d := range c.rm_list {
            err := c.flusher.Remove(d.key, d.value)
            if err != nil {
                return err
            }
        }

        c.rm_list = make([]kvdata, 0, len(c.rm_list))
        c.flusher.Flush()
        c.strategy.Flushed()
    }
    return nil
}

// The caller could Show a key value pair to a cache,
// and let the cache decide if it want to add this pair into the cache.
// A cache make this decision based on its strategy
func (c *KeyValueCache) Show(key string, v interface{}) os.Error {
    var err os.Error
    if should_add := c.strategy.ShouldAdd(key); should_add {

        c.rwlock.Lock()
        oldv, err := c.storage.Set(key, v)
        if oldv == nil {
            c.strategy.Added(key)
        }
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

func (c *KeyValueCache) Modify(key string, v interface{}) os.Error {
    c.rwlock.Lock()
    defer c.rwlock.Unlock()

    // First, this data is dirty
    c.dirty_list = append(c.dirty_list, kvdata{key, v})

    oldv, err := c.storage.Get(key)
    if err != nil {
        v = nil
        c.rwlock.RUnlock()
        return err
    }

    if oldv == nil {
        // This data is not in cache. So a cache-miss occur
        // We need to know if we should add this value into cache
        c.strategy.Miss(key)
        if should_add := c.strategy.ShouldAdd(key); should_add {
            _, err := c.storage.Set(key, v)
            c.strategy.Added(key)
            return err
        }
        return nil
    }
    // Cache hit. We need to update to the latest value
    c.strategy.Hit(key)
    _, err = c.storage.Set(key, v)
    return nil
}

func (c *KeyValueCache) Get(key string) (v interface{}, err os.Error) {
    c.rwlock.RLock()
    v, err = c.storage.Get(key)
    if err != nil {
        v = nil
        c.rwlock.RUnlock()
        return
    }

    if v == nil {
        // Cache miss
        c.strategy.Miss(key)
    } else {
        // Cache hit
        c.strategy.Hit(key)
    }
    c.rwlock.RUnlock()

    err = c.remove()
    err = c.flush()
    return
}

// Remove records the key need to be removed. And perform the
// actual removal in next flush
// Why not return the value? because the item may not in the cache.
func (c *KeyValueCache) Remove(key string) (err os.Error) {
    c.rwlock.Lock()

    var v interface{}
    v, err = c.storage.Remove(key)
    if err != nil {
        return err
    }
    c.strategy.Removed(key)

    c.rm_list = append(c.rm_list, kvdata{key, v})
    c.rwlock.Unlock()

    err = c.remove()
    err = c.flush()
    return
}

