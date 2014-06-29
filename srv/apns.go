/*
 * Copyright 2011-2013 Nan Deng
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
	"io"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uniqush/cache"
	"github.com/uniqush/connpool"
	. "github.com/uniqush/uniqush-push/push"
)

const (
	maxPayLoadSize int = 256
	maxNrConn      int = 13

	// in Minutes
	feedbackCheckPeriod int = 10
	// in Seconds
	maxWaitTime int = 20
)

type pushRequest struct {
	psp       *PushServiceProvider
	devtokens [][]byte
	payload   []byte
	maxMsgId  uint32
	expiry    uint32

	dpList  []*DeliveryPoint
	errChan chan error
	resChan chan *apnsResult
}

func (self *pushRequest) getId(idx int) uint32 {
	if idx < 0 || idx >= len(self.devtokens) {
		return 0
	}
	startId := self.maxMsgId - uint32(len(self.devtokens))
	return startId + uint32(idx)
}

type apnsResult struct {
	msgId  uint32
	status uint8
	err    error
}

type apnsPushService struct {
	reqChan       chan *pushRequest
	errChan       chan<- error
	nextMessageId uint32
	checkPoint    time.Time
}

func InstallAPNS() {
	GetPushServiceManager().RegisterPushServiceType(newAPNSPushService())
}

func newAPNSPushService() *apnsPushService {
	ret := new(apnsPushService)
	ret.reqChan = make(chan *pushRequest)
	ret.nextMessageId = 1
	go ret.pushMux()
	return ret
}

func (p *apnsPushService) Name() string {
	return "apns"
}

func (p *apnsPushService) Finalize() {
	close(p.reqChan)
}

func (self *apnsPushService) SetErrorReportChan(errChan chan<- error) {
	self.errChan = errChan
	return
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
		_, err := hex.DecodeString(devtoken)
		if err != nil {
			return fmt.Errorf("Invalid delivery point: bad device token. %v", err)
		}
		dp.FixedData["devtoken"] = devtoken
	} else {
		return errors.New("NoDevToken")
	}
	return nil
}

func (self *apnsPushService) getMessageIds(n int) uint32 {
	return atomic.AddUint32(&self.nextMessageId, uint32(n))
}

func apnsresToError(apnsres *apnsResult, psp *PushServiceProvider, dp *DeliveryPoint) error {
	var err error
	switch apnsres.status {
	case 0:
		err = nil
	case 1:
		err = NewBadDeliveryPointWithDetails(dp, "Processing Error")
	case 2:
		err = NewBadDeliveryPointWithDetails(dp, "Missing Device Token")
	case 3:
		err = NewBadNotificationWithDetails("Missing topic")
	case 4:
		err = NewBadNotificationWithDetails("Missing payload")
	case 5:
		err = NewBadNotificationWithDetails("Invalid token size")
	case 6:
		err = NewBadNotificationWithDetails("Invalid topic size")
	case 7:
		err = NewBadNotificationWithDetails("Invalid payload size")
	case 8:
		// err = NewBadDeliveryPointWithDetails(req.dp, "Invalid Token")
		// This token is invalid, we should unsubscribe this device.
		err = NewUnsubscribeUpdate(psp, dp)
	default:
		err = fmt.Errorf("Unknown Error: %d", apnsres.status)
	}
	return err
}

func (self *apnsPushService) waitResults(psp *PushServiceProvider, dpList []*DeliveryPoint, lastId uint32, resChan chan *apnsResult) {
	k := 0
	n := len(dpList)
	if n == 0 {
		return
	}
	for res := range resChan {
		idx := res.msgId - lastId + uint32(n)
		if idx >= uint32(len(dpList)) || idx < 0 {
			continue
		}
		dp := dpList[idx]
		err := apnsresToError(res, psp, dp)
		if unsub, ok := err.(*UnsubscribeUpdate); ok {
			self.errChan <- unsub
		}
		k++
		if k >= n {
			return
		}
	}
}

func (self *apnsPushService) Push(psp *PushServiceProvider, dpQueue <-chan *DeliveryPoint, resQueue chan<- *PushResult, notif *Notification) {
	defer close(resQueue)
	// Profiling
	// self.updateCheckPoint("")
	var err error
	req := new(pushRequest)
	req.psp = psp
	req.payload, err = toAPNSPayload(notif)

	if err == nil && len(req.payload) > maxPayLoadSize {
		err = NewBadNotificationWithDetails("Invalid payload size")
	}

	if err != nil {
		res := new(PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = err
		resQueue <- res
		for _ = range dpQueue {
		}
		return
	}

	unixNow := uint32(time.Now().Unix())
	expiry := unixNow + 60*60
	if ttlstr, ok := notif.Data["ttl"]; ok {
		ttl, err := strconv.ParseUint(ttlstr, 10, 32)
		if err == nil {
			expiry = unixNow + uint32(ttl)
		}
	}
	req.expiry = expiry
	req.devtokens = make([][]byte, 0, 10)
	dpList := make([]*DeliveryPoint, 0, 10)

	for dp := range dpQueue {
		res := new(PushResult)
		res.Destination = dp
		res.Provider = psp
		res.Content = notif
		devtoken, ok := dp.FixedData["devtoken"]
		if !ok {
			res.Err = NewBadDeliveryPointWithDetails(dp, "NoDevtoken")
			resQueue <- res
			continue
		}
		btoken, err := hex.DecodeString(devtoken)
		if err != nil {
			res.Err = NewBadDeliveryPointWithDetails(dp, err.Error())
			resQueue <- res
			continue
		}

		req.devtokens = append(req.devtokens, btoken)
		dpList = append(dpList, dp)
	}

	n := len(req.devtokens)
	lastId := self.getMessageIds(n)
	req.maxMsgId = lastId
	req.dpList = dpList

	errChan := make(chan error)
	resChan := make(chan *apnsResult, n)
	req.errChan = errChan
	req.resChan = resChan

	self.reqChan <- req

	// errChan closed means the message(s) is/are sent
	// to the APNS.
	for err = range errChan {
		res := new(PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = err
		resQueue <- res
	}
	// Profiling
	// self.updateCheckPoint("sending the message takes")
	if err != nil {
		return
	}

	for i, dp := range dpList {
		if dp != nil {
			r := new(PushResult)
			r.Provider = psp
			r.Content = notif
			r.Destination = dp
			mid := req.getId(i)
			r.MsgId = fmt.Sprintf("apns:%v-%v", psp.Name(), mid)
			r.Err = nil
			resQueue <- r
		}
	}

	go self.waitResults(psp, dpList, lastId, resChan)
}

type pushWorkerInfo struct {
	psp *PushServiceProvider
	ch  chan *pushRequest
}

func samePsp(a *PushServiceProvider, b *PushServiceProvider) bool {
	if a.Name() == b.Name() {
		if len(a.VolatileData) != len(b.VolatileData) {
			return false
		}
		for k, v := range a.VolatileData {
			if b.VolatileData[k] != v {
				return false
			}
		}
		return true
	}
	return false
}

func (self *apnsPushService) pushMux() {
	connMap := make(map[string]*pushWorkerInfo, 10)
	for req := range self.reqChan {
		if req == nil {
			break
		}
		psp := req.psp
		worker, ok := connMap[psp.Name()]

		needAdd := false
		if !ok {
			needAdd = true
		} else {
			if !samePsp(worker.psp, psp) {
				close(worker.ch)
				needAdd = true
			}
		}

		if needAdd {
			worker = &pushWorkerInfo{
				psp: psp,
				ch:  make(chan *pushRequest),
			}
			connMap[psp.Name()] = worker
			go self.pushWorker(psp, worker.ch)
		}

		if worker != nil {
			worker.ch <- req
		}
	}
	for _, worker := range connMap {
		if worker == nil || worker.ch == nil {
			continue
		}
		close(worker.ch)
	}
}

type apnsConnManager struct {
	psp        *PushServiceProvider
	cert       tls.Certificate
	conf       *tls.Config
	err        error
	addr       string
	resultChan chan *apnsResult
}

func newAPNSConnManager(psp *PushServiceProvider, resultChan chan *apnsResult) *apnsConnManager {
	manager := new(apnsConnManager)
	manager.cert, manager.err = tls.LoadX509KeyPair(psp.FixedData["cert"], psp.FixedData["key"])
	if manager.err != nil {
		return manager
	}
	manager.conf = &tls.Config{
		Certificates:       []tls.Certificate{manager.cert},
		InsecureSkipVerify: false,
	}

	manager.addr = psp.VolatileData["addr"]
	if skip, ok := psp.VolatileData["skipverify"]; ok {
		if skip == "true" {
			manager.conf.InsecureSkipVerify = true
		}
	}
	manager.resultChan = resultChan
	return manager
}

func (self *apnsConnManager) NewConn() (net.Conn, error) {
	if self.err != nil {
		return nil, self.err
	}

	/*
		conn, err := net.DialTimeout("tcp", self.addr, time.Duration(maxWaitTime)*time.Second)
		if err != nil {
			return nil, err
		}

		if c, ok := conn.(*net.TCPConn); ok {
			c.SetKeepAlive(true)
		}
		tlsconn := tls.Client(conn, self.conf)
		err = tlsconn.Handshake()
		if err != nil {
			return nil, err
		}
	*/

	tlsconn, err := tls.Dial("tcp", self.addr, self.conf)
	if err != nil {
		return nil, err
	}
	go resultCollector(self.psp, self.resultChan, tlsconn)
	return tlsconn, nil
}

