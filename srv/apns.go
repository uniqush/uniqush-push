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

package srv

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	. "github.com/uniqush/uniqush-push/push"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	maxWaitTime time.Duration = 5
)

type pushRequest struct {
	psp *PushServiceProvider
	dp *DeliveryPoint
	notif *Notification
	mid uint32
	resChan chan<- *PushResult
}

type apnsPushService struct {
	reqChan chan *pushRequest
}

func InstallAPNS() {
	GetPushServiceManager().RegisterPushServiceType(newAPNSPushService())
}

func newAPNSPushService() *apnsPushService {
	ret := new(apnsPushService)
	ret.reqChan = make(chan *pushRequest)
	go ret.pushMux()
	return ret
}

func (p *apnsPushService) Name() string {
	return "apns"
}

func (p *apnsPushService) Finalize() {
	close(p.reqChan)
	p.reqChan = nil
}

func (p *apnsPushService) BuildPushServiceProviderFromMap(kv map[string]string, psp *PushServiceProvider) error {
	if service, ok := kv["service"]; ok {
		psp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}

	if cert, ok := kv["cert"]; ok && len(cert) > 0 {
		psp.FixedData["cert"] = cert
	} else {
		return errors.New("NoCertificate")
	}

	if key, ok := kv["key"]; ok && len(key) > 0 {
		psp.FixedData["key"] = key
	} else {
		return errors.New("NoPrivateKey")
	}

	if sandbox, ok := kv["sandbox"]; ok {
		if sandbox == "true" {
			psp.VolatileData["addr"] = "gateway.sandbox.push.apple.com:2195"
			return nil
		}
	}
	psp.VolatileData["addr"] = "gateway.push.apple.com:2195"
	return nil
}

func (p *apnsPushService) BuildDeliveryPointFromMap(kv map[string]string, dp *DeliveryPoint) error {
	if service, ok := kv["service"]; ok && len(service) > 0 {
		dp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}
	if sub, ok := kv["subscriber"]; ok && len(sub) > 0 {
		dp.FixedData["subscriber"] = sub
	} else {
		return errors.New("NoSubscriber")
	}
	if devtoken, ok := kv["devtoken"]; ok && len(devtoken) > 0 {
		if len(devtoken) != 40 {
			return fmt.Errorf("Dev token %v is invalid: it should be 40 characters", devtoken)
		}
		dp.FixedData["devtoken"] = devtoken
	} else {
		return errors.New("NoDevToken")
	}
	return nil
}

func toAPNSPayload(n *Notification) ([]byte, error) {
	payload := make(map[string]interface{})
	aps := make(map[string]interface{})
	alert := make(map[string]interface{})
	for k, v := range n.Data {
		switch k {
		case "msg":
			alert["body"] = v
		case "loc-key":
			alert[k] = v
		case "loc-args":
			alert[k] = v
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
		case "ttl":
			continue
		default:
			payload[k] = v
		}
	}
	aps["alert"] = alert
	payload["aps"] = aps
	j, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return j, nil
}

