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
)

type APNSPusher struct {
	ServiceType
}

func NewAPNSPusher() *APNSPusher {
	ret := &APNSPusher{ServiceType{SRVTYPE_APNS}}
	return ret
}

type APNSService struct {
    psp *PushServiceProvider
}

func (s *APNSService) Certificate() string {
    return s.psp.auth_token
}

func (s *APNSService) PrivateKey() string {
    return s.psp.sender_id
}

func (s *APNSService) Address() string {
    if s.psp.real_auth_token == "" {
        return "gateway.sandbox.push.apple.com:2195"
    }
    return "gateway.push.apple.com:2195"
}

func NewAPNSService(psp *PushServiceProvider) *APNSService {
    ret := new(APNSService)
    ret.psp = psp
    return ret
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

func getDeviceToken(dp *DeliveryPoint) string {
    return dp.token
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

func (p *APNSPusher) Push(sp *PushServiceProvider,
                        s *DeliveryPoint,
                        n *Notification) (string, os.Error) {
    apnsServie := NewAPNSService(sp)
    cert, err := tls.LoadX509KeyPair(apnsServie.Certificate(), apnsServie.PrivateKey())
    if err != nil {
		return "", NewInvalidPushServiceProviderError(sp)
    }
    conf := &tls.Config {
              Certificates: []tls.Certificate{cert},
          }
    conn, err := net.Dial("tcp", apnsServie.Address())
    if err != nil {
		return "", NewInvalidPushServiceProviderError(sp)
    }

	tlsconn := tls.Client(conn, conf)
	err = tlsconn.Handshake()
    if err != nil {
		return "", NewInvalidPushServiceProviderError(sp)
    }
    devtoken := getDeviceToken(s)
	btoken, err := hex.DecodeString(devtoken)
    if err != nil {
        return "", NewInvalidDeliveryPointError(sp, s)
    }

    bpayload := toAPNSPayload(n)
    if bpayload == nil {
        /* FIXME new error type */
        return "", NewInvalidDeliveryPointError(sp, s)
    }
	buffer := bytes.NewBuffer([]byte{})
	// command
	binary.Write(buffer, binary.BigEndian, uint8(0))

	// push device token
	binary.Write(buffer, binary.BigEndian, uint16(len(btoken)))
	binary.Write(buffer, binary.BigEndian, btoken)

	// push payload
	binary.Write(buffer, binary.BigEndian, uint16(len(bpayload)))
	binary.Write(buffer, binary.BigEndian, bpayload)
	pdu := buffer.Bytes()

	// write pdu
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
    return "", nil
}

