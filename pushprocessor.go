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

package main

import (
	. "github.com/uniqush/log"
	. "github.com/uniqush/uniqush-push/db"
	. "github.com/uniqush/uniqush-push/push"
	"sync"
	"time"
)

type PushProcessor struct {
	logSetter
	databaseSetter
	max_nr_gorountines int
	max_nr_retry       int
	backendch          chan<- *Request
	psm                *PushServiceManager
}

const (
	init_backoff_time = 5
)

func NewPushProcessor(logger *Logger, dbfront PushDatabase, backendch chan<- *Request, psm *PushServiceManager) RequestProcessor {
	ret := new(PushProcessor)
	ret.SetLogger(logger)
	ret.SetDatabase(dbfront)
	ret.max_nr_gorountines = 1024
	ret.max_nr_retry = 3
	ret.backendch = backendch

	return ret
}

func (p *PushProcessor) retryRequest(req *Request, retryAfter time.Duration, subscriber string, psp *PushServiceProvider, dp *DeliveryPoint) {
	if req.nrRetries >= p.max_nr_retry {
		return
	}
	newreq := new(Request)
	newreq.nrRetries = req.nrRetries + 1
	newreq.PreviousTry = req
	newreq.ID = req.ID
	newreq.Action = ACTION_PUSH
	newreq.PushServiceProvider = psp
	newreq.DeliveryPoint = dp
	newreq.RequestSenderAddr = req.RequestSenderAddr
	newreq.Notification = req.Notification

	newreq.Service = req.Service
	newreq.Subscribers = make([]string, 1)
	newreq.Subscribers[0] = subscriber
	newreq.PunchTimestamp()

	if req.nrRetries == 0 || req.backoffTime == 0 {
		newreq.backoffTime = init_backoff_time
	} else {
		newreq.backoffTime = req.backoffTime << 1
	}

	waitTime := newreq.backoffTime
	if retryAfter > 0*time.Second {
		waitTime = int64(retryAfter.Seconds())
	}

	duration := time.Duration(time.Duration(waitTime) * time.Second)
	time.Sleep(duration)
	p.backendch <- newreq
}

func (p *PushProcessor) unsubscribe(req *Request, subscriber string, dp *DeliveryPoint) {
	a := new(Request)
	a.PunchTimestamp()
	a.ID = req.ID
	a.Action = ACTION_UNSUBSCRIBE
	a.RequestSenderAddr = req.RequestSenderAddr
	a.Service = req.Service
	a.Subscribers = make([]string, 1)
	a.Subscribers[0] = subscriber
	a.DeliveryPoint = dp
	p.backendch <- a
}

func (self *PushProcessor) updatePushServiceProvider(req *Request, psp *PushServiceProvider) error {
	if psp == nil {
		return nil
	}
	self.logger.Infof("[%s][UpdatePushServiceProvider] Service=%s, PushServiceProvider=%s",
		psp.PushServiceName(), req.Service, psp.Name())
	return self.dbfront.ModifyPushServiceProvider(psp)
}

func (self *PushProcessor) updateDeliveryPoint(req *Request, dp *DeliveryPoint) error {
	if dp == nil {
		return nil
	}
	self.logger.Infof("[%s][UpdateDeliveryPoint] Service=%s, DeliveryPoint=%s",
		dp.PushServiceName(), req.Service, dp.Name())
	return self.dbfront.ModifyDeliveryPoint(dp)
}

func recycle(psp *PushServiceProvider,
	dp *DeliveryPoint,
	n *Notification) {
	// TODO recycle only when they are not in cache!
	if psp != nil {
		psp.Recycle()
	}
	if dp != nil {
		dp.Recycle()
	}
	if n != nil {
		n.Recycle()
	}
}

func (p *PushProcessor) pushFail(req *Request, subscriber string, psp *PushServiceProvider, dp *DeliveryPoint, err error) {

	pspName := "All"
	dpName := "All"
	pspSrv := "UnknownService"

	if psp != nil {
		pspName = psp.Name()
		pspSrv = psp.PushServiceName()
	}
	if dp != nil {
		dpName = dp.Name()
	}

	p.logger.Errorf("[%s][PushFail] RequestId=%s Service=%s Subscriber=%s PushServiceProvider=%s DeliveryPoint=%s \"%v\"",
		pspSrv, req.ID, req.Service, subscriber, pspName, dpName, err)
	p.logger.Debugf("[%s][PushFailDebug] RequestId=%s Service=%s Subscriber=%s PushServiceProvider=\"%s\" DeliveryPoint=\"%s\" Notification=\"%v\" \"%v\"",
		pspSrv, req.ID, req.Service, subscriber, pspName, dpName, req.Notification, err)
	recycle(psp, dp, req.Notification)
}

