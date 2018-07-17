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
	"errors"
	"sync"
	"time"

	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/db"
	"github.com/uniqush/uniqush-push/push"
)

// PushBackEnd contains the data structures associated with sending pushes, managing subscriptions, and logging the results.
type PushBackEnd struct {
	psm     *push.PushServiceManager
	db      db.PushDatabase
	loggers []log.Logger
	errChan chan push.Error
}

// Finalize will save all subscriptions (and perform other cleanup) as part of the push service shutting down.
func (backend *PushBackEnd) Finalize() {
	// TODO: Add an option to prevent calling SAVE in implementations such as redis.
	// Users may want this if saving is time-consuming or already configured to happen periodically.
	backend.db.FlushCache()
	close(backend.errChan)
	backend.psm.Finalize()
}

// NewPushBackEnd creates and sets up the only instance of the push implementation.
func NewPushBackEnd(psm *push.PushServiceManager, database db.PushDatabase, loggers []log.Logger) *PushBackEnd {
	ret := new(PushBackEnd)
	ret.psm = psm
	ret.db = database
	ret.loggers = loggers
	ret.errChan = make(chan push.Error)
	go ret.processError()
	psm.SetErrorReportChan(ret.errChan)
	return ret
}

// AddPushServiceProvider is used by /addpsp to add a push service provider (for a service+push type) to the database.
func (backend *PushBackEnd) AddPushServiceProvider(service string, psp *push.PushServiceProvider) error {
	return backend.db.AddPushServiceProviderToService(service, psp)
}

// RemovePushServiceProvider is used by /rmpsp to remove a push service provider (for a service+push type) from the database.
func (backend *PushBackEnd) RemovePushServiceProvider(service string, psp *push.PushServiceProvider) error {
	return backend.db.RemovePushServiceProviderFromService(service, psp)
}

func (backend *PushBackEnd) GetPushServiceProviderConfigs() ([]*push.PushServiceProvider, error) {
	return backend.db.GetPushServiceProviderConfigs()
}

func (backend *PushBackEnd) Subscribe(service, sub string, dp *push.DeliveryPoint) (*push.PushServiceProvider, error) {
	return backend.db.AddDeliveryPointToService(service, sub, dp)
}

func (backend *PushBackEnd) Unsubscribe(service, sub string, dp *push.DeliveryPoint) error {
	return backend.db.RemoveDeliveryPointFromService(service, sub, dp)
}

func (backend *PushBackEnd) processError() {
	for err := range backend.errChan {
		rid := randomUniqId()
		nullHandler := &NullApiResponseHandler{}
		e := backend.fixError(rid, "", err, backend.loggers[LoggerPush], 0*time.Second, nullHandler)
		if e != nil {
			switch e0 := e.(type) {
			case *push.InfoReport:
				backend.loggers[LoggerPush].Infof("%v", e0)
			default: // Includes *ErrorReport
				backend.loggers[LoggerPush].Errorf("Error: %v", e0)
			}
		}
	}
}

func (backend *PushBackEnd) fixError(
	reqID string,
	remoteAddr string,
	event error,
	logger log.Logger,
	after time.Duration,
	handler ApiResponseHandler,
) error {
	if event == nil {
		return nil
	}
	switch err := event.(type) {
	case *push.RetryError:
		backend.fixRetryError(err, reqID, remoteAddr, logger, after, handler)
		return nil
	case *push.PushServiceProviderUpdate:
		backend.fixPushServiceProviderUpdate(err, reqID, remoteAddr, logger, handler)
		return nil
	case *push.DeliveryPointUpdate:
		backend.fixDeliveryPointUpdate(err, reqID, remoteAddr, logger, handler)
		return nil
	case *push.InvalidRegistrationUpdate:
		backend.fixInvalidRegistrationUpdate(err, reqID, remoteAddr, logger, handler)
		return nil
	case *push.UnsubscribeUpdate:
		backend.fixUnsubscribeUpdate(err, reqID, remoteAddr, logger, handler)
		return nil
	default:
		return err
	}
}

