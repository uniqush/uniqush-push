package db

import (
    "testing"
    "uniqush"
    "fmt"
)

const (
    registration_id string = "FakeRegisterId"
    auth string = "FakeAuthID"
)

func TestDatabaseFrontDesk(t *testing.T) {
    fmt.Print("Wrinting test subscribers ...\t\n")
    sp := uniqush.NewC2DMServiceProvider("", "monnand@gmail.com", auth)
    s := uniqush.NewAndroidDeliveryPoint("", "nan.deng.osu@gmail.com", registration_id)
    service := "MyTestService"
    subscriber := "Monnand"

    c := new(DatabaseConfig)
    c.Port = -1
    c.Engine = "redis"
    c.Name = "6"
    c.EverySec = 600
    c.LeastDirty = 10
    c.CacheSize = 10

    f := NewDatabaseFrontDesk(c)
    f.AddPushServiceProviderToService(service, sp)
    f.AddDeliveryPointToService(service, subscriber, s, -1)

    f.FlushCache()
    fmt.Print("Finished\n")
}

