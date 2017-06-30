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
 * See https://developer.apple.com/library/content/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/BinaryProviderAPI.html#//apple_ref/doc/uid/TP40008194-CH13-SW1
 */

// Package binary_api supports version 2 of the old APNS binary protocol (over an encrypted TCP socket)
package binary_api

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/uniqush/cache"
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

const (
	// in Seconds
	maxWaitTime int = 20
)

type pushWorkerGroupInfo struct {
	psp *push.PushServiceProvider
	ch  chan *common.PushRequest
}

type BinaryPushRequestProcessor struct {
	reqChan    chan *common.PushRequest
	errChan    chan<- push.PushError
	wgFinalize sync.WaitGroup
	poolSize   int
	reqLock    sync.RWMutex
	finished   bool

	// connManagerMaker is called to create a ConnManager for a given push.PushServiceProvider
	connManagerMaker func(psp *push.PushServiceProvider, resultChan chan<- *common.APNSResult) ConnManager
	// feedbackChecker is called once. It periodically connects to APNS's feedback servers and fetches unsubscribe updates.
	// TODO: Should I still call feedbackChecker in the HTTP/2 API?
	feedbackChecker func(psp *push.PushServiceProvider, dpCache *cache.Cache, errChan chan<- push.PushError)
}

var _ common.PushRequestProcessor = &BinaryPushRequestProcessor{}

func NewRequestProcessor(poolSize int) *BinaryPushRequestProcessor {
	ret := &BinaryPushRequestProcessor{
		reqChan: make(chan *common.PushRequest),
		// These two callbacks won't be changed, except for in tests.
		connManagerMaker: newAPNSConnManager,
		feedbackChecker:  feedbackChecker,
		poolSize:         poolSize,
		finished:         false,
	}
	ret.wgFinalize.Add(1)
	go func() {
		ret.pushMux()
		ret.wgFinalize.Done()
	}()
	return ret
}

func (self *BinaryPushRequestProcessor) Finalize() {
	self.reqLock.Lock()
	wasFinished := self.finished
	self.finished = true
	self.reqLock.Unlock()
	if wasFinished {
		fmt.Println("Finalize was called twice - this shouldn't happen")
	} else {
		close(self.reqChan)
	}
	self.wgFinalize.Wait()
}

func (self *BinaryPushRequestProcessor) GetMaxPayloadSize() int {
	return 2048
}

func (self *BinaryPushRequestProcessor) SetErrorReportChan(errChan chan<- push.PushError) {
	self.errChan = errChan
}

func (self *BinaryPushRequestProcessor) AddRequest(req *common.PushRequest) {
	self.reqLock.RLock()
	defer self.reqLock.RUnlock()
	if self.finished {
		go func() { // Asynchronously do this, sending on ErrChan would be a blocking operation.
			req.ErrChan <- push.NewError("Uniqush is shutting down")
			close(req.ErrChan)
		}()
		return
	}
	self.reqChan <- req
}

// pushMux processes requests from self.reqChan to send pushes, forwarding the requests to pushWorkerGroups it creates for them to send responses
func (self *BinaryPushRequestProcessor) pushMux() {
	connMap := make(map[string]*pushWorkerGroupInfo, 10)
	for req := range self.reqChan {
		if req == nil {
			break
		}
		psp := req.PSP
		workerGroup, ok := connMap[psp.Name()]

		needAdd := false
		if !ok {
			needAdd = true
		} else {
			if !push.IsSamePSP(workerGroup.psp, psp) {
				close(workerGroup.ch)
				needAdd = true
			}
		}

		if needAdd {
			workerGroup = &pushWorkerGroupInfo{
				psp: psp,
				ch:  make(chan *common.PushRequest),
			}
			connMap[psp.Name()] = workerGroup
			self.wgFinalize.Add(1)
			go func() {
				defer self.wgFinalize.Done()
				self.pushWorkerGroup(psp, workerGroup.ch)
			}()
		}

		if workerGroup != nil {
			workerGroup.ch <- req
		}
	}
	for _, workerGroup := range connMap {
		if workerGroup == nil || workerGroup.ch == nil {
			continue
		}
		close(workerGroup.ch)
	}
}