// fixRetryError will retry sending the push with longer and longer intervals, and give up when the interval exceeds 1 minute.
func (backend *PushBackEnd) fixRetryError(
	err *push.RetryError,
	reqID string,
	remoteAddr string,
	logger log.Logger,
	after time.Duration,
	handler ApiResponseHandler,
) {
	if err.Provider == nil || err.Destination == nil || err.Content == nil {
		return
	}
	var service string
	var sub string
	var ok bool

	if service, ok = err.Provider.FixedData["service"]; !ok {
		return
	}

	if sub, ok = err.Destination.FixedData["subscriber"]; !ok {
		return
	}
	if after <= 1*time.Second {
		after = 5 * time.Second
	}
	providerName := err.Provider.Name()
	destinationName := err.Destination.Name()
	if after > 1*time.Minute {
		logger.Errorf("RequestID=%v Service=%v Subscriber=%v PushServiceProvider=%v DeliveryPoint=%v Failed after retry", reqID, service, sub, providerName, destinationName)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, Subscriber: &sub, PushServiceProvider: &providerName, DeliveryPoint: &destinationName, Code: UNIQUSH_ERROR_FAILED_RETRY})
		return
	}
	logger.Infof("RequestID=%v Service=%v Subscriber=%v PushServiceProvider=%v DeliveryPoint=%v Retry after %v", reqID, service, sub, providerName, destinationName, after)
	go func() {
		<-time.After(after)
		subs := make([]string, 1)
		subs[0] = sub
		after = 2 * after
		backend.pushImpl(reqID, remoteAddr, service, subs, nil, err.Content, nil, backend.loggers[LoggerPush], err.Provider, err.Destination, after, handler)
	}()
}

func (backend *PushBackEnd) fixPushServiceProviderUpdate(
	err *push.PushServiceProviderUpdate,
	reqID string,
	remoteAddr string,
	logger log.Logger,
	handler ApiResponseHandler,
) {
	if err.Provider == nil {
		return
	}
	var service string
	var ok bool

	if service, ok = err.Provider.FixedData["service"]; !ok {
		return
	}
	psp := err.Provider
	e := backend.db.ModifyPushServiceProvider(psp)
	pspName := psp.Name()
	if e != nil {
		logger.Errorf("RequestID=%v Service=%v PushServiceProvider=%v Update Failed: %v", reqID, service, pspName, e)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, PushServiceProvider: &pspName, Code: UNIQUSH_ERROR_UPDATE_PUSH_SERVICE_PROVIDER, ErrorMsg: strPtrOfErr(e)})
	} else {
		logger.Infof("RequestID=%v Service=%v PushServiceProvider=%v Update Success", reqID, service, pspName)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, PushServiceProvider: &pspName, Code: UNIQUSH_SUCCESS})
	}
}

func (backend *PushBackEnd) fixDeliveryPointUpdate(
	err *push.DeliveryPointUpdate,
	reqID string,
	remoteAddr string,
	logger log.Logger,
	handler ApiResponseHandler,
) {
	if err.Destination == nil {
		return
	}
	var service string
	var sub string
	var ok bool

	if sub, ok = err.Destination.FixedData["subscriber"]; !ok {
		return
	}

	if service, ok = err.Destination.FixedData["service"]; !ok {
		service = ""
	}
	dp := err.Destination
	e := backend.db.ModifyDeliveryPoint(dp)
	dpName := dp.Name()
	if e != nil {
		logger.Errorf("Subscriber=%v DeliveryPoint=%v Update Failed: %v", sub, dpName, e)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Subscriber: &sub, Service: &service, DeliveryPoint: &dpName, Code: UNIQUSH_ERROR_UPDATE_DELIVERY_POINT, ErrorMsg: strPtrOfErr(e)})
	} else {
		logger.Infof("Service=%v Subscriber=%v DeliveryPoint=%v Update Success", service, sub, dpName)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Subscriber: &sub, Service: &service, DeliveryPoint: &dpName, Code: UNIQUSH_SUCCESS, ModifiedDp: true})
	}
}

