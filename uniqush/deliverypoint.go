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
)

type DeliveryPoint struct {
    /* Begin Obsoleted */
    OSType
    //Name string
    token string
    account string
    /* End Obsoleted */

    PushPeer
}

func NewEmptyDeliveryPoint() *DeliveryPoint {
    ret := new(DeliveryPoint)
    ret.InitPushPeer()
    return ret
}

/* Begin Obsoleted */
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

func NewIOSDeliveryPoint(name, devtoken string) *DeliveryPoint {
    s := new(DeliveryPoint)
    s.Name = name
    s.token = devtoken
    s.OSType = OS_IOS
    s.account = "unused"
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

/*
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
*/
/* End Obsoleted */
