/*
 * Copyright 2011-2013 Nan Deng
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package binary_api

// This file contains a connection manager, which creates new connections to apns based on a config.
// The connections allow writing raw bytes to apns. The responses from Apple are parsed by a goroutine and added to a channel.

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"

	"github.com/uniqush/connpool"
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

type apnsConnManager struct {
	psp        *push.PushServiceProvider
	cert       tls.Certificate
	conf       *tls.Config
	err        error
	addr       string
	resultChan chan *common.APNSResult
}

var _ connpool.ConnManager = &apnsConnManager{}

func NewAPNSConnManager(psp *push.PushServiceProvider, resultChan chan *common.APNSResult) connpool.ConnManager {
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

func resultCollector(psp *push.PushServiceProvider, resChan chan<- *common.APNSResult, c net.Conn) {
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
			res := new(common.APNSResult)
			res.Err = push.NewInfof("Connection closed by APNS: %v", err)
			resChan <- res
			return
		}

		byteBuffer := bytes.NewBuffer(buf)

		err = binary.Read(byteBuffer, binary.BigEndian, &cmd)
		if err != nil {
			res := new(common.APNSResult)
			res.Err = push.NewErrorf("Failed to read cmd: %v", err)
			resChan <- res
			continue
		}

		err = binary.Read(byteBuffer, binary.BigEndian, &status)
		if err != nil {
			res := new(common.APNSResult)
			res.Err = push.NewErrorf("Failed to read status: %v", err)
			resChan <- res
			continue
		}

		err = binary.Read(byteBuffer, binary.BigEndian, &msgid)
		if err != nil {
			res := new(common.APNSResult)
			res.Err = push.NewErrorf("Failed to read msgid: %v", err)
			resChan <- res
			continue
		}

		res := new(common.APNSResult)
		res.MsgId = msgid
		res.Status = status
		resChan <- res
	}
}
