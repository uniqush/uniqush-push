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
	"fmt"
	"io"
	"net"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// ConnManager abstracts creating TLS sockets to send push payloads to APNS.
type ConnManager interface {
	// Used to create a new connection.
	// It will be called if a worker needs to open a different connection
	// or if a new connection is being created.
	// The uint32 will be set to non-zero if the connection is closed
	NewConn() (net.Conn, <-chan bool, error)
}

// loggingConnManager decorates a ConnManager and logs opened connections.
type loggingConnManager struct {
	manager ConnManager
	errChan chan<- push.PushError
}

var _ ConnManager = &loggingConnManager{}

func (self *loggingConnManager) NewConn() (conn net.Conn, closed <-chan bool, err error) {
	conn, closed, err = self.manager.NewConn()
	if conn != nil {
		self.errChan <- push.NewInfof("Connection to APNS opened: %v to %v", conn.LocalAddr(), conn.RemoteAddr())
	}
	return
}

func newLoggingConnManager(manager ConnManager, errChan chan<- push.PushError) *loggingConnManager {
	return &loggingConnManager{
		manager: manager,
		errChan: errChan,
	}
}

type connManagerImpl struct {
	psp        *push.PushServiceProvider
	cert       tls.Certificate
	conf       *tls.Config
	err        error
	addr       string
	resultChan chan<- *common.APNSResult
}

var _ ConnManager = &connManagerImpl{}

func newAPNSConnManager(psp *push.PushServiceProvider, resultChan chan<- *common.APNSResult) ConnManager {
	manager := new(connManagerImpl)
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

// NewConn returns either a connection and a channel to signal when the receiver detects a closed connection,
// or an error.
func (self *connManagerImpl) NewConn() (net.Conn, <-chan bool, error) {
	if self.err != nil {
		return nil, nil, fmt.Errorf("Error initializing apnsConnManager: %v", self.err)
	}

	tlsconn, err := tls.Dial("tcp", self.addr, self.conf)
	if err != nil {
		if err.Error() == "EOF" {
			err = fmt.Errorf("Certificate is probably invalid/expired: %v", err)
		}
		return nil, nil, err
	}
	closed := make(chan bool, 1) // notifies worker when reader detects socket closed. Has a buffer so adding one element is non-blocking
	go resultCollector(self.psp, self.resultChan, tlsconn, closed)
	return tlsconn, closed, nil
}

// resultCollector processes the 6-byte APNS responses for each of our push notifications.
// One resultCollector goroutine is automatically created for each connection established by NewConn (used by worker pools)
// Visible for testing.
func resultCollector(psp *push.PushServiceProvider, resChan chan<- *common.APNSResult, c net.Conn, closed chan<- bool) {
	defer func() {
		// Optimization: If listening socket notices that the channel is closed, notify the sending socket so that it can reopen it.
		closed <- true
		c.Close()
	}()
	var bufData [6]byte
	for {
		var cmd uint8
		var status uint8
		var msgid uint32
		buf := bufData[:]

		for i := range buf {
			buf[i] = 0
		}

		// Read 6 bytes of data from APNS response.
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

		// Create an common.APNSResult structure from those 6 bytes.
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
		// Send this response to the connection manager, which will translate it to a result/error structure associated with the Push,
		// and send that to the pushbackend.
		// Because APNS pushes don't wait for APNS to respond with bytes, this won't be part of the uniqush response, but will be used to update the DB.
		resChan <- res

		// A status code of 10 indicates that the APNs server closed the connection. Close this connection.
		// See https://developer.apple.com/library/ios/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/Appendixes/BinaryProviderAPI.html#//apple_ref/doc/uid/TP40008194-CH106-SW8 Table A-1
		if status == 10 {
			return
		}
	}
}
