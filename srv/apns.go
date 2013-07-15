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
	"errors"
	"github.com/uniqush/connpool"
	. "github.com/uniqush/uniqush-push/push"
	"io"
	"net"
	"time"
)

const (
	maxWaitTime         time.Duration = 7
	maxPayLoadSize      int           = 256
	maxNrConn           int           = 15
	feedbackCheckPeriod time.Duration = 600
)

type pushRequest struct {
	psp       *PushServiceProvider
	devtokens [][]byte
	payload   []byte
	msgIds    []uint32
	expiry    uint32

	errChan chan error
}

type apnsPushService struct {
	reqChan    chan *pushRequest
	errChan    chan<- error
	checkPoint time.Time
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
		dp.FixedData["devtoken"] = devtoken
	} else {
		return errors.New("NoDevToken")
	}
	return nil
}

func (self *apnsPushService) Push(psp *PushServiceProvider, dpQueue <-chan *DeliveryPoint, resQueue chan<- *PushResult, notif *Notification) {
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

type apnsConnManager struct {
	psp  *PushServiceProvider
	cert tls.Certificate
	conf *tls.Config
	err  error
	addr string
}

func newAPNSConnManager(psp *PushServiceProvider) *apnsConnManager {
	manager := new(apnsConnManager)
	manager.cert, manager.err = tls.LoadX509KeyPair(psp.FixedData["cert"], psp.FixedData["key"])
	if manager.err != nil {
		return manager
	}
	manager.conf = &tls.Config{
		Certificates:       []tls.Certificate{manager.cert},
		InsecureSkipVerify: false,
	}

	if skip, ok := psp.VolatileData["skipverify"]; ok {
		if skip == "true" {
			manager.conf.InsecureSkipVerify = true
		}
	}
	manager.addr = psp.VolatileData["addr"]
	return manager
}

func (self *apnsConnManager) NewConn() (conn net.Conn, err error) {
	if self.err != nil {
		return nil, err
	}
	tlsconn, err := tls.Dial("tcp", self.addr, self.conf)
	if err != nil {
		return nil, err
	}
	err = tlsconn.Handshake()
	if err != nil {
		return nil, err
	}
	return tlsconn, nil
}

func (self *apnsConnManager) InitConn(conn net.Conn) error {
	return nil
}

func (self *apnsPushService) multiPush(req *pushRequest, pool *connpool.Pool) {
	if len(req.payload) > maxPayLoadSize {
		req.errChan <- NewBadNotificationWithDetails("payload is too large")
		return
	}
	if len(req.msgIds) != len(req.devtokens) {
		req.errChan <- NewBadNotificationWithDetails("message ids cannot be matched")
		return
	}
	conn, err := pool.Get()
	if err != nil {
		req.errChan <- err
		return
	}
	defer conn.Close()
	defer close(req.errChan)

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

	payload := req.payload

	for i, token := range req.devtokens {
		buffer := bytes.NewBuffer(dataBuffer[:0])
		mid := req.msgIds[i]
		// command
		binary.Write(buffer, binary.BigEndian, uint8(1))
		// transaction id
		binary.Write(buffer, binary.BigEndian, mid)

		// Expiry
		binary.Write(buffer, binary.BigEndian, req.expiry)

		// device token
		binary.Write(buffer, binary.BigEndian, uint16(len(token)))
		buffer.Write(token)

		// payload
		binary.Write(buffer, binary.BigEndian, uint16(len(payload)))
		buffer.Write(payload)

		pdu := buffer.Bytes()
		err := writen(conn, pdu)
		if err != nil {
			req.errChan <- err
			return
		}
	}
}

func (self *apnsPushService) pushWorker(psp *PushServiceProvider, reqChan chan *pushRequest) {
	manager := newAPNSConnManager(psp)
	pool := connpool.NewPool(maxNrConn, maxNrConn, manager)
	for {
		select {
		case req := <-reqChan:
			go self.multiPush(req, pool)
		}
	}
}

func writen(w io.Writer, buf []byte) error {
	n := len(buf)
	for n >= 0 {
		l, err := w.Write(buf)
		if err != nil {
			if nerr, ok := err.(net.Error); ok && nerr.Temporary() {
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
