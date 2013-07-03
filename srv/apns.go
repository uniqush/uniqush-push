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
	"github.com/uniqush/cache"
	. "github.com/uniqush/uniqush-push/push"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxWaitTime    time.Duration = 7
	maxPayLoadSize int           = 256
)

type pushRequest struct {
	psp       *PushServiceProvider
	dp        *DeliveryPoint
	notif     *Notification
	resChan   chan<- *PushResult
	msgIdChan chan<- string
}

type apnsPushService struct {
	reqChan chan *pushRequest
	errChan chan<- error
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

	_, err := tls.LoadX509KeyPair(psp.FixedData["cert"], psp.FixedData["key"])
	if err != nil {
		return err
	}

	if skip, ok := kv["skipverify"]; ok {
		if skip == "true" {
			psp.VolatileData["skipverify"] = "true"
		}
	}
	if sandbox, ok := kv["sandbox"]; ok {
		if sandbox == "true" {
			psp.VolatileData["addr"] = "gateway.sandbox.push.apple.com:2195"
			return nil
		}
	}
	if addr, ok := kv["addr"]; ok {
		psp.VolatileData["addr"] = addr
		return nil
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
		dp.FixedData["devtoken"] = devtoken
	} else {
		return errors.New("NoDevToken")
	}
	return nil
}

func parseList(str string) []string {
	ret := make([]string, 0, 10)
	elem := make([]rune, 0, len(str))
	escape := false
	for _, r := range str {
		if escape {
			escape = false
			elem = append(elem, r)
		} else if r == '\\' {
			escape = true
		} else if r == ',' {
			if len(elem) > 0 {
				ret = append(ret, string(elem))
			}
			elem = elem[:0]
		} else {
			elem = append(elem, r)
		}
	}
	if len(elem) > 0 {
		ret = append(ret, string(elem))
	}
	return ret
}

