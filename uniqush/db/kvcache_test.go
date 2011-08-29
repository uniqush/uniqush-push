package db

import (
    "testing"
    "fmt"
    "os"
)

type FakeFlusher struct {
}

func (f *FakeFlusher) Set(key string, v interface{}) os.Error {
    fmt.Print("Flush: ", key)
    return nil
}

func (f *FakeFlusher) Remove(key string, v interface{}) os.Error {
    fmt.Print("Remove: ", key)
    return nil
}

func (f *FakeFlusher) Flush() os.Error {
    return nil
}

func TestLRUCache(t *testing.T) {
    strategy := NewLRUPeriodFlushStrategy(3, 100)
    storage := NewInMemoryKeyValueStorage(10)
    flusher := &FakeFlusher{}

    cache := NewKeyValueCache(storage, strategy, flusher)

    for i := 0; i < 10; i++ {
        str := fmt.Sprint(i)
        cache.Show(str, str)
    }

    keys, _ := cache.Keys()
    if !same(convert2string([]int{7,9,8}), keys) {
        t.Errorf("should be [7 8 9], but %v", keys)
    }
    fmt.Print(len(keys), ": ", keys)
}