// generatePayload generates the bytes of a frame to send to APNS, for the Binary Provider API v2
func generatePayload(payload, token []byte, expiry uint32, mid uint32) []byte {
	// Total size for each notification:
	// https://developer.apple.com/library/content/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/BinaryProviderAPI.html#//apple_ref/doc/uid/TP40008194-CH13-SW1
	//
	// - command:               			1 (`2`)
	// - Frame length:                      4 (32-bit integer)
	// - Item 1 - device token: 			103 or 35 (3+100 or 3+32) apple announced that we might have 100-byte device tokens in the future, in WWDC 2015 (conflicts with above document). Previously 32.
	// - Item 2 - JSON payload: 			2051 (3+2048)
	// - Item 3 - notification identifier:  7 (3+4)
	// - Item 4 - expiry identifier:        7 (3+4)
	// - Item 5 - priority:                 4 (3+1)
	//
	// In total, 2175 bytes (max)
	var dataBuffer [2180]byte

	// transaction id
	// https://developer.apple.com/library/ios/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/Chapters/CommunicatingWIthAPS.html#//apple_ref/doc/uid/TP40008194-CH101-SW4
	// Figure 5-2: An arbitrary, opaque value that identifies this notification. This identifier is used for reporting errors to your server.

	// Format of a frame
	//
	// 1. (1 byte) item ID(1-5)
	// 2. (2 byte) item data length (n)
	// 3. (n bytes)

	// 5 frames, with 3 bytes of headers and the following data (The list id is the item ID, 1-5):
	// 1. Device token (32 bytes)
	// 2. JSON Payload (variable length, up to 2048 bytes)
	// 3. Notification identifier (4 bytes)
	// 4. Expiration date (4 bytes)
	// 5. Priority (1 byte)
	buffer := bytes.NewBuffer(dataBuffer[:0])
	// command is version 2,

	// Write the "Command" (2, to push with version 2 of the protocol)
	binary.Write(buffer, binary.BigEndian, uint8(2))
	// Write the "Frame length" (The size of the frame data, which is the remainder of the protocol)
	frameDataLength := uint32((3 + len(token)) + (3 + len(payload)) + (3 + 4) + (3 + 4) + (3 + 1))

	binary.Write(buffer, binary.BigEndian, frameDataLength)

	// Writes 3 bytes for the item header, with item id and 2 bytes of length
	writeItemHeader := func(id uint8, itemLength uint16) {
		buffer.WriteByte(id)
		binary.Write(buffer, binary.BigEndian, itemLength)
	}

	// Item 1. Device token
	writeItemHeader(1, uint16(len(token)))
	buffer.Write(token)

	// Item 2. JSON payload
	writeItemHeader(2, uint16(len(payload)))
	buffer.Write(payload)

	// Item 3. Notification identifier
	writeItemHeader(3, 4)
	binary.Write(buffer, binary.BigEndian, uint32(mid))

	// Item 4. Expiration date
	writeItemHeader(4, 4)
	binary.Write(buffer, binary.BigEndian, uint32(expiry))

	// Item 5. Priority

	// Previously, in protocol v1, there was implicitly a priority of 10
	// https://developer.apple.com/library/content/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/LegacyNotificationFormat.html#//apple_ref/doc/uid/TP40008194-CH14-SW1
	// > These formats do not include a priority; a priority of 10 is assumed.
	priority := uint8(10)
	writeItemHeader(5, 1)
	// TODO: If we specify a ttl of 0, assume we want to send it with highest priority. Otherwise... do something else, or allow the user to configure priority?
	buffer.WriteByte(priority)

	// End of frame
	return buffer.Bytes()
}

// singlePush sends bytes to APNS, retrying with different connections if it failed to write bytes.
func (self *BinaryPushRequestProcessor) singlePush(payload, token []byte, expiry uint32, mid uint32, workerPool *Pool, errChan chan<- push.PushError) {
	// Generate the v2 frame payload
	pdu := generatePayload(payload, token, expiry, mid)

	// Send the request(frame) to APNS. err is nil if bytes were successfully written.
	err := workerPool.Push(pdu)

	// Retry with lengthening randomized delays if there are errors sending bytes to APNS.
	sleepTime := time.Duration(maxWaitTime) * time.Second
	for nrRetries := 0; err != nil && nrRetries < 3; nrRetries++ {
		switch err := err.(type) {
		case *PermanentError:
			// This is misconfigured, e.g. we weren't able to establish a new connection. Give up.
			errChan <- push.NewError(err.Error())
			return
		case *TemporaryError:
			errChan <- push.NewErrorf("error on connection with %v: %v. Will retry within %v", err.Endpoint, err.Err, sleepTime)
			break
		default:
			errChan <- push.NewErrorf("unknown error on connection: %v. Will retry within %v", err, sleepTime)
			break
		}
		errChan = self.errChan

		time.Sleep(sleepTime)
		// randomly wait more time
		sleepTime += time.Duration(rand.Int63n(int64(sleepTime)))
		// Let's try another connection to see if we can recover this error
		err = workerPool.Push(pdu)
	}
}