func (self *apnsConnManager) InitConn(conn net.Conn, n int) error {
	return nil
}

func (self *apnsPushService) singlePush(payload, token []byte, expiry uint32, mid uint32, pool *connpool.Pool, errChan chan<- error) {
	conn, err := pool.Get()
	if err != nil {
		errChan <- err
		return
	}
	// Total size for each notification:
	//
	// - command: 1
	// - identifier: 4
	// - expiry: 4
	// - device token length: 2
	// - device token: 32 (vary)
	// - payload length: 2
	// - payload: vary (256 max)
	//
	// In total, 301 bytes (max)
	var dataBuffer [2048]byte

	buffer := bytes.NewBuffer(dataBuffer[:0])
	// command
	binary.Write(buffer, binary.BigEndian, uint8(1))
	// transaction id
	binary.Write(buffer, binary.BigEndian, mid)

	// Expiry
	binary.Write(buffer, binary.BigEndian, expiry)

	// device token
	binary.Write(buffer, binary.BigEndian, uint16(len(token)))
	buffer.Write(token)

	// payload
	binary.Write(buffer, binary.BigEndian, uint16(len(payload)))
	buffer.Write(payload)

	pdu := buffer.Bytes()

	deadline := time.Now().Add(time.Duration(maxWaitTime) * time.Second)
	conn.SetWriteDeadline(deadline)

	err = writen(conn, pdu)

	sleepTime := time.Duration(maxWaitTime) * time.Second
	for nrRetries := 0; err != nil && nrRetries < 3; nrRetries++ {
		errChan <- fmt.Errorf("error on connection with %v: %v. Will retry within %v", conn.RemoteAddr(), err, sleepTime)
		errChan = self.errChan
		conn.Close()

		time.Sleep(sleepTime)
		// randomly wait more time
		sleepTime += time.Duration(rand.Int63n(int64(sleepTime)))
		// Let's try another connection to see if we can recover this error
		conn, err = pool.Get()

		if err != nil {
			errChan <- fmt.Errorf("We are unable to get a connection: %v", err)
			conn.Close()
			return
		}
		deadline := time.Now().Add(sleepTime)
		conn.SetWriteDeadline(deadline)
		err = writen(conn, pdu)
	}
	conn.SetWriteDeadline(time.Time{})
	conn.Close()
}

