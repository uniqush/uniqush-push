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
 *
 */

// TODO: Move this file into the module srv/apns/binary_api after this PR is merged.
// TODO: Change the implementation from connection pool to worker pool.
// The old connection pool usages were tightly coupled to the push service.

package apns

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/uniqush/cache"
	"github.com/uniqush/connpool"
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/binary_api"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

type pushWorkerInfo struct {
	psp *push.PushServiceProvider
	ch  chan *common.PushRequest
}

func (self *pushService) pushMux() {
	connMap := make(map[string]*pushWorkerInfo, 10)
	for req := range self.reqChan {
		if req == nil {
			break
		}
		psp := req.PSP
		worker, ok := connMap[psp.Name()]

		needAdd := false
		if !ok {
			needAdd = true
		} else {
			if !push.IsSamePSP(worker.psp, psp) {
				close(worker.ch)
				needAdd = true
			}
		}

		if needAdd {
			worker = &pushWorkerInfo{
				psp: psp,
				ch:  make(chan *common.PushRequest),
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

func (self *pushService) singlePush(payload, token []byte, expiry uint32, mid uint32, pool *connpool.Pool, errChan chan<- push.PushError) {
	conn, err := pool.Get()
	if err != nil {
		errChan <- push.NewConnectionError(err)
		return
	}
	// Total size for each notification:
	//
	// - command: 1
	// - identifier: 4
	// - expiry: 4
	// - device token length: 2
	// - device token: variable (100 max) - apple announced that we might have 100-byte device tokens in the future, in WWDC 2015. Previously 32.
	// - payload length: 2
	// - payload: variable (2048 max)
	//
	// In total, 2161 bytes (max)
	var dataBuffer [2180]byte

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
		errChan <- push.NewErrorf("error on connection with %v: %v. Will retry within %v", conn.RemoteAddr(), err, sleepTime)
		// Switch error channels after the first error - only return the first error to clients, log the remainder.
		errChan = self.errChan
		conn.Close()

		time.Sleep(sleepTime)
		// randomly wait more time
		sleepTime += time.Duration(rand.Int63n(int64(sleepTime)))
		// Let's try another connection to see if we can recover this error
		conn, err = pool.Get()

		if err != nil {
			// The pool may return nil for conn.
			if conn != nil {
				conn.Close()
			}
			errChan <- push.NewErrorf("We are unable to get a connection: %v", err)
			return
		}
		deadline := time.Now().Add(sleepTime)
		conn.SetWriteDeadline(deadline)
		err = writen(conn, pdu)
	}
	conn.SetWriteDeadline(time.Time{})
	conn.Close()
}

func (self *pushService) multiPush(req *common.PushRequest, pool *connpool.Pool) {
	defer close(req.ErrChan)
	if len(req.Payload) > maxPayLoadSize {
		req.ErrChan <- push.NewBadNotificationWithDetails("payload is too large")
		return
	}

	n := len(req.Devtokens)
	wg := new(sync.WaitGroup)
	wg.Add(n)

	for i, token := range req.Devtokens {
		mid := req.GetId(i)
		go func(mid uint32, token []byte) {
			self.singlePush(req.Payload, token, req.Expiry, mid, pool, req.ErrChan)
			wg.Done()
		}(mid, token)
	}
	wg.Wait()
}

func clearRequest(req *common.PushRequest, resChan chan *common.APNSResult) {
	time.Sleep(time.Duration(maxWaitTime+2) * time.Second)

	for i, _ := range req.Devtokens {
		res := new(common.APNSResult)
		res.MsgId = req.GetId(i)
		res.Status = uint8(0)
		res.Err = nil
		req.ResChan <- res
	}
}

func (self *pushService) pushWorker(psp *push.PushServiceProvider, reqChan chan *common.PushRequest) {
	resultChan := make(chan *common.APNSResult, 100)
	manager := binary_api.NewAPNSConnManager(psp, resultChan)
	pool := connpool.NewPool(maxNrConn, maxNrConn, manager)
	defer pool.Close()

	workerid := fmt.Sprintf("workder-%v-%v", time.Now().Unix(), rand.Int63())

	dpCache := cache.New(256, -1, 0*time.Second, nil)

	go binary_api.FeedbackChecker(psp, dpCache, self.errChan)

	// XXX use a tree structure would be faster and more stable.
	reqMap := make(map[uint32]*common.PushRequest, 1024)
	for {
		select {
		case req := <-reqChan:
			if req == nil {
				fmt.Printf("[%v][%v] I was told to stop (req == nil)\n", time.Now(), workerid)
				return
			}

			for i, _ := range req.Devtokens {
				mid := req.GetId(i)
				reqMap[mid] = req
			}

			for _, dp := range req.DPList {
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
			if req, ok := reqMap[res.MsgId]; ok {
				delete(reqMap, res.MsgId)
				req.ResChan <- res
			} else if res.Err != nil {
				self.errChan <- res.Err
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
