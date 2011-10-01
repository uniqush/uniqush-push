/*
 * Copyright 2011 Nan Deng
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

package uniqush

import (
    "testing"
    "fmt"
)

const (
    registration_id string = "FakeRegisterId"
    auth string = "FakeAuthID"
)

func TestDatabaseFrontDesk(t *testing.T) {
    fmt.Print("Wrinting test subscribers ...\t\n")
    sp := NewC2DMServiceProvider("", "monnand@gmail.com", auth)
    s := NewAndroidDeliveryPoint("", "nan.deng.osu@gmail.com", registration_id)
    service := "MyTestService"
    subscriber := "Monnand"

    c := new(DatabaseConfig)
    c.Port = -1
    c.Engine = "redis"
    c.Name = "6"
    c.EverySec = 600
    c.LeastDirty = 10
    c.CacheSize = 10

    f, _ := NewDatabaseFrontDesk(c)
    f.AddPushServiceProviderToService(service, sp)
    f.AddDeliveryPointToService(service, subscriber, s, -1)

    f.FlushCache()
    fmt.Print("Finished\n")
}

