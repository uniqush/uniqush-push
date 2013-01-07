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
<<<<<<< HEAD
	"sync/atomic"
	"time"
)

type apnsPushService struct {
	nextid uint32
	conns  map[string]net.Conn
	pfp    PushFailureHandler
=======
	"sync"
	"time"
)

const (
	maxWaitTime time.Duration = 5
)

type pushRequest struct {
	psp     *PushServiceProvider
	dp      *DeliveryPoint
	notif   *Notification
	resChan chan<- *PushResult
	msgIdChan chan<- string
}

type apnsPushService struct {
	reqChan chan *pushRequest
>>>>>>> master
}

func InstallAPNS() {
	GetPushServiceManager().RegisterPushServiceType(newAPNSPushService())
}

func newAPNSPushService() *apnsPushService {
	ret := new(apnsPushService)
<<<<<<< HEAD
	ret.conns = make(map[string]net.Conn, 5)
=======
	ret.reqChan = make(chan *pushRequest)
	go ret.pushMux()
>>>>>>> master
	return ret
}

func (p *apnsPushService) Name() string {
	return "apns"
}

<<<<<<< HEAD
func (p *apnsPushService) SetAsyncFailureHandler(pfp PushFailureHandler) {
	p.pfp = pfp
}

func (p *apnsPushService) Finalize() {
	for _, c := range p.conns {
		c.Close()
	}
}

