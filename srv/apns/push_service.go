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

// It's just here as an intermediate step, to rearrange the code without introduce any new code.
// The old code is tightly coupled with the connection pool usage, and will be switched to a worker pool.

package apns

import (
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/apns/common"
)

const (
	maxPayLoadSize int = 2048
	maxNrConn      int = 13

	// in Seconds
	maxWaitTime int = 20
)

type pushService struct {
	reqChan       chan *common.PushRequest
	errChan       chan<- push.PushError
	nextMessageId uint32
	checkPoint    time.Time
}

var _ push.PushServiceType = &pushService{}

func (p *pushService) Name() string {
	return "apns"
}

func (p *pushService) Finalize() {
	close(p.reqChan)
}

func (self *pushService) SetErrorReportChan(errChan chan<- push.PushError) {
	self.errChan = errChan
	return
}

func (p *pushService) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
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

func NewPushService() *pushService {
	ret := new(pushService)
	ret.reqChan = make(chan *common.PushRequest)
	ret.nextMessageId = 1
	go ret.pushMux()
	return ret
}

func (self *pushService) getMessageIds(n int) uint32 {
	return atomic.AddUint32(&self.nextMessageId, uint32(n))
}

func apnsresToError(apnsres *common.APNSResult, psp *push.PushServiceProvider, dp *push.DeliveryPoint) push.PushError {
	var err push.PushError
	switch apnsres.Status {
	case 0:
		err = nil
	case 1:
		err = push.NewBadDeliveryPointWithDetails(dp, "Processing Error")
	case 2:
		err = push.NewBadDeliveryPointWithDetails(dp, "Missing Device Token")
	case 3:
		err = push.NewBadNotificationWithDetails("Missing topic")
	case 4:
		err = push.NewBadNotificationWithDetails("Missing payload")
	case 5:
		err = push.NewBadNotificationWithDetails("Invalid token size")
	case 6:
		err = push.NewBadNotificationWithDetails("Invalid topic size")
	case 7:
		err = push.NewBadNotificationWithDetails("Invalid payload size")
	case 8:
		// err = NewBadDeliveryPointWithDetails(req.dp, "Invalid Token")
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

func (self *pushService) Push(psp *push.PushServiceProvider, dpQueue <-chan *push.DeliveryPoint, resQueue chan<- *push.PushResult, notif *push.Notification) {
	defer close(resQueue)
	// Profiling
	// self.updateCheckPoint("")
	var err push.PushError
	req := new(common.PushRequest)
	req.PSP = psp
	req.Payload, err = toAPNSPayload(notif)

	if err == nil && len(req.Payload) > maxPayLoadSize {
		err = push.NewBadNotificationWithDetails("Invalid payload size")
	}

	if err != nil {
		res := new(push.PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = push.NewErrorf("Failed to create push: %v", err)
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

	self.reqChan <- req

	// errChan closed means the message(s) is/are sent
	// to the APNS.
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

	go self.waitResults(psp, dpList, lastId, resChan)
}

func (self *pushService) updateCheckPoint(prefix string) {
	if len(prefix) > 0 {
		duration := time.Since(self.checkPoint)
		fmt.Printf("%v: %v\n", prefix, duration)
	}
	self.checkPoint = time.Now()
}
