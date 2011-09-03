/*
 *  Uniqush by Nan Deng
 *  Copyright (C) 2010 Nan Deng
 *
 *  This software is free software; you can redistribute it and/or
 *  modify it under the terms of the GNU Lesser General Public
 *  License as published by the Free Software Foundation; either
 *  version 3.0 of the License, or (at your option) any later version.
 *
 *  This software is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 *  Lesser General Public License for more details.
 *
 *  You should have received a copy of the GNU Lesser General Public
 *  License along with this software; if not, write to the Free Software
 *  Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA  02111-1307  USA
 *
 *  Nan Deng <monnand@gmail.com>
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

    f := NewDatabaseFrontDesk(c)
    f.AddPushServiceProviderToService(service, sp)
    f.AddDeliveryPointToService(service, subscriber, s, -1)

    f.FlushCache()
    fmt.Print("Finished\n")
}

