package db

import (
    "uniqush"
    "os"
)

type UniqushDatabaseWriter interface {
    SetDeliveryPoint(dp *uniqush.DeliveryPoint) os.Error
    SetPushServiceProvider(psp *uniqush.PushServiceProvider) os.Error
    RemoveDeliveryPoint(dp *uniqush.DeliveryPoint) os.Error
    RemovePushServiceProvider(psp *uniqush.PushServiceProvider) os.Error
}

type UniqushDatabaseReader interface {
    GetDeliveryPoint(name string) (*uniqush.DeliveryPoint, os.Error)
    GetPushServiceProvider(name string) (*uniqush.PushServiceProvider, os.Error)
    /* TODO Subscriber, Service, etc. */
}

type UniqushDatabase interface {
    UniqushDatabaseReader
    UniqushDatabaseWriter
}

type CachedFlusher struct {
    rmlist []kvdata
    dirtylist []kvdata
    flusher KeyValueFlusher
}

func NewCachedFlusher(flusher KeyValueFlusher) KeyValueFlusher {
    f := new(CachedFlusher)
    f.flusher = flusher
    f.rmlist = make([]kvdata, 0, 128)
    f.dirtylist = make([]kvdata, 0, 128)
    return f
}

func (f *CachedFlusher) Set(key string, value interface{}) os.Error {
    f.dirtylist = append(f.dirtylist, kvdata{key, value})
    return nil
}

func (f *CachedFlusher) Remove(key string, value interface{}) os.Error {
    f.rmlist= append(f.rmlist, kvdata{key, value})
    return nil
}

func (f *CachedFlusher) Flush() os.Error {
    var err os.Error
    for _, d := range f.dirtylist {
        err = f.flusher.Set(d.key, d.value)
        if err != nil {
            return err
        }
    }
    for _, d := range f.rmlist {
        err = f.flusher.Remove(d.key, d.value)
        if err != nil {
            return err
        }
    }
    return nil
}

type DeliveryPointFlusher struct {
    dbwriter UniqushDatabaseWriter
}

func NewDeliveryPointFlusher(dbwriter UniqushDatabaseWriter) KeyValueFlusher {
    ret := new(DeliveryPointFlusher)
    ret.dbwriter = dbwriter
    return ret
}

func (f *DeliveryPointFlusher) Set(key string, value interface{}) os.Error {
    return f.dbwriter.SetDeliveryPoint(value.(*uniqush.DeliveryPoint))
}

func (f *DeliveryPointFlusher) Remove(key string, value interface{}) os.Error {
    return f.dbwriter.RemoveDeliveryPoint(value.(*uniqush.DeliveryPoint))
}

func (f *DeliveryPointFlusher) Flush() os.Error {
    return nil
}

type PushServiceProviderFlusher struct {
    dbwriter UniqushDatabaseWriter
}

func NewPushServiceProviderFlusher(dbwriter UniqushDatabaseWriter) KeyValueFlusher {
    ret := new(PushServiceProviderFlusher)
    ret.dbwriter = dbwriter
    return ret
}

func (f *PushServiceProviderFlusher) Set(key string, value interface{}) os.Error {
    return f.dbwriter.SetPushServiceProvider(value.(*uniqush.PushServiceProvider))
}

func (f *PushServiceProviderFlusher) Remove(key string, value interface{}) os.Error {
    return f.dbwriter.RemovePushServiceProvider(value.(*uniqush.PushServiceProvider))
}

func (f *PushServiceProviderFlusher) Flush() os.Error {
    return nil
}

func getDeliveryPointFlusher(dbwriter UniqushDatabaseWriter) KeyValueFlusher {
    dpflusher := NewDeliveryPointFlusher(dbwriter)
    cached := NewCachedFlusher(dpflusher)
    return cached
}

func getPushServiceProviderFlusher(dbwriter UniqushDatabaseWriter) KeyValueFlusher {
    dpflusher := NewPushServiceProviderFlusher(dbwriter)
    cached := NewCachedFlusher(dpflusher)
    return cached
}

// This is a decorator
type CachedUniqushDatabase struct {
    psp_cache KeyValueCacheIf
    dp_cache KeyValueCacheIf
    dbreader UniqushDatabaseReader
    dbwriter UniqushDatabaseWriter
}

func NewCachedUniqushDatabase(dbreader UniqushDatabaseReader,
                              dbwriter UniqushDatabaseWriter,
                              max int,
                              flush_period int64,
                              min_dirty int) UniqushDatabase {
    cdb := new(CachedUniqushDatabase)
    cdb.dbreader = dbreader
    cdb.dbwriter = dbwriter

    // Delivery Points stored in an LRU cache
    lru := NewLRUPeriodFlushStrategy(max, flush_period, min_dirty)
    storage := NewInMemoryKeyValueStorage(max + 10)
    cdb.dp_cache = NewKeyValueCache(storage, lru, getDeliveryPointFlusher(dbwriter))

    // Push Service Providers are always in cache
    alwaysin := NewAlwaysInCachePeriodFlushStrategy(flush_period, min_dirty)
    storage = NewInMemoryKeyValueStorage(max + 10)
    cdb.psp_cache = NewKeyValueCache(storage, alwaysin, getPushServiceProviderFlusher(dbwriter))

    return cdb
}

func (cdb *CachedUniqushDatabase) GetDeliveryPoint(name string) (dp *uniqush.DeliveryPoint, err os.Error) {
    dpif, e := cdb.dp_cache.Get(name)
    if e != nil {
        dp = nil
        err = e
        return
    }

    if dpif == nil {
        dpif, err = cdb.dbreader.GetDeliveryPoint(name)
        if err != nil {
            dp = nil
            return
        }
        if dpif == nil {
            dp = nil
            return
        }
        cdb.dp_cache.Show(name, dpif)
    }
    dp = dpif.(*uniqush.DeliveryPoint)

    return
}

func (cdb *CachedUniqushDatabase) GetPushServiceProvider(name string) (psp *uniqush.PushServiceProvider, err os.Error) {
    pspif, e := cdb.psp_cache.Get(name)
    if e != nil {
        psp = nil
        err = e
        return
    }

    if pspif == nil {
        pspif, err = cdb.dbreader.GetPushServiceProvider(name)
        if err != nil {
            psp = nil
            return
        }
        if pspif == nil {
            psp = nil
            return
        }
        cdb.psp_cache.Show(name, pspif)
    }
    psp = pspif.(*uniqush.PushServiceProvider)

    return
}

func (cdb *CachedUniqushDatabase) SetDeliveryPoint(dp *uniqush.DeliveryPoint) os.Error {
    return cdb.dp_cache.Modify(dp.Name, dp)
}
func (cdb *CachedUniqushDatabase) SetPushServiceProvider(psp *uniqush.PushServiceProvider) os.Error {
    return cdb.psp_cache.Modify(psp.Name, psp)
}
func (cdb *CachedUniqushDatabase) RemoveDeliveryPoint(dp *uniqush.DeliveryPoint) os.Error {
    return cdb.dp_cache.Remove(dp.Name)
}
func (cdb *CachedUniqushDatabase) RemovePushServiceProvider(psp *uniqush.PushServiceProvider) os.Error {
    return cdb.psp_cache.Remove(psp.Name)
}
