package uniqush

import (
    "testing"
    "fmt"
)

func TestRedisConnection(t *testing.T) {
    /*
    c := new(DatabaseConfig)
    c.Port = -1
    c.Engine = "redis"
    c.Name = "6"
    udb := NewUniqushRedisDB(c)

    fmt.Print("Test redis connection ...\t")
    v, err := udb.client.Get("test_counter")

    if err != nil {
        t.Errorf("Error: %v\n", err)
    }
    if string(v) != "0" && v != nil {
        t.Errorf("the test_counter should be 0, but it is %v now\n", string(v))
    }
    fmt.Print("OK\n")
    */
}

func TestKV2DP(t *testing.T) {
    var value string = "1.1000:2000"
    dp := keyValueToDeliveryPoint("myandroid", []byte(value))
    if dp == nil {
        t.Errorf("Wrong!")
        return
    }
    b := deliveryPointToValue(dp)
    str := string(b)

    if value != str {
        t.Errorf("Wrong! %s != %s\n", value, str)
        return
    }
    //fmt.Printf("%s\n%s\n", dp.Debug(), value)
}

func getUDB() *UniqushRedisDB {
    c := new(DatabaseConfig)
    c.Port = -1
    c.Engine = "redis"
    c.Name = "6"
    udb := NewUniqushRedisDB(c)
    return udb
}

func getCachedUDB() UniqushDatabase {
    udb := getUDB()
    ret := NewCachedUniqushDatabase(udb, udb, nil)
    return ret
}

func BenchmarkRedisDB(b *testing.B) {
    name := "myandroid"
    udb := getUDB()
    for i := 0; i < 10000; i++ {
        dp, _ := udb.GetDeliveryPoint(name)
        if dp.Name != name {
            fmt.Printf("Wrong Name!")
            return
        }
    }
}

func BenchmarkCachedRedisDB(b *testing.B) {
    name := "myandroid"
    udb := getCachedUDB()
    for i := 0; i < 10000; i++ {
        dp, _ := udb.GetDeliveryPoint(name)
        if dp.Name != name {
            fmt.Printf("Wrong Name!")
            return
        }
    }
}

func TestGetSetDeliveryPoint(t *testing.T) {
    var value string = "1.1000:2000"
    dp := keyValueToDeliveryPoint("myandroid", []byte(value))
    c := new(DatabaseConfig)
    c.Port = -1
    c.Engine = "redis"
    c.Name = "6"
    udb := NewUniqushRedisDB(c)

    udb.SetDeliveryPoint(dp)
    ndp, _ := udb.GetDeliveryPoint(dp.Name)

    b := deliveryPointToValue(ndp)
    str := string(b)

    if value != str {
        t.Errorf("Wrong! %s != %s\n", value, str)
        return
    }

    //fmt.Printf("%s==%s ...\tOK\n", value, str)
}