func (p *apnsPushService) waitError(id string,
	c net.Conn,
	psp *PushServiceProvider,
	dp *DeliveryPoint,
	n *Notification) {
	duration, err := time.ParseDuration("5s")
	if err != nil {
		return
	}
	deadline := time.Now().Add(duration)
	//c.SetReadTimeout(5E9)
	err = c.SetDeadline(deadline)
	if err != nil {
		return
	}
	readb := [6]byte{}
	nr, err := c.Read(readb[:])
	if err != nil {
		return
	}
	if nr > 0 {
		switch readb[1] {
		case 2:
			p.pfp.OnPushFail(p,
				id,
				NewInvalidDeliveryPointError(psp,
					dp,
					errors.New("Missing device token")))
		case 3:
			err := NewInvalidNotification(psp, dp, n, errors.New("Missing topic"))
			p.pfp.OnPushFail(p, id, err)
		case 4:
			err := NewInvalidNotification(psp, dp, n, errors.New("Missing payload"))
			p.pfp.OnPushFail(p, id, err)
		case 5:
			err := NewInvalidNotification(psp, dp, n, errors.New("Invalid token size"))
			p.pfp.OnPushFail(p, id, err)
		case 6:
			err := NewInvalidNotification(psp, dp, n, errors.New("Invalid topic size"))
			p.pfp.OnPushFail(p, id, err)
		case 7:
			err := NewInvalidNotification(psp, dp, n, errors.New("Invalid payload size"))
			p.pfp.OnPushFail(p, id, err)
		case 8:
			err := NewInvalidDeliveryPointError(psp, dp, errors.New("Invalid token"))
			p.pfp.OnPushFail(p, id, err)
		default:
			err := errors.New("Unknown Error")
			p.pfp.OnPushFail(p, id, err)
		}
	}
=======
func (p *apnsPushService) Finalize() {
	close(p.reqChan)
>>>>>>> master
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
<<<<<<< HEAD

=======
	if skip, ok := kv["skipverify"]; ok {
		if skip == "true" {
			psp.VolatileData["skipverify"] = "true"
		}
	}
>>>>>>> master
	if sandbox, ok := kv["sandbox"]; ok {
		if sandbox == "true" {
			psp.VolatileData["addr"] = "gateway.sandbox.push.apple.com:2195"
			return nil
		}
	}
<<<<<<< HEAD
=======
	if addr, ok := kv["addr"]; ok {
		psp.VolatileData["addr"] = addr
		return nil
	}
>>>>>>> master
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

func toAPNSPayload(n *Notification) ([]byte, error) {
	payload := make(map[string]interface{})
	aps := make(map[string]interface{})
	alert := make(map[string]interface{})
	for k, v := range n.Data {
		switch k {
		case "msg":
			alert["body"] = v
<<<<<<< HEAD
=======
		case "loc-key":
			alert[k] = v
		case "loc-args":
			alert[k] = v
>>>>>>> master
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
<<<<<<< HEAD
=======
		case "ttl":
			continue
>>>>>>> master
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

<<<<<<< HEAD
func (p *apnsPushService) getConn(psp *PushServiceProvider) (net.Conn, error) {
	name := psp.Name()
	if conn, ok := p.conns[name]; ok {
		return conn, nil
	}
	return p.reconnect(psp)
}

func (p *apnsPushService) reconnect(psp *PushServiceProvider) (net.Conn, error) {
	name := psp.Name()
	if conn, ok := p.conns[name]; ok {
		conn.Close()
	}
	cert, err := tls.LoadX509KeyPair(psp.FixedData["cert"], psp.FixedData["key"])
	if err != nil {
		return nil, NewInvalidPushServiceProviderError(psp, err)
	}
	conf := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
	}
	tlsconn, err := tls.Dial("tcp", psp.VolatileData["addr"], conf)
	if err != nil {
		return nil, NewConnectionError(psp, err, "DialErr")
	}
	err = tlsconn.Handshake()
	if err != nil {
		return nil, NewConnectionError(psp, err, "HandshakeErr")
	}
	p.conns[name] = tlsconn
	return tlsconn, nil
}

func (p *apnsPushService) Push(sp *PushServiceProvider,
	s *DeliveryPoint,
	n *Notification) (string, error) {
	devtoken := s.FixedData["devtoken"]
	btoken, err := hex.DecodeString(devtoken)
	if err != nil {
		return "", NewInvalidDeliveryPointError(sp, s, err)
	}

	bpayload, err := toAPNSPayload(n)
	if err != nil {
		return "", NewInvalidNotification(sp, s, n, err)
	}
	buffer := bytes.NewBuffer([]byte{})
=======
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
		resChan <- res
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

>>>>>>> master
	// command
	binary.Write(buffer, binary.BigEndian, uint8(1))

	// transaction id
<<<<<<< HEAD
	mid := atomic.AddUint32(&(p.nextid), 1)
	if smid, ok := n.Data["id"]; ok {
		imid, err := strconv.ParseUint(smid, 10, 0)
		if err == nil {
			mid = uint32(imid)
		}
	}
	binary.Write(buffer, binary.BigEndian, mid)

	// Expiry
	expiry := uint32(time.Now().Second() + 60*60)

	if sexpiry, ok := n.Data["expiry"]; ok {
		uiexp, err := strconv.ParseUint(sexpiry, 10, 0)
		if err == nil {
			expiry = uint32(uiexp)
=======
	binary.Write(buffer, binary.BigEndian, mid)

	// Expiry: default is one hour
	unixNow := uint32(time.Now().Unix())
	expiry := unixNow + 60*60
	if ttlstr, ok := notif.Data["ttl"]; ok {
		ttl, err := strconv.ParseUint(ttlstr, 10, 32)
		if err == nil {
			expiry = unixNow + uint32(ttl)
>>>>>>> master
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

<<<<<<< HEAD
	tlsconn, err := p.getConn(sp)
	if err != nil {
		return "", err
	}

	for i := 0; i < 2; i++ {
		err = writen(tlsconn, pdu[:1])
		if err != nil {
			tlsconn, err = p.reconnect(sp)
			if err != nil {
				return "", err
			}
			continue
		}
		err = writen(tlsconn, pdu[1:])
		if err != nil {
			tlsconn, err = p.reconnect(sp)
			if err != nil {
				return "", err
			}
			continue
		} else {
			break
		}
	}

	if err != nil {
		return "", err
	}

	id := fmt.Sprintf("apns:%s-%d", sp.Name(), mid)
	go p.waitError(id, tlsconn, sp, s, n)
	return id, nil
=======
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

	var connErr error
	connErr = nil

	connErrReported := false

	conn, err := connectAPNS(psp)
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

			req.msgIdChan <- messageId

			// Connection dropped by remote server.
			// Reconnect it.
			if connErr == tmpErrReconnect {
				conn, err = connectAPNS(psp)
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
					conn, err = connectAPNS(psp)
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
					result.Err = NewBadDeliveryPointWithDetails(req.dp, "Invalid Token")
				default:
					result.Err = fmt.Errorf("Unknown Error: %d", apnsres.status)
				}

				delete(reqIdMap, apnsres.msgId)
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
>>>>>>> master
}
