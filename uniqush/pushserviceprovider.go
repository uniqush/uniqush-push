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
    "fmt"
    "strings"
)

type PushServiceProvider struct {
    ServiceType
    Name string
    sender_id string
    auth_token string
    real_auth_token string
}

type C2DMServiceProvider interface {
    SenderID() string
    AuthToken() string
    UpdateAuthToken(token string)
}

func NewC2DMServiceProvider(name, senderid, auth string) *PushServiceProvider {
    s := &PushServiceProvider{SERVICE_C2DM, name, senderid, auth, auth}
    return s
}

func NewAPNSServiceProvider(name, cert, key string, sandbox bool) *PushServiceProvider {
    psp := new(PushServiceProvider)
    psp.ServiceType = SERVICE_APNS
    psp.auth_token = cert
    psp.sender_id = key
    if !sandbox {
        psp.real_auth_token = "1"
    } else {
        psp.real_auth_token = "0"
    }
    return psp
}
/* TODO Other service providers */

func (sp *PushServiceProvider) SenderID() string {
    if sp.ServiceID() == SRVTYPE_C2DM {
        return sp.sender_id
    }
    return ""
}

func (sp *PushServiceProvider) AuthToken() string {
    if sp.ServiceID() == SRVTYPE_C2DM {
        return sp.real_auth_token
    }
    return ""
}

func (psp *PushServiceProvider) UpdateAuthToken(token string) {
    psp.real_auth_token = token
}

func (sp *PushServiceProvider) UniqStr() string {
    // It seems that we only need name and sender_id to identify a psp
    // inside a service
    return sp.ServiceName() + ":" + sp.sender_id // + "#" + sp.auth_token
}

func (sp *PushServiceProvider) Marshal() []byte {
    str := fmt.Sprintf("%d.%s:%s:%s", sp.ServiceID(), sp.sender_id, sp.auth_token, sp.real_auth_token)
    return []byte(str)
}

func (psp *PushServiceProvider) Unmarshal(name string, value []byte) *PushServiceProvider{
    v := string(value)
    var substr string
    var srvtype int
    fmt.Sscanf(v, "%d.%s", &srvtype, &substr)

    psp.ServiceType.id = srvtype
    fields := strings.Split(substr, ":")
    if len(fields) < 2 {
        return nil
    }
    psp.Name = name
    psp.sender_id = fields[0]
    psp.auth_token = fields[1]
    if len(fields) == 3 {
        psp.real_auth_token = fields[2]
    } else {
        psp.real_auth_token = fields[1]
    }
    return psp
}