func toAPNSPayload(n *Notification) ([]byte, error) {
	payload := make(map[string]interface{})
	aps := make(map[string]interface{})
	alert := make(map[string]interface{})
	for k, v := range n.Data {
		switch k {
		case "msg":
			alert["body"] = v
		case "action-loc-key":
			alert[k] = v
		case "loc-key":
			alert[k] = v
		case "loc-args":
			alert[k] = parseList(v)
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
	if len(j) > maxPayLoadSize {
		return nil, NewBadNotificationWithDetails("payload is too large")
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
	msgId  uint32
	status uint8
	err    error
}

func (self *apnsPushService) Push(psp *PushServiceProvider, dpQueue <-chan *DeliveryPoint, resQueue chan<- *PushResult, notif *Notification) {
	wg := new(sync.WaitGroup)

	for dp := range dpQueue {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resultChannel := make(chan *PushResult, 1)
			msgIdChannel := make(chan string)
			req := new(pushRequest)
			req.dp = dp
			req.notif = notif
			req.resChan = resultChannel
			req.psp = psp
			req.msgIdChan = msgIdChannel

			self.reqChan <- req

			messageId := <-msgIdChannel

			n := 0
			for {
				select {
				case res := <-resultChannel:
					if res == nil {
						return
					}
					resQueue <- res
					n++
				case <-time.After(maxWaitTime * time.Second):
					// It should success according to APNS' document
					if n == 0 {
						res := new(PushResult)
						res.Provider = psp
						res.Destination = dp
						res.Content = notif
						res.MsgId = messageId
						res.Err = nil
						resQueue <- res
					}
					return
				}
			}

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

		n, err := io.ReadFull(c, buf)

		// The connection is closed by remote server. It could recover.
		if err == io.EOF {
			res := new(apnsResult)
			res.err = io.EOF
			resChan <- res
			return
		} else if err != nil || n != len(buf) {
			// Otherwise, it cannot recover.
			res := new(apnsResult)
			res.err = NewConnectionError(err)
			resChan <- res
			return
		}

		byteBuffer := bytes.NewBuffer(buf)

		err = binary.Read(byteBuffer, binary.BigEndian, &cmd)
		if err != nil {
			res := new(apnsResult)
			res.err = err
			resChan <- res
			continue
		}

		err = binary.Read(byteBuffer, binary.BigEndian, &status)
		if err != nil {
			res := new(apnsResult)
			res.err = err
			resChan <- res
			continue
		}

		err = binary.Read(byteBuffer, binary.BigEndian, &msgid)
		if err != nil {
			res := new(apnsResult)
			res.err = err
			resChan <- res
			continue
		}

		res := new(apnsResult)
		res.msgId = msgid
		res.status = status
		fmt.Printf("MsgId=%v, status=%v\n", msgid, status)
		resChan <- res
	}
}

func (self *apnsPushService) singlePush(psp *PushServiceProvider, dp *DeliveryPoint, notif *Notification, mid uint32, tlsconn net.Conn) error {
	devtoken, ok := dp.FixedData["devtoken"]

	if !ok {
		return NewBadDeliveryPointWithDetails(dp, "NoDevtoken")
	}

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

func connectFeedback(psp *PushServiceProvider) (net.Conn, error) {
	cert, err := tls.LoadX509KeyPair(psp.FixedData["cert"], psp.FixedData["key"])
	if err != nil {
		return nil, NewBadPushServiceProviderWithDetails(psp, err.Error())
	}

	conf := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: false,
	}

	if skip, ok := psp.VolatileData["skipverify"]; ok {
		if skip == "true" {
			conf.InsecureSkipVerify = true
		}
	}

	addr := "feedback.sandbox.push.apple.com:2196"
	if psp.VolatileData["addr"] == "gateway.push.apple.com:2195" {
		addr = "feedback.push.apple.com:2196"
	} else if psp.VolatileData["addr"] == "gateway.sandbox.push.apple.com:2195" {
		addr = "feedback.sandbox.push.apple.com:2196"
	} else {
		ae := strings.Split(psp.VolatileData["addr"], ":")
		addr = fmt.Sprintf("%v:2196", ae[0])
	}
	tlsconn, err := tls.Dial("tcp", addr, conf)
	if err != nil {
		return nil, err
	}
	err = tlsconn.Handshake()
	if err != nil {
		return nil, err
	}
	return tlsconn, nil
}

// connect the feedback service with exp back off retry
func (self *apnsPushService) connectFeedbackOrRetry(psp *PushServiceProvider) net.Conn {
	retryAfter := 1 * time.Minute
	for retryAfter <= 1*time.Hour {
		conn, err := connectFeedback(psp)
		if err == nil {
			return conn
		}
		<-time.After(retryAfter)
		retryAfter = retryAfter * 2
	}
	return nil
}

func (self *apnsPushService) feedbackReceiver(psp *PushServiceProvider) []string {
	conn := self.connectFeedbackOrRetry(psp)
	if conn == nil {
		return nil
	}
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()

	ret := make([]string, 0, 1)
	for {
		var unsubTime uint32
		var tokenLen uint16

		err := binary.Read(conn, binary.BigEndian, &unsubTime)
		if err != nil {
			return ret
		}
		err = binary.Read(conn, binary.BigEndian, &tokenLen)
		if err != nil {
			return ret
		}
		devtoken := make([]byte, int(tokenLen))

		n, err := io.ReadFull(conn, devtoken)
		if err != nil {
			return ret
		}
		if n != int(tokenLen) {
			return ret
		}

		devtokenstr := hex.EncodeToString(devtoken)
		devtokenstr = strings.ToLower(devtokenstr)
		ret = append(ret, devtokenstr)
	}
	return ret
}

func (self *apnsPushService) connectAPNS(psp *PushServiceProvider) (net.Conn, error) {
	cert, err := tls.LoadX509KeyPair(psp.FixedData["cert"], psp.FixedData["key"])
	if err != nil {
		return nil, NewBadPushServiceProviderWithDetails(psp, err.Error())
	}

	conf := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: false,
	}

	if skip, ok := psp.VolatileData["skipverify"]; ok {
		if skip == "true" {
			conf.InsecureSkipVerify = true
		}
	}

	tlsconn, err := tls.Dial("tcp", psp.VolatileData["addr"], conf)
	if err != nil {
		return nil, err
	}
	err = tlsconn.Handshake()
	if err != nil {
		return nil, err
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
		var ok bool
		if ch, ok = connChan[psp.Name()]; !ok {
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

	dpCache := cache.New(1024, -1, 0*time.Second, nil)
	//dropedDp := make([]string, 0, 1024)

	var connErr error
	connErr = nil

	connErrReported := false

	conn, err := self.connectAPNS(psp)
	if err != nil {
		connErr = err
	} else {
		go self.resultCollector(psp, resChan, conn)
	}

	var nextid uint32
	nextid = 5

	tmpErrReconnect := fmt.Errorf("Reconnect")

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
			messageId := fmt.Sprintf("apns:%v-%v", psp.Name(), mid)

			if devtoken, ok := dp.FixedData["devtoken"]; ok {
				dpCache.Set(devtoken, dp)
			}

			req.msgIdChan <- messageId

			// Connection dropped by remote server.
			// Reconnect it.
			if connErr == tmpErrReconnect {
				conn, err = self.connectAPNS(psp)
				if err != nil {
					connErr = err
				} else {
					connErr = nil
					go self.resultCollector(psp, resChan, conn)
				}
			}

			if connErr != nil {
				if connErrReported {
					// we have already reported the connection error. start again.
					if conn != nil {
						conn.Close()
					}

					// try to recover by re-connecting the server
					conn, err = self.connectAPNS(psp)
					if err != nil {
						// we failed again.
						connErr = err
						result := new(PushResult)
						result.Content = notif
						result.Provider = psp
						result.Destination = dp
						result.MsgId = messageId
						result.Err = connErr
						req.resChan <- result
						close(req.resChan)
						connErrReported = true
						continue
					} else {
						go self.resultCollector(psp, resChan, conn)
					}
				} else {
					// report this error
					connErrReported = true
					result := new(PushResult)
					result.Content = notif
					result.Provider = psp
					result.Destination = dp
					result.MsgId = messageId
					result.Err = connErr
					req.resChan <- result
					close(req.resChan)
					continue
				}
			}

			err := self.singlePush(psp, dp, notif, mid, conn)

			if err != nil {
				// encountered some difficulty on sending the data.
				result := new(PushResult)
				result.Content = notif
				result.Provider = psp
				result.Destination = dp
				result.MsgId = messageId
				result.Err = err
				req.resChan <- result
				close(req.resChan)
			} else {
				// wait the result from APNs
				reqIdMap[mid] = req

				// If there is no response from APNS,
				// then we have to have a way to clear the request
				// in reqIdMap.
				go func() {
					time.Sleep(time.Duration(maxWaitTime-1) * time.Second)
					apnsres := new(apnsResult)
					apnsres.err = nil
					apnsres.msgId = mid

					// On time out, consider it as success.
					apnsres.status = 0
					resChan <- apnsres
				}()
			}
			unsubed := self.feedbackReceiver(psp)
			for _, unsubDev := range unsubed {
				dpif := dpCache.Delete(unsubDev)
				if dpif == nil {
					continue
				}
				dp, ok := dpif.(*DeliveryPoint)
				if !ok {
					continue
				}
				err := NewUnsubscribeUpdate(psp, dp)
				self.errChan <- err
			}

		case apnsres := <-resChan:
			// Connection Closed by remote server.
			// Recover it.
			if apnsres.err == io.EOF {
				conn.Close()

				// Notify all waiting goroutines
				for msgid, req := range reqIdMap {
					result := new(PushResult)
					result.Content = req.notif
					result.Provider = psp
					result.Destination = req.dp
					result.MsgId = fmt.Sprintf("apns:%v-%v", psp.Name(), msgid)
					result.Err = fmt.Errorf("Connection Closed by Remote Server")
					req.resChan <- result
					close(req.resChan)
				}
				reqIdMap = make(map[uint32]*pushRequest)
				connErr = tmpErrReconnect
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
				delete(reqIdMap, apnsres.msgId)
				if apnsres.err != nil {
					result.Err = apnsres.err
					req.resChan <- result
					close(req.resChan)
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
					// result.Err = NewBadDeliveryPointWithDetails(req.dp, "Invalid Token")
					// This token is invalid, we should unsubscribe this device.
					result.Err = NewUnsubscribeUpdate(psp, req.dp)
				default:
					result.Err = fmt.Errorf("Unknown Error: %d", apnsres.status)
				}

				go func() {
					select {
					case req.resChan <- result:
					case <-time.After((maxWaitTime + 1) * time.Second):
					}
					close(req.resChan)
				}()
			}
		}
	}
}

func (self *apnsPushService) SetErrorReportChan(errChan chan<- error) {
	self.errChan = errChan
	return
}