func (backend *PushBackEnd) fixInvalidRegistrationUpdate(
	err *push.InvalidRegistrationUpdate,
	reqID string,
	remoteAddr string,
	logger log.Logger,
	handler ApiResponseHandler,
) {
	if err.Provider == nil || err.Destination == nil {
		return
	}

	var service string
	var sub string
	var ok bool

	if service, ok = err.Provider.FixedData["service"]; !ok {
		return
	}
	if sub, ok = err.Destination.FixedData["subscriber"]; !ok {
		return
	}
	dp := err.Destination
	e := backend.Unsubscribe(service, sub, dp)
	dpName := dp.Name()
	if e != nil {
		logger.Errorf("Service=%v Subscriber=%v DeliveryPoint=%v Removing invalid reg failed: %v", service, sub, dpName, e)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, Subscriber: &sub, DeliveryPoint: &dpName, Code: UNIQUSH_REMOVE_INVALID_REG, ErrorMsg: strPtrOfErr(e)})
	} else {
		logger.Infof("Service=%v Subscriber=%v DeliveryPoint=%v Invalid registration removed", service, sub, dpName)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, Subscriber: &sub, DeliveryPoint: &dpName, Code: UNIQUSH_REMOVE_INVALID_REG})
	}
}

func (backend *PushBackEnd) fixUnsubscribeUpdate(
	err *push.UnsubscribeUpdate,
	reqID string,
	remoteAddr string,
	logger log.Logger,
	handler ApiResponseHandler,
) {
	if err.Provider == nil || err.Destination == nil {
		return
	}
	var service string
	var sub string
	var ok bool

	if service, ok = err.Provider.FixedData["service"]; !ok {
		return
	}
	if sub, ok = err.Destination.FixedData["subscriber"]; !ok {
		return
	}
	dp := err.Destination
	e := backend.Unsubscribe(service, sub, dp)
	dpName := dp.Name()
	if e != nil {
		logger.Errorf("Service=%v Subscriber=%v DeliveryPoint=%v Unsubscribe failed: %v", service, sub, dpName, e)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, Subscriber: &sub, DeliveryPoint: &dpName, Code: UNIQUSH_UPDATE_UNSUBSCRIBE, ErrorMsg: strPtrOfErr(e)})
	} else {
		logger.Infof("Service=%v Subscriber=%v DeliveryPoint=%v Unsubscribe success", service, sub, dpName)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, Subscriber: &sub, DeliveryPoint: &dpName, Code: UNIQUSH_UPDATE_UNSUBSCRIBE})
	}
}

func getDeliveryPointNameOrUnknown(dp *push.DeliveryPoint) string {
	if dp != nil {
		return dp.Name()
	}
	return "Unknown"
}

func getProviderNameOrUnknown(provider *push.PushServiceProvider) string {
	if provider != nil {
		return provider.Name()
	}
	return "Unknown"
}