func (self *apnsPushService) multiPush(req *pushRequest, pool *connpool.Pool) {
	defer close(req.errChan)
	if len(req.payload) > maxPayLoadSize {
		req.errChan <- NewBadNotificationWithDetails("payload is too large")
		return
	}

	n := len(req.devtokens)
	wg := new(sync.WaitGroup)
	wg.Add(n)

	for i, token := range req.devtokens {
		mid := req.getId(i)
		go func(mid uint32, token []byte) {
			self.singlePush(req.payload, token, req.expiry, mid, pool, req.errChan)
			wg.Done()
		}(mid, token)
	}
	wg.Wait()
}

func clearRequest(req *pushRequest, resChan chan *apnsResult) {
	time.Sleep(time.Duration(maxWaitTime+2) * time.Second)

	for i, _ := range req.devtokens {
		res := new(apnsResult)
		res.msgId = req.getId(i)
		res.status = uint8(0)
		res.err = nil
		req.resChan <- res
	}
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

func receiveFeedback(psp *PushServiceProvider) []string {
	conn, err := connectFeedback(psp)
	if conn == nil || err != nil {
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

func processFeedback(psp *PushServiceProvider, dpCache *cache.Cache, errChan chan<- error) {
	keys := receiveFeedback(psp)
	for _, token := range keys {
		dpif := dpCache.Delete(token)
		if dpif == nil {
			continue
		}
		if dp, ok := dpif.(*DeliveryPoint); ok {
			err := NewUnsubscribeUpdate(psp, dp)
			errChan <- err
		}
	}
}

func feedbackChecker(psp *PushServiceProvider, dpCache *cache.Cache, errChan chan<- error) {
	for {
		time.Sleep(time.Duration(feedbackCheckPeriod) * time.Minute)
		processFeedback(psp, dpCache, errChan)
	}
}

func (self *apnsPushService) pushWorker(psp *PushServiceProvider, reqChan chan *pushRequest) {
	resultChan := make(chan *apnsResult, 100)
	manager := newAPNSConnManager(psp, resultChan)
	pool := connpool.NewPool(maxNrConn, maxNrConn, manager)
	defer pool.Close()

	workerid := fmt.Sprintf("workder-%v-%v", time.Now().Unix(), rand.Int63())

	dpCache := cache.New(256, -1, 0*time.Second, nil)

	go feedbackChecker(psp, dpCache, self.errChan)

	// XXX use a tree structure would be faster and more stable.
	reqMap := make(map[uint32]*pushRequest, 1024)
	for {
		select {
		case req := <-reqChan:
			if req == nil {
				fmt.Printf("[%v][%v] I was told to stop (req == nil)\n", time.Now(), workerid)
				return
			}

			for i, _ := range req.devtokens {
				mid := req.getId(i)
				reqMap[mid] = req
			}

			for _, dp := range req.dpList {
				if key, ok := dp.FixedData["devtoken"]; ok {
					dpCache.Set(key, dp)
				}
			}
			go self.multiPush(req, pool)
			go clearRequest(req, resultChan)
		case res := <-resultChan:
			if res == nil {
				fmt.Printf("[%v][%v] I was told to stop (res == nil)\n", time.Now(), workerid)
				return
			}
			if req, ok := reqMap[res.msgId]; ok {
				delete(reqMap, res.msgId)
				req.resChan <- res
			} else if res.err != nil {
				self.errChan <- res.err
			}
		}
	}
}

func writen(w io.Writer, buf []byte) error {
	n := len(buf)
	for n >= 0 {
		l, err := w.Write(buf)
		if err != nil {
			if nerr, ok := err.(net.Error); ok && nerr.Temporary() && !nerr.Timeout() {
				continue
			}
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
		case "content-available":
			aps["content-available"] = v
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

func (self *apnsPushService) updateCheckPoint(prefix string) {
	if len(prefix) > 0 {
		duration := time.Since(self.checkPoint)
		fmt.Printf("%v: %v\n", prefix, duration)
	}
	self.checkPoint = time.Now()
}

func resultCollector(psp *PushServiceProvider, resChan chan<- *apnsResult, c net.Conn) {
	defer c.Close()
	var bufData [6]byte
	for {
		var cmd uint8
		var status uint8
		var msgid uint32
		buf := bufData[:]

		for i, _ := range buf {
			buf[i] = 0
		}

		_, err := io.ReadFull(c, buf)
		if err != nil {
			if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
				continue
			}
			res := new(apnsResult)
			res.err = NewInfof("Connection closed by APNS: %v", err)
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