func writen(w io.Writer, buf []byte) error {
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

type apnsResult struct {
	msgId uint32
	status uint8
	err error
}

func (self *apnsPushService) Push(psp *PushServiceProvider, dpQueue <-chan *DeliveryPoint, resQueue chan<- *PushResult, notif *Notification) {
	wg := new(sync.WaitGroup)

	for dp := range dpQueue {
		wg.Add(1)
		go func () {
			resultChannel := make(chan *PushResult, 1)
			req := new(pushRequest)
			req.dp = dp
			req.notif = notif
			req.resChan = resultChannel
			req.psp = psp

			self.reqChan <- req

			for {
				select {
				case res := <-resultChannel:
					resQueue <- res
				case <-time.After(maxWaitTime * time.Second):
					return

				}
			}
			wg.Done()

		}()
	}

	wg.Wait()
	close(resQueue)
}

func (self *apnsPushService) resultCollector(psp *PushServiceProvider, resChan chan<- *apnsResult, c net.Conn) {
	for {
		var cmd uint8
		var status uint8
		var msgid uint32
		buf := make([]byte, 6)

		n, err := c.Read(buf)

		// The connection is closed by remote server. It could recover.
		if err == io.EOF {
			res := new(apnsResult)
			res.err = io.EOF
			resChan <- res
			return
		} else if err != nil {
			// Otherwise, it cannot recover.
			res := new(apnsResult)
			res.err = NewConnectionError(err)
			resChan<-res
			return
		}

		if n != 6 {
			continue
		}

		byteBuffer := bytes.NewBuffer(buf)

		err = binary.Read(byteBuffer, binary.BigEndian, &cmd)
		if err != nil {
			res := new(apnsResult)
			res.err = NewConnectionError(err)
			resChan<-res
			continue
		}

		err = binary.Read(byteBuffer, binary.BigEndian, &status)
		if err != nil {
			res := new(apnsResult)
			res.err = NewConnectionError(err)
			resChan<-res
			continue
		}

		err = binary.Read(byteBuffer, binary.BigEndian, &msgid)
		if err != nil {
			res := new(apnsResult)
			res.err = NewConnectionError(err)
			resChan<-res
			continue
		}

		res := new(apnsResult)
		res.msgId = msgid
		res.status = status
		resChan<-res
	}
}

func (self *apnsPushService) singlePush(psp *PushServiceProvider, dp *DeliveryPoint, notif *Notification, mid uint32, tlsconn net.Conn) error {
	devtoken := dp.FixedData["devtoken"]

	btoken, err := hex.DecodeString(devtoken)
	if err != nil {
		return NewBadDeliveryPointWithDetails(dp, err.Error())
	}

	bpayload, err := toAPNSPayload(notif)
	if err != nil {
		return NewBadNotificationWithDetails(err.Error())
	}

	buffer := bytes.NewBuffer([]byte{})

	// command
	binary.Write(buffer, binary.BigEndian, uint8(1))

	// transaction id
	binary.Write(buffer, binary.BigEndian, mid)

	// Expiry: default is one hour
	unixNow := uint32(time.Now().Unix())
	expiry := unixNow + 60*60
	if ttlstr, ok := notif.Data["ttl"]; ok {
		ttl, err := strconv.ParseUint(ttlstr, 10, 32)
		if err == nil {
			expiry = unixNow + uint32(ttl)
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
		return NewConnectionError(err)
	}

	return nil
}

func connectAPNS(psp *PushServiceProvider) (net.Conn, error) {
	cert, err := tls.LoadX509KeyPair(psp.FixedData["cert"], psp.FixedData["key"])
	if err != nil {
		return nil, NewBadPushServiceProviderWithDetails(psp, err.Error())
	}

	conf := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		//InsecureSkipVerify: true,
	}

	tlsconn, err := tls.Dial("tcp", psp.VolatileData["addr"], conf)
	if err != nil {
		return nil, NewConnectionError(err)
	}
	err = tlsconn.Handshake()
	if err != nil {
		return nil, NewConnectionError(err)
	}
	return tlsconn, nil
}

func (self *apnsPushService) pushMux() {
	connChan := make(map[string]chan *pushRequest, 10)
	for req := range self.reqChan {
		if req == nil {
			break
		}
		psp := req.psp
		var ch chan *pushRequest
		if ch, ok := connChan[psp.Name()]; !ok {
			ch = make(chan *pushRequest)
			connChan[psp.Name()] = ch
			go self.pushWorker(psp, ch)
		}
		ch <- req
	}
	for _, c := range connChan {
		close(c)
	}
}

func (self *apnsPushService) pushWorker(psp *PushServiceProvider, reqChan chan *pushRequest) {
	resChan := make(chan *apnsResult)

	reqIdMap := make(map[uint32]*pushRequest)

	var connErr error
	connErr = nil

	conn, err := connectAPNS(psp)
	if err != nil {
		connErr = err
	}

	go self.resultCollector(psp, resChan, conn)

	var nextid uint32

	nextid = 5

	for {
		select {
		case req := <-reqChan:
			// closed by another goroutine
			// close the connection and exit
			if req == nil {
				if connErr == nil {
					conn.Close()
				}
				return
			}

			dp := req.dp
			notif := req.notif
			mid := nextid
			nextid++

			if connErr != nil {
				result := new(PushResult)
				result.Content = notif
				result.Provider = psp
				result.Destination = dp
				result.MsgId = fmt.Sprintf("%v", mid)
				result.Err = connErr
				req.resChan<-result
				continue
			}

			reqIdMap[mid] = req
			err := self.singlePush(psp, dp, notif, mid, conn)

			if err != nil {
				result := new(PushResult)
				result.Content = notif
				result.Provider = psp
				result.Destination = dp
				result.MsgId = fmt.Sprintf("apns:%v-%v", psp.Name(), mid)
				result.Err = err
				req.resChan<-result
				delete(reqIdMap, mid)
			}

		case apnsres := <-resChan:
			// Connection Closed by remote server.
			// Recover it.
			if apnsres.err == io.EOF {
				conn.Close()
				conn, err = connectAPNS(psp)
				if err != nil {
					connErr = err
					continue
				}
				go self.resultCollector(psp, resChan, conn)
				continue
			}
			if cerr, ok := apnsres.err.(*ConnectionError); ok {
				connErr = cerr
			}

			if req, ok := reqIdMap[apnsres.msgId]; ok {
				result := new(PushResult)
				result.Content = req.notif
				result.Provider = psp
				result.Destination = req.dp
				result.MsgId = fmt.Sprintf("apns:%v-%v", psp.Name(), apnsres.msgId)
				if apnsres.err != nil {
					result := new(PushResult)
					result.Err = apnsres.err
					req.resChan<-result
					continue
				}

				switch apnsres.status {
				case 0:
					result.Err = nil
				case 1:
					result.Err = NewBadDeliveryPointWithDetails(req.dp, "Processing Error")
				case 2:
					result.Err = NewBadDeliveryPointWithDetails(req.dp, "Missing Device Token")
				case 3:
					result.Err = NewBadNotificationWithDetails("Missing topic")
				case 4:
					result.Err = NewBadNotificationWithDetails("Missing payload")
				case 5:
					result.Err = NewBadNotificationWithDetails("Invalid token size")
				case 6:
					result.Err = NewBadNotificationWithDetails("Invalid topic size")
				case 7:
					result.Err = NewBadNotificationWithDetails("Invalid payload size")
				case 8:
					result.Err = NewBadDeliveryPointWithDetails(req.dp, "Invalid Token")
				default:
					result.Err = fmt.Errorf("Unknown Error: %d", apnsres.status)
				}

				delete(reqIdMap, apnsres.msgId)
				go func() {
					select {
					case req.resChan<-result:
					case <-time.After((maxWaitTime + 1) * time.Second):
					}
					close(req.resChan)
				}()
			}
		}
	}
}

