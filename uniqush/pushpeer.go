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
    "json"
    "fmt"
    "os"
    "crypto/sha1"
)

type PushPeer struct {
    name string
    pushServiceType PushServiceType
    VolatileData map[string]string
    FixedData map[string]string
}

func (p *PushPeer) PushServiceName() string {
    return p.pushServiceType.Name()
}

func (p *PushPeer) ToString() string {
    ret := "push service type: "
    ret += p.pushServiceType.Name()
    ret += "\nFixed Data:\n"

    for k, v := range(p.FixedData) {
        ret += k + ": " + v + "\n"
    }

    ret += "\n"
    return ret
}

func (p *PushPeer) InitPushPeer() {
    p.pushServiceType = nil
    p.VolatileData = make(map[string]string, 2)
    p.FixedData = make(map[string]string, 2)
}

func (p *PushPeer) Name() string {
    if p.name != "" {
        return p.name
    }
    hash := sha1.New()
    if p.FixedData == nil {
        return ""
    }
    b, _ := json.Marshal(p.FixedData)
    hash.Write(b)
    p.name = fmt.Sprintf("%s:%x",
                         p.pushServiceType.Name(),
                         hash.Sum())
    return p.name
}

func (p *PushPeer) Marshal() []byte {
    if p.pushServiceType == nil {
        return nil
    }
    s := make([]map[string]string, 2)
    s[0] = p.FixedData
    s[1] = p.VolatileData
    b, err := json.Marshal(s)
    if err != nil {
        return nil
    }
    str := p.pushServiceType.Name() + ":" + string(b)
    return []byte(str)
}

func (p *PushPeer) Unmarshal(value []byte) os.Error {
    //var f interface{}

    var f []map[string]string

    err := json.Unmarshal(value, &f)
    if err != nil {
        fmt.Printf("Error Unmarshal: %v\n", err)
        return err
    }

    if len(f) < 2 {
        return os.NewError("Invalid Push Peer")
    }

    p.FixedData = f[0]
    p.VolatileData = f[1]

    /*
    var s []interface{}
    var m map[string]interface{}

    switch f.(type) {
    case []interface{}:
        s = f.([]interface{})
        if len(s) < 2 {
            return os.NewError("Invalid Push Peer")
        }

        switch s[0].(type) {
        case map[string]interface{}:
            m = s[0].(map[string]interface{})
            for k, v := range m {
                switch v.(type) {
                case string:
                    str := v.(string)
                    p.FixedData[k] = str
                default:
                    return os.NewError("Invalid Push Peer")
                }
            }
        default:
            return os.NewError("Invalid Push Peer")
        }

        switch s[1].(type) {
        case map[string]interface{}:
            m = s[0].(map[string]interface{})
            for k, v := range m {
                switch v.(type) {
                case string:
                    str := v.(string)
                    p.VolatileData[k] = str
                default:
                    return os.NewError("Invalid Push Peer")
                }
            }
        default:
            return os.NewError("Invalid Push Peer")
        }

    }
    */

    return nil
}