// multiPush calls singlePush in parallel for each token type in Devtokens, and waits for each singlePush to complete.
func (self *BinaryPushRequestProcessor) multiPush(req *common.PushRequest, workerpool *Pool) {
	defer close(req.ErrChan)
	if len(req.Payload) > self.GetMaxPayloadSize() {
		req.ErrChan <- push.NewBadNotificationWithDetails("payload is too large")
		return
	}

	n := len(req.Devtokens)
	wg := new(sync.WaitGroup)
	wg.Add(n)

	for i, token := range req.Devtokens {
		mid := req.GetId(i)
		go func(mid uint32, token []byte) {
			self.singlePush(req.Payload, token, req.Expiry, mid, workerpool, req.ErrChan)
			wg.Done()
		}(mid, token)
	}
	wg.Wait()
}

// clearRequest cleans up data for timed out requests from the reqMap of a pushWorkerGroup goroutine.
func clearRequest(req *common.PushRequest, resChan chan<- *common.APNSResult) {
	time.Sleep(time.Duration(maxWaitTime+2) * time.Second)

	for i := range req.Devtokens {
		res := new(common.APNSResult)
		res.MsgId = req.GetId(i)
		// TODO: Should this instead indicate that the request timed out?
		res.Status = uint8(0)
		res.Err = nil
		// It seems as if this should be resChan instead of req.ResChan, so I changed this.
		resChan <- res
	}
}

// overrideFeedbackChecker overrides the function used to listen for unsubscriptions from the APNS feedback servers.
func (self *BinaryPushRequestProcessor) overrideFeedbackChecker(newFeedbackChecker func(*push.PushServiceProvider, *cache.Cache, chan<- push.PushError)) {
	self.feedbackChecker = newFeedbackChecker
}

// overrideAPNSConnManagerMaker overrides the function used to construct a ConnManager implementation.
// This should be used only for tests.
func (self *BinaryPushRequestProcessor) overrideAPNSConnManagerMaker(connManagerMaker func(*push.PushServiceProvider, chan<- *common.APNSResult) ConnManager) {
	self.connManagerMaker = connManagerMaker
}

// pushWorkerGroup receives pushRequests from reqChan, responding on the channels within that common.PushRequest
func (self *BinaryPushRequestProcessor) pushWorkerGroup(psp *push.PushServiceProvider, reqChan <-chan *common.PushRequest) {
	resultChan := make(chan *common.APNSResult, 100)
	// Create a connection manager to open connections.
	// This will create a goroutine listening on each connection it creates, to be sent to us on resultChan.
	manager := newLoggingConnManager(self.connManagerMaker(psp, (chan<- *common.APNSResult)(resultChan)), self.errChan)
	// There's a pool for each push endpoint.
	workerpool := NewPool(manager, self.poolSize, maxWaitTime)
	defer workerpool.Close()

	workerid := fmt.Sprintf("workder-%v-%v", time.Now().Unix(), rand.Int63())

	dpCache := cache.New(256, -1, 0*time.Second, nil)

	// In a background thread, connect to corresponding feedback server and listen for unsubscribe updates to send to that user.
	go self.feedbackChecker(psp, dpCache, self.errChan)

	// XXX use a tree structure would be faster and more stable.
	reqMap := make(map[uint32]*common.PushRequest, 1024)

	// Callback for handling responses - used both in processing and when shutting down.
	handleResponse := func(res *common.APNSResult) {
		// Process the first result that is received for each MsgId, forwarding it to the requester.
		if req, ok := reqMap[res.MsgId]; ok {
			delete(reqMap, res.MsgId)
			req.ResChan <- res
		} else if res.Err != nil {
			self.errChan <- res.Err
		}
	}
	for {
		select {
		case req := <-reqChan:
			// Accept requests from pushMux goroutine, sending pushes to APNS for each devtoken.
			// If a response is received from APNS for a devtoken, forward it on the channel for the corresponding request.
			// Otherwise, close it
			if req == nil {
				fmt.Printf("[%v][%v] I was told to stop (req == nil) - stopping\n", time.Now(), workerid)
				// Finish up any remaining requests to uniqush, forwarding the responses from APNS.
				// clearRequest should ensure that reqMap is eventually empty - this has also been tested under heavy load.
				for {
					if len(reqMap) == 0 {
						fmt.Printf("[%v][%v] I was told to stop - stopped\n", time.Now(), workerid)
						return
					}
					res, ok := <-resultChan
					if !ok || res == nil {
						fmt.Printf("[%v][%v] Some pending requests don't have any responses - shouldn't happen\n", time.Now(), workerid)
						return
					}
					handleResponse(res)
				}
			}

			for i := range req.Devtokens {
				mid := req.GetId(i)
				reqMap[mid] = req
			}

			for _, dp := range req.DPList {
				if key, ok := dp.FixedData["devtoken"]; ok {
					dpCache.Set(key, dp)
				}
			}
			go self.multiPush(req, workerpool)
			go clearRequest(req, resultChan)
		case res := <-resultChan:
			if res == nil {
				fmt.Printf("[%v][%v] I was told to stop (res == nil)\n", time.Now(), workerid)
				return
			}
			handleResponse(res)
		}
	}
}