func (p *PushProcessor) pushRetry(req *Request, subscriber string, psp *PushServiceProvider, dp *DeliveryPoint, err *RetryError) {
	go p.retryRequest(req, err.After, subscriber, psp, dp)
	p.logger.Warnf("[%s][PushRetry] RequestId=%s Service=%s Subscriber=%s PushServiceProvider=%s DeliveryPoint=%s \"%v\"",
		psp.PushServiceName(), req.ID, req.Service, subscriber,
		psp.Name(), dp.Name(), err)
}

func (p *PushProcessor) pushSucc(req *Request, subscriber string, psp *PushServiceProvider, dp *DeliveryPoint, id string) {
	p.logger.Infof("[%s][PushSuccess] RequestId=%s Service=%s Subscriber=%s PushServiceProvider=%s DeliveryPoint=%s MsgId=%s",
		psp.PushServiceName(), req.ID, req.Service, subscriber,
		psp.Name(), dp.Name(), id)
	recycle(psp, dp, req.Notification)
}

func (self *PushProcessor) processResult(req *Request, resChan chan *PushResult, pairSubMap map[string]string) {
	for res := range resChan {
		sub := "All"
		if res.Provider != nil && res.Destination != nil {
			if s, ok := res.Destination.VolatileData["subscriber"]; ok {
				sub = s
			} else if s, ok := pairSubMap[res.Provider.Name()+"::"+res.Destination.Name()]; ok {
				sub = s
			}
		}
		if res.Err == nil {
			self.pushSucc(req, sub, res.Provider, res.Destination, res.MsgId)
			continue
		}
		switch err := res.Err.(type) {
		case *RetryError:
			self.pushRetry(req, sub, res.Provider, res.Destination, err)
		case *PushServiceProviderUpdate:
			self.updatePushServiceProvider(req, err.Provider)
		case *DeliveryPointUpdate:
			self.updateDeliveryPoint(req, err.Destination)
		case *UnsubscribeUpdate:
			self.unsubscribe(req, sub, err.Destination)
		default:
			self.pushFail(req, sub, res.Provider, res.Destination, err)
		}
	}
}

// NOTE: We won't wait retries.
func (p *PushProcessor) Process(req *Request) {
	defer req.Finish()

	resChan := make(chan *PushResult)
	// TODO we should remove this variable one day.
	pairSubMap := make(map[string]string, len(req.Subscribers))
	wg := new(sync.WaitGroup)

	if len(req.Subscribers) == 1 && req.PushServiceProvider != nil && req.DeliveryPoint != nil {
		psp := req.PushServiceProvider
		dp := req.DeliveryPoint
		notif := req.Notification
		ch := make(chan *DeliveryPoint)
		sub := req.Subscribers[0]
		dp.VolatileData["subscriber"] = sub

		pairSubMap[psp.Name()+"::"+dp.Name()] = sub
		wg.Add(1)
		go func() {
			p.psm.Push(psp, ch, resChan, notif)
			wg.Done()
		}()
		ch <- dp
		close(ch)

		wg.Wait()
		p.processResult(req, resChan, pairSubMap)
		return
	}

	dpChanMap := make(map[string]chan *DeliveryPoint)

	wg.Add(1)
	go func() {
		p.processResult(req, resChan, pairSubMap)
		wg.Done()
	}()
	for _, sub := range req.Subscribers {
		pspDpList, err := p.dbfront.GetPushServiceProviderDeliveryPointPairs(req.Service, sub)

		if err != nil {
			p.logger.Errorf("[PushFail] Service=%s Subscriber=%s DatabaseError %v", req.Service, sub, err)
			req.Respond(err)
		}

		for _, pair := range pspDpList {
			psp := pair.PushServiceProvider
			dp := pair.DeliveryPoint
			notif := req.Notification
			pairSubMap[psp.Name()+"::"+dp.Name()] = sub
			// XXX this is ugly and dirty. But what can we do?
			dp.VolatileData["subscriber"] = sub
			if ch, ok := dpChanMap[psp.Name()]; ok {
				ch <- dp
			} else {
				ch := make(chan *DeliveryPoint)
				dpChanMap[psp.Name()] = ch
				wg.Add(1)
				go func() {
					p.psm.Push(psp, ch, resChan, notif)
					wg.Done()
				}()
				ch <- dp
			}
		}
	}
	for _, dpch := range dpChanMap {
		close(dpch)
	}
	wg.Wait()
}
