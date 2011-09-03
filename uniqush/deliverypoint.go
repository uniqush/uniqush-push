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
    "fmt"
    "strings"
)

type DeliveryPoint struct {
    OSType
    Name string
    token string
    account string
}

type AndroidDeliveryPoint interface {
    GoogleAccount() string
    RegistrationID() string
}

type IOSDeliveryPoint interface {
    AppleAccount() string
    DeviceToken() string
}

func NewAndroidDeliveryPoint(name, account, regid string) *DeliveryPoint{
    s := new(DeliveryPoint)
    s.Name = name
    s.OSType = OS_ANDROID
    s.token = regid
    s.account = account
    return s
}

func (s *DeliveryPoint) DeviceToken() string {
    if s.OSID() == OSTYPE_IOS {
        return s.token
    }
    return ""
}

func (s *DeliveryPoint) AppleAccount() string {
    if s.OSID() == OSTYPE_ANDROID {
        return s.account
    }
    return ""
}


func (s *DeliveryPoint) GoogleAccount() string {
    if s.OSID() == OSTYPE_ANDROID {
        return s.account
    }
    return ""
}

func (s *DeliveryPoint) RegistrationID() string {
    if s.OSID() == OSTYPE_ANDROID {
        return s.token
    }
    return ""
}

func (s *DeliveryPoint) Debug() string {
    ret := "OS: "
    ret += s.OSName()
    ret += "\n"

    ret += "Name: " + s.Name + "\n"
    ret += "Account: " + s.account+ "\n"
    ret += "Token: " + s.token+ "\n"
    return ret
}

func (s *DeliveryPoint) UniqStr() string {
    return s.OSName() + ":" + s.account + "#" + s.token
}

func (dp *DeliveryPoint) Marshal() []byte {
    str := fmt.Sprintf("%d.%s:%s", dp.OSID(), dp.account, dp.token)
    return []byte(str)
}

func (dp *DeliveryPoint) Unmarshal(name string, value []byte) *DeliveryPoint {
    v := string(value)
    var substr string
    var ostype int
    fmt.Sscanf(v, "%d.%s", &ostype, &substr)
    dp.OSType.id = ostype

    fields := strings.Split(substr, ":")
    if len(fields) < 2 {
        return nil
    }
    dp.Name = name
    dp.account = fields[0]
    dp.token = fields[1]
    return dp
}
