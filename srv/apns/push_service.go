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
 * This implements version 2 of the Binary Provider API
 *
 * ## A note on ttl and expiry (Expiration date)
 *
 * From
 * https://developer.apple.com/library/content/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/BinaryProviderAPI.html#//apple_ref/doc/uid/TP40008194-CH13-SW1
 *
 * > A UNIX epoch date expressed in seconds (UTC) that identifies when the notification is no longer valid and can be discarded.
 * >
 * > If this value is non-zero, APNs stores the notification tries to deliver the notification at least once.
 * > Specify zero to indicate that the notification expires immediately and that APNs should not store the notification at all.
 */

package apns

import (
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	// There are two different protocols we use to connect to APNS: binary and HTTP2.
	// TODO: Make this configurable.
	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/binary_api"
	"github.com/uniqush/uniqush-push/srv/apns/common"
	"github.com/uniqush/uniqush-push/srv/apns/http_api"
)

const (
	maxPayLoadSize int = 2048
	maxNrConn      int = 13
)

type pushService struct {
	binaryRequestProcessor common.PushRequestProcessor
	httpRequestProcessor   common.PushRequestProcessor
	errChan                chan<- push.PushError
	nextMessageId          uint32
	checkPoint             time.Time
}

var _ push.PushServiceType = &pushService{}

func NewPushService() *pushService {
	return &pushService{
		binaryRequestProcessor: binary_api.NewRequestProcessor(maxNrConn),
		httpRequestProcessor:   http_api.NewRequestProcessor(),
		nextMessageId:          0,
	}
}

// getMessageIds is needed for the binary API of APNS.
func (self *pushService) getMessageIds(n int) uint32 {
	return atomic.AddUint32(&self.nextMessageId, uint32(n))
}

func (p *pushService) Name() string {
	return "apns"
}

func (p *pushService) Finalize() {
	p.binaryRequestProcessor.Finalize()
	p.httpRequestProcessor.Finalize()
}

func (self *pushService) SetErrorReportChan(errChan chan<- push.PushError) {
	self.errChan = errChan
	self.binaryRequestProcessor.SetErrorReportChan(errChan)
	self.httpRequestProcessor.SetErrorReportChan(errChan)
	return
}

func (p *pushService) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
	if service, ok := kv["service"]; ok {
		psp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}

	return p.buildBinaryPushServiceProviderFromMap(kv, psp)
}

func (p *pushService) buildBinaryPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
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

	// Put other things which can change in VolatileData.
	// E.g. a bundleid can be changed by the company which manages the app.
	if bundleid, ok := kv["bundleid"]; ok {
		psp.VolatileData["bundleid"] = bundleid
	} else {
		psp.VolatileData["bundleid"] = ""
	}
	if sandbox, ok := kv["sandbox"]; ok {
		if sandbox == "true" {
			psp.VolatileData["addr"] = "gateway.sandbox.push.apple.com:2195"
		}
	} else {
		if addr, ok := kv["addr"]; ok {
			psp.VolatileData["addr"] = addr
		} else {
			psp.VolatileData["addr"] = "gateway.push.apple.com:2195"
		}
	}
	return nil
}

