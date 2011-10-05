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
    "crypto/tls"
    "net"
    "json"
    "os"
    "io"
    "bytes"
    "encoding/hex"
    "encoding/binary"
    "strconv"
    "sync/atomic"
    "time"
    "fmt"
)

type APNSPushService struct {
    nextid uint32
}

func init() {
    GetPushServiceManager().RegisterPushServiceType(NewAPNSPushService())
}

func NewAPNSPushService() *APNSPushService {
    ret := new(APNSPushService)
    return ret
}

func (p *APNSPushService) Name() string {
    return "apns"
}

func (p *APNSPushService) SetAsyncFailureProcessor(pfp PushFailureProcessor) {
}

func (p *APNSPushService) BuildPushServiceProviderFromMap(kv map[string]string) (*PushServiceProvider, os.Error) {
    psp := NewEmptyPushServiceProvider()
    if service, ok := kv["service"]; ok {
        psp.FixedData["service"] = service
    } else {
        return nil, os.NewError("NoService")
    }

    if cert, ok := kv["cert"]; ok {
        psp.FixedData["cert"] = cert
    } else {
        return nil, os.NewError("NoCertificate")
    }

    if key, ok := kv["key"]; ok {
        psp.FixedData["key"] = key
    } else {
        return nil, os.NewError("NoPrivateKey")
    }

    if sandbox, ok := kv["sandbox"]; ok {
        if sandbox == "true" {
            psp.VolatileData["addr"] = "gateway.sandbox.push.apple.com:2195"
            return psp, nil
        }
    }
    psp.VolatileData["addr"] = "gateway.push.apple.com:2195"
    return psp, nil
}

func (p *APNSPushService) BuildDeliveryPointFromMap(kv map[string]string) (*DeliveryPoint, os.Error) {
    dp := NewEmptyDeliveryPoint()

    if service, ok := kv["service"]; ok {
        dp.FixedData["service"] = service
    } else {
        return nil, os.NewError("NoService")
    }
    if sub, ok := kv["subscriber"]; ok {
        dp.FixedData["subscriber"] = sub
    } else {
        return nil, os.NewError("NoSubscriber")
    }
    if devtoken, ok := kv["devtoken"]; ok {
        dp.FixedData["devtoken"] = devtoken
    } else {
        return nil, os.NewError("NoDevToken")
    }
    return dp, nil
}

func toAPNSPayload(n *Notification) []byte {
    payload := make(map[string]interface{})
    aps := make(map[string]interface{})
    alert := make(map[string]interface{})
    for k, v := range n.Data {
        switch (k) {
        case "msg":
            alert["body"] = v
        case "badge":
            b, err := strconv.Atoi(v)
            if err != nil {
                continue
            } else {
                aps["badge"] = b
            }
        case "sound":
            aps["sound"] = v
        case "img":
            alert["launch-image"] = v
        case "id":
            continue
        case "expiry":
            continue
        default:
            payload[k] = v
        }
    }
    aps["alert"] = alert
    payload["aps"] = aps
    j, err := json.Marshal(payload)
    if err != nil {
        return nil
    }
    return j
}

func writen (w io.Writer, buf []byte) os.Error {
    n := len(buf)
    for n >= 0 {
        l, err := w.Write(buf)
        if err != nil {
            return err
        }
        if l >= n {
            return nil
        }
        n -= l
        buf = buf[l:]
    }
    return nil
}

func (p *APNSPushService) Push(sp *PushServiceProvider,
                        s *DeliveryPoint,
                        n *Notification) (string, os.Error) {
    cert, err := tls.LoadX509KeyPair(sp.FixedData["cert"], sp.FixedData["key"])
    if err != nil {
        return "", NewInvalidPushServiceProviderError(sp)
    }
    conf := &tls.Config {
        Certificates: []tls.Certificate{cert},
    }
    conn, err := net.Dial("tcp", sp.VolatileData["addr"])
    if err != nil {
        return "", NewInvalidPushServiceProviderError(sp)
    }

    tlsconn := tls.Client(conn, conf)
    defer tlsconn.Close()
    err = tlsconn.Handshake()
    if err != nil {
        return "", NewInvalidPushServiceProviderError(sp)
    }
    devtoken := s.FixedData["devtoken"]
    btoken, err := hex.DecodeString(devtoken)
    if err != nil {
        return "", NewInvalidDeliveryPointError(sp, s)
    }

    bpayload := toAPNSPayload(n)
    if bpayload == nil {
        return "", NewInvalidNotification(sp, s, n)
    }
    buffer := bytes.NewBuffer([]byte{})
    // command
    binary.Write(buffer, binary.BigEndian, uint8(1))

    // transaction id
    mid := atomic.AddUint32(&(p.nextid), 1)
    if smid, ok := n.Data["id"]; ok {
        imid, err := strconv.Atoui(smid)
        if err == nil {
            mid = uint32(imid)
        }
    }
    binary.Write(buffer, binary.BigEndian, mid)

    // Expiry
    expiry := uint32(time.Seconds() + 60*60)

    if sexpiry, ok := n.Data["expiry"]; ok {
        uiexp, err := strconv.Atoui(sexpiry)
        if err == nil {
            expiry = uint32(uiexp)
        }
    }
    binary.Write(buffer, binary.BigEndian, expiry)

    // device token
    binary.Write(buffer, binary.BigEndian, uint16(len(btoken)))
    binary.Write(buffer, binary.BigEndian, btoken)

    // payload
    binary.Write(buffer, binary.BigEndian, uint16(len(bpayload)))
    binary.Write(buffer, binary.BigEndian, bpayload)
    pdu := buffer.Bytes()

    err = writen(tlsconn, pdu)
    if err != nil {
        return "", NewInvalidPushServiceProviderError(sp)
    }
    tlsconn.SetReadTimeout(5E8)
    readb := [6]byte{}
    nr, err := tlsconn.Read(readb[:])
    /* TODO error handling */
    if nr > 0 {
        switch(readb[1]) {
        case 2:
            return "", NewInvalidDeliveryPointError(sp, s)
        default:
            return "", NewInvalidPushServiceProviderError(sp)
        }
    }
    return fmt.Sprintf("%d", mid), nil
}