func (backend *PushBackEnd) collectResult(
	reqID string,
	remoteAddr string,
	service string,
	resChan <-chan *push.Result,
	logger log.Logger,
	after time.Duration,
	handler ApiResponseHandler,
) {
	for res := range resChan {
		var sub string
		ok := false
		if res.Destination != nil {
			sub, ok = res.Destination.FixedData["subscriber"]
		}
		if res.Provider != nil && res.Destination != nil {
			if !ok {
				destinationName := res.Destination.Name()
				logger.Errorf("RequestID=%v Subscriber=%v DeliveryPoint=%v Bad Delivery Point: No subscriber", reqID, sub, destinationName)
				handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, Subscriber: &sub, DeliveryPoint: &destinationName, Code: UNIQUSH_ERROR_BAD_DELIVERY_POINT})
				continue
			}
		}
		var subRepr string
		if ok {
			subRepr = sub
		} else {
			subRepr = "Unknown"
		}
		if res.Err == nil {
			dpName := getDeliveryPointNameOrUnknown(res.Destination)
			pspName := getProviderNameOrUnknown(res.Provider)
			msgID := res.MsgID
			logger.Infof("RequestID=%v Service=%v Subscriber=%v PushServiceProvider=%v DeliveryPoint=%v MsgID=%v Success!", reqID, service, subRepr, pspName, dpName, msgID)
			handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, Subscriber: &sub, PushServiceProvider: &pspName, DeliveryPoint: &dpName, MessageId: &msgID, Code: UNIQUSH_SUCCESS})
			continue
		}
		err := backend.fixError(reqID, remoteAddr, res.Err, logger, after, handler)
		if err != nil {
			dpName := getDeliveryPointNameOrUnknown(res.Destination)
			pspName := getProviderNameOrUnknown(res.Provider)
			logger.Errorf("RequestID=%v Service=%v Subscriber=%v PushServiceProvider=%v DeliveryPoint=%v Failed: %v", reqID, service, subRepr, pspName, dpName, err)
			handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, Subscriber: &sub, PushServiceProvider: &pspName, DeliveryPoint: &dpName, Code: UNIQUSH_ERROR_GENERIC, ErrorMsg: strPtrOfErr(err)})
		}
	}
}

func (backend *PushBackEnd) NumberOfDeliveryPoints(service, sub string, logger log.Logger) int {
	pspDpList, err := backend.db.GetPushServiceProviderDeliveryPointPairs(service, sub, nil)
	if err != nil {
		logger.Errorf("Query=NumberOfDeliveryPoints Service=%v Subscriber=%v Failed: Database Error %v", service, sub, err)
		return 0
	}
	return len(pspDpList)
}

func (backend *PushBackEnd) Subscriptions(services []string, subscriber string, logger log.Logger, fetchIds bool) []map[string]string {
	emptyResult := []map[string]string{}

	log := func(msg string, err error) {
		if nil == err {
			logger.Infof("Service=%v Subscriber=%v %s", services, subscriber, msg)
		} else {
			logger.Errorf("Service=%v Subscriber=%v %s %v", services, subscriber, msg, err)
		}
	}

	if len(subscriber) == 0 {
		log("", errors.New("NoSubscriber"))
		return emptyResult
	}

	subscriptions, err := backend.db.GetSubscriptions(services, subscriber, logger)
	if err != nil {
		log("", err)
		return emptyResult
	}
	if !fetchIds {
		for _, v := range subscriptions {
			delete(v, db.DELIVERY_POINT_ID)
		}
	}

	return subscriptions
}

func (backend *PushBackEnd) RebuildServiceSet() error {
	return backend.db.RebuildServiceSet()
}

func (backend *PushBackEnd) Push(reqID string, remoteAddr string, service string, subs []string, dpNamesRequested []string, notif *push.Notification, perdp map[string][]string, logger log.Logger, handler ApiResponseHandler) {
	backend.pushImpl(reqID, remoteAddr, service, subs, dpNamesRequested, notif, perdp, logger, nil, nil, 0*time.Second, handler)
}