func (p *pushService) BuildDeliveryPointFromMap(kv map[string]string, dp *push.DeliveryPoint) error {
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

func apnsresToError(apnsres *common.APNSResult, psp *push.PushServiceProvider, dp *push.DeliveryPoint) push.PushError {
	var err push.PushError
	switch apnsres.Status {
	case common.STATUS0_SUCCESS:
		err = nil
	case common.STATUS1_PROCESSING_ERROR:
		err = push.NewBadDeliveryPointWithDetails(dp, "Processing Error")
	case common.STATUS2_MISSING_DEVICE_TOKEN:
		err = push.NewBadDeliveryPointWithDetails(dp, "Missing Device Token")
	case common.STATUS3_MISSING_TOPIC:
		err = push.NewBadNotificationWithDetails("Missing topic")
	case common.STATUS4_MISSING_PAYLOAD:
		err = push.NewBadNotificationWithDetails("Missing payload")
	case common.STATUS5_INVALID_TOKEN_SIZE:
		err = push.NewBadNotificationWithDetails("Invalid token size")
	case common.STATUS6_INVALID_TOPIC_SIZE:
		err = push.NewBadNotificationWithDetails("Invalid topic size")
	case common.STATUS7_INVALID_PAYLOAD_SIZE:
		err = push.NewBadNotificationWithDetails("Invalid payload size")
	case common.STATUS8_UNSUBSCRIBE:
		// err = push.NewBadDeliveryPointWithDetails(req.dp, "Invalid Token")
		// This token is invalid, we should unsubscribe this device.
		err = push.NewUnsubscribeUpdate(psp, dp)
	default:
		err = push.NewErrorf("Unknown Error: %d", apnsres.Status)
	}
	return err
}

func (self *pushService) waitResults(psp *push.PushServiceProvider, dpList []*push.DeliveryPoint, lastId uint32, resChan chan *common.APNSResult) {
	k := 0
	n := len(dpList)
	if n == 0 {
		return
	}
	for res := range resChan {
		idx := res.MsgId - lastId + uint32(n)
		if idx >= uint32(len(dpList)) || idx < 0 {
			continue
		}
		dp := dpList[idx]
		err := apnsresToError(res, psp, dp)
		if unsub, ok := err.(*push.UnsubscribeUpdate); ok {
			self.errChan <- unsub
		}
		k++
		if k >= n {
			return
		}
	}
}

// Returns a JSON APNS payload, for a dummy device token
func (self *pushService) Preview(notif *push.Notification) ([]byte, push.PushError) {
	return toAPNSPayload(notif)
}

// Push will read all of the delivery points to send to from dpQueue and send responses on resQueue before closing the channel. If the notification data is invalid,
// it will send only one response.
func (self *pushService) Push(psp *push.PushServiceProvider, dpQueue <-chan *push.DeliveryPoint, resQueue chan<- *push.PushResult, notif *push.Notification) {
	defer close(resQueue)
	// Profiling
	// self.updateCheckPoint("")
	var err push.PushError
	req := new(common.PushRequest)
	req.PSP = psp
	req.Payload, err = toAPNSPayload(notif)

	var requestProcessor common.PushRequestProcessor
	if http2, ok := notif.Data["uniqush.http2"]; ok && http2 == "1" {
		requestProcessor = self.httpRequestProcessor
	} else {
		requestProcessor = self.binaryRequestProcessor
	}

	if err == nil && len(req.Payload) > requestProcessor.GetMaxPayloadSize() {
		err = push.NewBadNotificationWithDetails(fmt.Sprintf("payload is too large: %d > %d", len(req.Payload), requestProcessor.GetMaxPayloadSize()))
	}

	if err != nil {
		res := new(push.PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = push.NewErrorf("Failed to create push: %v", err)
		resQueue <- res
		for range dpQueue {
		}
		return
	}

	// By default, the notification expires in an hour if the ttl is omitted.
	// Uniqush users can send a ttl of 0 to send a notification that expires immediately.
	// Uniqush users can alternately choose a positive ttl in seconds, which will be converted to a timestamp.
	unixNow := uint32(time.Now().Unix())
	expiry := unixNow + 60*60
	if ttlstr, ok := notif.Data["ttl"]; ok {
		ttl, err := strconv.ParseUint(ttlstr, 10, 32)
		if err == nil {
			if ttl > 0 {
				// Expiry is the exact date and time when the notification
				// expires. It's not a "time to live".
				expiry = unixNow + uint32(ttl)
			} else {
				expiry = uint32(0)
			}
		}
	}
	req.Expiry = expiry
	req.Devtokens = make([][]byte, 0, 10)
	dpList := make([]*push.DeliveryPoint, 0, 10)

	for dp := range dpQueue {
		res := new(push.PushResult)
		res.Destination = dp
		res.Provider = psp
		res.Content = notif
		devtoken, ok := dp.FixedData["devtoken"]
		if !ok {
			res.Err = push.NewBadDeliveryPointWithDetails(dp, "NoDevtoken")
			resQueue <- res
			continue
		}
		btoken, err := hex.DecodeString(devtoken)
		if err != nil {
			res.Err = push.NewBadDeliveryPointWithDetails(dp, err.Error())
			resQueue <- res
			continue
		}

		req.Devtokens = append(req.Devtokens, btoken)
		dpList = append(dpList, dp)
	}

	n := len(req.Devtokens)
	lastId := self.getMessageIds(n)
	req.MaxMsgId = lastId
	req.DPList = dpList

	// We send this request object to be processed by pushMux goroutine, to send responses/errors back.
	errChan := make(chan push.PushError)
	resChan := make(chan *common.APNSResult, n)
	req.ErrChan = errChan
	req.ResChan = resChan

	requestProcessor.AddRequest(req)

	// errChan closed means the message(s) is/are sent successfully to the APNs.
	// However, we may have not yet receieved responses from APNS - those are sent on resChan
	for err = range errChan {
		res := new(push.PushResult)
		res.Provider = psp
		res.Content = notif
		if _, ok := err.(*push.ErrorReport); ok {
			res.Err = push.NewErrorf("Failed to send payload to APNS: %v", err)
		} else {
			res.Err = err
		}
		resQueue <- res
	}
	// Profiling
	// self.updateCheckPoint("sending the message takes")
	if err != nil {
		return
	}

	for i, dp := range dpList {
		if dp != nil {
			r := new(push.PushResult)
			r.Provider = psp
			r.Content = notif
			r.Destination = dp
			mid := req.GetId(i)
			r.MsgId = fmt.Sprintf("apns:%v-%v", psp.Name(), mid)
			r.Err = nil
			resQueue <- r
		}
	}

	// Wait for the unserialized responses from APNS asyncronously - these will not affect what we send our clients for this request, but will affect subsequent requests.
	// TODO: With HTTP/2, this can be refactored to become synchronous (not in this PR, not while binary provider is supported for a PSP). The map[string]T can be removed.
	go self.waitResults(psp, dpList, lastId, resChan)
}

func (self *pushService) updateCheckPoint(prefix string) {
	if len(prefix) > 0 {
		duration := time.Since(self.checkPoint)
		fmt.Printf("%v: %v\n", prefix, duration)
	}
	self.checkPoint = time.Now()
}
