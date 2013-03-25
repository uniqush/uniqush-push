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
	"github.com/nu7hatch/gouuid"
	. "github.com/uniqush/log"
	. "github.com/uniqush/uniqush-push/db"
	. "github.com/uniqush/uniqush-push/push"
	"sync"
	"time"
)

type PushBackEnd struct {
	psm     *PushServiceManager
	db      PushDatabase
	loggers []Logger
	errChan chan error
}

func (self *PushBackEnd) Finalize() {
	self.db.FlushCache()
	close(self.errChan)
}

func NewPushBackEnd(psm *PushServiceManager, database PushDatabase, loggers []Logger) *PushBackEnd {
	ret := new(PushBackEnd)
	ret.psm = psm
	ret.db = database
	ret.loggers = loggers
	ret.errChan = make(chan error)
	go ret.processError()
	psm.SetErrorReportChan(ret.errChan)
	return ret
}

func (self *PushBackEnd) AddPushServiceProvider(service string, psp *PushServiceProvider) error {
	err := self.db.AddPushServiceProviderToService(service, psp)
	if err != nil {
		return err
	}
	return nil
}

func (self *PushBackEnd) RemovePushServiceProvider(service string, psp *PushServiceProvider) error {
	err := self.db.RemovePushServiceProviderFromService(service, psp)
	if err != nil {
		return err
	}
	return nil
}

func (self *PushBackEnd) Subscribe(service, sub string, dp *DeliveryPoint) (*PushServiceProvider, error) {
	psp, err := self.db.AddDeliveryPointToService(service, sub, dp)
	if err != nil {
		return nil, err
	}
	return psp, nil
}

func (self *PushBackEnd) Unsubscribe(service, sub string, dp *DeliveryPoint) error {
	err := self.db.RemoveDeliveryPointFromService(service, sub, dp)
	if err != nil {
		return err
	}
	return nil
}

func (self *PushBackEnd) processError() {
	for err := range self.errChan {
		rid, _ := uuid.NewV4()
		self.fixError(rid.String(), err, self.loggers[LOGGER_PUSH], 0*time.Second)
	}
}

func (self *PushBackEnd) fixError(reqId string, event error, logger Logger, after time.Duration) error {
	var service string
	var sub string
	var ok bool
	switch err := event.(type) {
	case *RetryError:
		if service, ok = err.Provider.FixedData["service"]; !ok {
			return nil
		}
		if sub, ok = err.Destination.FixedData["subscriber"]; !ok {
			return nil
		}
		if after <= 0*time.Second {
			after = 5 * time.Second
		}
		if after > 1*time.Minute {
			logger.Errorf("RequestID=%v Service=%v Subscriber=%v PushServiceProvider=%v DeliveryPoint=%v Failed after retry", reqId, service, sub, err.Provider.Name(), err.Destination.Name())
			return nil
		}
		logger.Infof("RequestID=%v Service=%v Subscriber=%v PushServiceProvider=%v DeliveryPoint=%v Retry after %v", reqId, service, sub, err.Provider.Name(), err.Destination.Name(), after)
		go func() {
			<-time.After(after)
			subs := make([]string, 1)
			subs[0] = sub
			after = 2 * after
			self.pushImpl(reqId, service, subs, err.Content, self.loggers[LOGGER_PUSH], err.Provider, err.Destination, after)
		}()
	case *PushServiceProviderUpdate:
		if service, ok = err.Provider.FixedData["service"]; !ok {
			return nil
		}
		psp := err.Provider
		e := self.db.ModifyPushServiceProvider(psp)
		if e != nil {
			logger.Errorf("RequestID=%v Service=%v PushServiceProvider=%v Update Failed: %v", reqId, service, psp.Name(), e)
		} else {
			logger.Infof("RequestID=%v Service=%v PushServiceProvider=%v Update Success", reqId, service, psp.Name())
		}
	case *DeliveryPointUpdate:
		if sub, ok = err.Destination.FixedData["subscriber"]; !ok {
			return nil
		}
		dp := err.Destination
		e := self.db.ModifyDeliveryPoint(dp)
		if e != nil {
			logger.Errorf("Subscriber=%v DeliveryPoint=%v Update Failed: %v", sub, dp.Name(), e)
		} else {
			logger.Infof("Service=%v Subscriber=%v DeliveryPoint=%v Update Success", service, sub, dp.Name())
		}
	case *UnsubscribeUpdate:
		if service, ok = err.Provider.FixedData["service"]; !ok {
			return nil
		}
		if sub, ok = err.Destination.FixedData["subscriber"]; !ok {
			return nil
		}
		dp := err.Destination
		e := self.Unsubscribe(service, sub, dp)
		if e != nil {
			logger.Errorf("Service=%v Subscriber=%v DeliveryPoint=%v Unsubscribe failed: %v", service, sub, dp.Name(), e)
		} else {
			logger.Infof("Service=%v Subscriber=%v DeliveryPoint=%v Unsubscribe success", service, sub, dp.Name())
		}
	default:
		return err
	}
	return nil
}