// pushImpl will fetch subscriptions and send push notifications using the corresponding service.
// It will retry pushes if they fail (May be through sending an RetryError, or it may be within the psp implementation).
func (backend *PushBackEnd) pushImpl(
	reqID string,
	remoteAddr string,
	service string,
	subs []string,
	dpNamesRequested []string,
	notif *push.Notification,
	perdp map[string][]string,
	logger log.Logger,
	provider *push.PushServiceProvider,
	dest *push.DeliveryPoint,
	after time.Duration,
	handler ApiResponseHandler,
) {
	// dpChanMap maps a PushServiceProvider(by name) to a list of delivery points to send data to (from various subscriptions).
	// If there are multiple subscriptions, lazily adding to a channel is probably faster than passing a list,
	// because you'd need to fetch all subscriptions from the DB before starting to push otherwise.
	dpChanMap := make(map[string]chan *push.DeliveryPoint)
	// wg is used to wait for all pushes and push responses to complete before returning.
	wg := new(sync.WaitGroup)

	// Loop over all subscriptions, fetching the list of corresponding delivery points to send to from the db, starting to push and send pushes.
	for _, sub := range subs {
		dpidx := 0
		var pspDpList []db.PushServiceProviderDeliveryPointPair
		if provider != nil && dest != nil {
			// Note: subs always has length 1 when dest != nil
			pspDpList = make([]db.PushServiceProviderDeliveryPointPair, 1)
			pspDpList[0].PushServiceProvider = provider
			pspDpList[0].DeliveryPoint = dest
		} else {
			var err error
			pspDpList, err = backend.db.GetPushServiceProviderDeliveryPointPairs(service, sub, dpNamesRequested)
			if err != nil {
				logger.Errorf("RequestID=%v Service=%v Subscriber=%v Failed: Database Error: %v", reqID, service, sub, err)
				handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, Subscriber: &sub, Code: UNIQUSH_ERROR_DATABASE, ErrorMsg: strPtrOfErr(err)})
				continue
			}
		}

		if len(pspDpList) == 0 {
			logger.Errorf("RequestID=%v Service=%v Subscriber=%v Failed: No device", reqID, service, sub)
			handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, Subscriber: &sub, Code: UNIQUSH_ERROR_NO_DEVICE})
			continue
		}

		for _, pair := range pspDpList {
			psp := pair.PushServiceProvider
			dp := pair.DeliveryPoint
			if psp == nil {
				logger.Errorf("RequestID=%v Service=%v Subscriber=%v Failed once: nil Push Service Provider", reqID, service, sub)
				handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, Subscriber: &sub, Code: UNIQUSH_ERROR_NO_PUSH_SERVICE_PROVIDER})
				continue
			}
			if dp == nil {
				logger.Errorf("RequestID=%v Service=%v Subscriber=%v Failed once: nil Delivery Point", reqID, service, sub)
				handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqID, From: &remoteAddr, Service: &service, Subscriber: &sub, Code: UNIQUSH_ERROR_NO_DELIVERY_POINT})
				continue
			}
			var dpQueue chan *push.DeliveryPoint
			var ok bool
			if dpQueue, ok = dpChanMap[psp.Name()]; !ok {
				dpQueue = make(chan *push.DeliveryPoint)
				dpChanMap[psp.Name()] = dpQueue
				resChan := make(chan *push.Result)
				wg.Add(1)
				note := notif
				if len(perdp) > 0 {
					note = notif.Clone()
					for k, v := range perdp {
						value := v[dpidx%len(v)]
						note.Data[k] = value
					}
					dpidx++
				}
				// Make the pushservicemanager send to (each delivery point of) the PSP asyncronously
				go func() {
					backend.psm.Push(psp, dpQueue, resChan, note)
					wg.Done()
				}()
				wg.Add(1)
				// Wait for the response from the PSP asynchronously
				go func() {
					// Note: if this is a retry, the duration `after` will increase, and fixError will account for that when deciding to retry
					backend.collectResult(reqID, remoteAddr, service, resChan, logger, after, handler)
					wg.Done()
				}()
			}

			// Add this delivery point to the group for that psp.Name()
			dpQueue <- dp
		}
	}
	// Signal that there are no more delivery points so that goroutines can stop reading the next delivery point.
	for _, dpch := range dpChanMap {
		close(dpch)
	}
	// Wait for every goroutine started by this method to finish.
	wg.Wait()
}

func (backend *PushBackEnd) Preview(ptname string, notif *push.Notification) ([]byte, push.Error) {
	return backend.psm.Preview(ptname, notif)
}