func (self *PushBackEnd) collectResult(reqId string, service string, resChan <-chan *PushResult, logger Logger, after time.Duration) {
	for res := range resChan {
		var sub string
		var ok bool
		if res.Provider != nil && res.Destination != nil {
			if sub, ok = res.Destination.FixedData["subscriber"]; !ok {
				logger.Errorf("RequestID=%v Subscriber=%v DeliveryPoint=%v Bad Delivery Point: No subscriber", reqId, sub, res.Destination.Name())
				continue
			}
		}
		if res.Err == nil {
			logger.Infof("RequestID=%v Service=%v Subscriber=%v PushServiceProvider=%v DeliveryPoint=%v MsgId=%v Success!", reqId, service, sub, res.Provider.Name(), res.Destination.Name(), res.MsgId)
			continue
		}
		err := self.fixError(reqId, res.Err, logger, after)
		if err != nil {
			pspName := "Unknown"
			dpName := "Unknown"
			if res.Provider != nil {
				pspName = res.Provider.Name()
			}
			if res.Destination != nil {
				dpName = res.Destination.Name()
			}
			logger.Infof("RequestID=%v Service=%v Subscriber=%v PushServiceProvider=%v DeliveryPoint=%v Failed: %v", reqId, service, sub, pspName, dpName, err)
		}
	}
}

func (self *PushBackEnd) Push(reqId string, service string, subs []string, notif *Notification, logger Logger) {
	self.pushImpl(reqId, service, subs, notif, logger, nil, nil, 0*time.Second)
}

func (self *PushBackEnd) pushImpl(reqId string, service string, subs []string, notif *Notification, logger Logger, provider *PushServiceProvider, dest *DeliveryPoint, after time.Duration) {
	dpChanMap := make(map[string]chan *DeliveryPoint)
	wg := new(sync.WaitGroup)
	for _, sub := range subs {
		var pspDpList []PushServiceProviderDeliveryPointPair
		if provider != nil && dest != nil {
			pspDpList := make([]PushServiceProviderDeliveryPointPair, 1)
			pspDpList[0].PushServiceProvider = provider
			pspDpList[0].DeliveryPoint = dest
		} else {
			var err error
			pspDpList, err = self.db.GetPushServiceProviderDeliveryPointPairs(service, sub)
			if err != nil {
				logger.Errorf("RequestID=%v Service=%v Subscriber=%v Failed: Database Error %v", reqId, service, sub, err)
				continue
			}
		}

		if len(pspDpList) == 0 {
			logger.Errorf("RequestID=%v Service=%v Subscriber=%v Failed: No device", reqId, service, sub)
			continue
		}

		for _, pair := range pspDpList {
			psp := pair.PushServiceProvider
			dp := pair.DeliveryPoint
			if psp == nil {
				logger.Errorf("RequestID=%v Service=%v Subscriber=%v Failed once: nil Push Service Provider", reqId, service, sub)
				continue
			}
			if dp == nil {
				logger.Errorf("RequestID=%v Service=%v Subscriber=%v Failed once: nil Delivery Point", reqId, service, sub)
				continue
			}
			var ch chan *DeliveryPoint
			var ok bool
			if ch, ok = dpChanMap[psp.Name()]; !ok {
				ch = make(chan *DeliveryPoint)
				dpChanMap[psp.Name()] = ch
				resChan := make(chan *PushResult)
				wg.Add(1)
				go func() {
					self.psm.Push(psp, ch, resChan, notif)
					wg.Done()
				}()
				wg.Add(1)
				go func() {
					self.collectResult(reqId, service, resChan, logger, after)
					wg.Done()
				}()
			}
			ch <- dp
		}
	}
	for _, dpch := range dpChanMap {
		close(dpch)
	}
	wg.Wait()
}
