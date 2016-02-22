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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/orsonwang/uniqush-push/push"
	"github.com/uniqush/log"
)

type RestAPI struct {
	psm       *PushServiceManager
	loggers   []log.Logger
	backend   *PushBackEnd
	version   string
	waitGroup *sync.WaitGroup
	stopChan  chan<- bool
}

func randomUniqId() string {
	var d [16]byte
	io.ReadFull(rand.Reader, d[:])
	return fmt.Sprintf("%x-%v", time.Now().Unix(), base64.URLEncoding.EncodeToString(d[:]))
}

// loggers: sequence is web, add
func NewRestAPI(psm *PushServiceManager, loggers []log.Logger, version string, backend *PushBackEnd) *RestAPI {
	ret := new(RestAPI)
	ret.psm = psm
	ret.loggers = loggers
	ret.version = version
	ret.backend = backend
	ret.waitGroup = new(sync.WaitGroup)
	return ret
}

const (
	ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL    = "/addpsp"
	REMOVE_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL = "/rmpsp"
	ADD_DELIVERY_POINT_TO_SERVICE_URL           = "/subscribe"
	REMOVE_DELIVERY_POINT_FROM_SERVICE_URL      = "/unsubscribe"
	PUSH_NOTIFICATION_URL                       = "/push"
	STOP_PROGRAM_URL                            = "/stop"
	VERSION_INFO_URL                            = "/version"
	QUERY_NUMBER_OF_DELIVERY_POINTS_URL         = "/nrdp"
)

var validServicePattern *regexp.Regexp
var validSubscriberPattern *regexp.Regexp

func init() {
	var err error
	validServicePattern, err = regexp.Compile("^[a-zA-z\\.0-9-_@]+$")
	if err != nil {
		validServicePattern = nil
	}
	validSubscriberPattern, err = regexp.Compile("^[a-zA-z\\.0-9-_@]+$")
	if err != nil {
		validSubscriberPattern = nil
	}
}

func validateSubscribers(subs []string) error {
	if validSubscriberPattern != nil {
		for _, sub := range subs {
			if !validSubscriberPattern.MatchString(sub) {
				return fmt.Errorf("invalid subscriber name: %s. Accept charaters: a-z, A-Z, 0-9, -, _, @ or .", sub)
			}
		}
	}
	return nil
}

func validateService(service string) error {
	if validServicePattern != nil {
		if !validServicePattern.MatchString(service) {
			return fmt.Errorf("invalid service name: %s. Accept charaters: a-z, A-Z, 0-9, -, _, @ or .", service)
		}
	}
	return nil
}

func getSubscribersFromMap(kv map[string]string, validate bool) (subs []string, err error) {
	var v string
	var ok bool
	if v, ok = kv["subscriber"]; !ok {
		if v, ok = kv["subscribers"]; !ok {
			err = fmt.Errorf("NoSubscriber")
			return
		}
	}
	s := strings.Split(v, ",")
	subs = make([]string, 0, len(s))
	for _, sub := range s {
		if len(sub) > 0 {
			subs = append(subs, sub)
		}
	}
	if validate {
		err = validateSubscribers(subs)
		if err != nil {
			subs = nil
			return
		}
	}
	return
}

func getServiceFromMap(kv map[string]string, validate bool) (service string, err error) {
	var ok bool
	if service, ok = kv["service"]; !ok {
		err = fmt.Errorf("NoService")
		return
	}
	if validate {
		err = validateService(service)
		if err != nil {
			service = ""
			return
		}
	}
	return
}

func (self *RestAPI) changePushServiceProvider(kv map[string]string, logger log.Logger, remoteAddr string, add bool) ApiResponseDetails {
	psp, err := self.psm.BuildPushServiceProviderFromMap(kv)
	if err != nil {
		logger.Errorf("From=%v Cannot build push service provider: %v", remoteAddr, err)
		return ApiResponseDetails{From: &remoteAddr, Code: UNIQUSH_ERROR_BUILD_PUSH_SERVICE_PROVIDER, ErrorMsg: strPtrOfErr(err)}
	}
	service, err := getServiceFromMap(kv, true)
	if err != nil {
		logger.Errorf("From=%v Cannot get service name: %v; %v", remoteAddr, service, err)
		return ApiResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SERVICE, ErrorMsg: strPtrOfErr(err)}
	}
	if add {
		err = self.backend.AddPushServiceProvider(service, psp)
	} else {
		err = self.backend.RemovePushServiceProvider(service, psp)
	}
	if err != nil {
		logger.Errorf("From=%v Failed: %v", remoteAddr, err)
		return ApiResponseDetails{From: &remoteAddr, Code: UNIQUSH_ERROR_GENERIC, ErrorMsg: strPtrOfErr(err)}
	}
	pspName := psp.Name()
	logger.Infof("From=%v Service=%v PushServiceProvider=%v Success!", remoteAddr, service, pspName)
	return ApiResponseDetails{From: &remoteAddr, Service: &service, PushServiceProvider: &pspName, Code: UNIQUSH_SUCCESS}
}

func (self *RestAPI) changeSubscription(kv map[string]string, logger log.Logger, remoteAddr string, issub bool) ApiResponseDetails {
	dp, err := self.psm.BuildDeliveryPointFromMap(kv)
	if err != nil {
		logger.Errorf("Cannot build delivery point: %v", err)
		return ApiResponseDetails{From: &remoteAddr, Code: UNIQUSH_ERROR_BUILD_DELIVERY_POINT, ErrorMsg: strPtrOfErr(err)}
	}
	service, err := getServiceFromMap(kv, true)
	if err != nil {
		logger.Errorf("From=%v Cannot get service name: %v; %v", remoteAddr, service, err)
		return ApiResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SERVICE, ErrorMsg: strPtrOfErr(err)}
	}
	subs, err := getSubscribersFromMap(kv, true)
	if err != nil {
		logger.Errorf("From=%v Service=%v Cannot get subscriber: %v", remoteAddr, service, err)
		return ApiResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SUBSCRIBER, ErrorMsg: strPtrOfErr(err)}
	}

	var psp *PushServiceProvider
	if issub {
		psp, err = self.backend.Subscribe(service, subs[0], dp)
	} else {
		err = self.backend.Unsubscribe(service, subs[0], dp)
	}
	if err != nil {
		logger.Errorf("From=%v Failed: %v", remoteAddr, err)
		return ApiResponseDetails{From: &remoteAddr, Code: UNIQUSH_ERROR_GENERIC, ErrorMsg: strPtrOfErr(err)}
	}
	dpName := dp.Name()
	if psp == nil {
		logger.Infof("From=%v Service=%v Subscriber=%v DeliveryPoint=%v Success!", remoteAddr, service, subs[0], dpName)
		return ApiResponseDetails{From: &remoteAddr, Service: &service, Subscriber: &subs[0], DeliveryPoint: &dpName, Code: UNIQUSH_SUCCESS}
	} else {
		pspName := psp.Name()
		logger.Infof("From=%v Service=%v Subscriber=%v PushServiceProvider=%v DeliveryPoint=%v Success!", remoteAddr, service, subs[0], pspName, dpName)
		return ApiResponseDetails{From: &remoteAddr, Service: &service, Subscriber: &subs[0], DeliveryPoint: &dpName, PushServiceProvider: &pspName, Code: UNIQUSH_SUCCESS}
	}
}

func (self *RestAPI) pushNotification(reqId string, kv map[string]string, perdp map[string][]string, logger log.Logger, remoteAddr string, handler ApiResponseHandler) {
	service, err := getServiceFromMap(kv, true)
	if err != nil {
		logger.Errorf("RequestId=%v From=%v Cannot get service name: %v; %v", reqId, remoteAddr, service, err)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqId, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SERVICE})
		return
	}
	subs, err := getSubscribersFromMap(kv, false)
	if err != nil {
		logger.Errorf("RequestId=%v From=%v Service=%v Cannot get subscriber: %v", reqId, remoteAddr, service, err)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqId, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SUBSCRIBER})
		return
	}
	if len(subs) == 0 {
		logger.Errorf("RequestId=%v From=%v Service=%v NoSubscriber", reqId, remoteAddr, service)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqId, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_NO_SUBSCRIBER})
		return
	}

	notif := NewEmptyNotification()

	for k, v := range kv {
		if len(v) <= 0 {
			continue
		}
		switch k {
		case "subscriber":
		case "subscribers":
		case "service":
			// three keys need to be ignored
		case "badge":
			if v != "" {
				var e error
				_, e = strconv.Atoi(v)
				if e == nil {
					notif.Data["badge"] = v
				} else {
					notif.Data["badge"] = "0"
				}
			}
		default:
			notif.Data[k] = v
		}
	}

	if notif.IsEmpty() {
		logger.Errorf("RequestId=%v From=%v Service=%v NrSubscribers=%v Subscribers=\"%+v\" EmptyNotification", reqId, remoteAddr, service, len(subs), subs)
		handler.AddDetailsToHandler(ApiResponseDetails{RequestId: &reqId, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_EMPTY_NOTIFICATION})
		return
	}

	logger.Infof("RequestId=%v From=%v Service=%v NrSubscribers=%v Subscribers=\"%+v\"", reqId, remoteAddr, service, len(subs), subs)

	self.backend.Push(reqId, remoteAddr, service, subs, notif, perdp, logger, handler)
}

func (self *RestAPI) stop(w io.Writer, remoteAddr string) {
	self.waitGroup.Wait()
	self.backend.Finalize()
	self.loggers[LOGGER_WEB].Infof("stopped by %v", remoteAddr)
	if w != nil {
		fmt.Fprintf(w, "Stopped\r\n")
	}
	self.stopChan <- true
	return
}

func (self *RestAPI) numberOfDeliveryPoints(kv map[string][]string, logger log.Logger, remoteAddr string) int {
	ret := 0
	ss, ok := kv["service"]
	if !ok {
		return ret
	}
	if len(ss) == 0 {
		return ret
	}
	service := ss[0]
	subs, ok := kv["subscriber"]
	if !ok {
		return ret
	}
	if len(subs) == 0 {
		return ret
	}
	sub := subs[0]
	ret = self.backend.NumberOfDeliveryPoints(service, sub, logger)
	return ret
}

func (self *RestAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	remoteAddr := r.RemoteAddr

	switch r.URL.Path {
	case QUERY_NUMBER_OF_DELIVERY_POINTS_URL:
		r.ParseForm()
		n := self.numberOfDeliveryPoints(r.Form, self.loggers[LOGGER_WEB], remoteAddr)
		fmt.Fprintf(w, "%v\r\n", n)
		return
	case VERSION_INFO_URL:
		fmt.Fprintf(w, "%v\r\n", self.version)
		self.loggers[LOGGER_WEB].Infof("Checked version from %v", remoteAddr)
		return
	case STOP_PROGRAM_URL:
		self.stop(w, remoteAddr)
		return
	}
	r.ParseForm()
	kv := make(map[string]string, len(r.Form))
	perdp := make(map[string][]string, 3)
	perdpPrefix := "uniqush.perdp."
	for k, v := range r.Form {
		if len(k) > len(perdpPrefix) {
			if k[:len(perdpPrefix)] == perdpPrefix {
				key := k[len(perdpPrefix):]
				perdp[key] = v
				continue
			}
		}
		if len(v) > 0 {
			kv[k] = v[0]
		}
	}

	self.waitGroup.Add(1)
	defer self.waitGroup.Done()
	var handler ApiResponseHandler = nil
	var details ApiResponseDetails
	switch r.URL.Path {
	case ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL:
		handler = newSimpleResponseHandler(self.loggers[LOGGER_ADDPSP], "AddPushServiceProvider")
		details = self.changePushServiceProvider(kv, self.loggers[LOGGER_ADDPSP], remoteAddr, true)
		handler.AddDetailsToHandler(details)
	case REMOVE_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL:
		handler = newSimpleResponseHandler(self.loggers[LOGGER_RMPSP], "RemovePushServiceProvider")
		details = self.changePushServiceProvider(kv, self.loggers[LOGGER_RMPSP], remoteAddr, false)
		handler.AddDetailsToHandler(details)
	case ADD_DELIVERY_POINT_TO_SERVICE_URL:
		handler = newSimpleResponseHandler(self.loggers[LOGGER_SUB], "Subscribe")
		details = self.changeSubscription(kv, self.loggers[LOGGER_SUB], remoteAddr, true)
		handler.AddDetailsToHandler(details)
	case REMOVE_DELIVERY_POINT_FROM_SERVICE_URL:
		handler = newSimpleResponseHandler(self.loggers[LOGGER_UNSUB], "Unsubscribe")
		details = self.changeSubscription(kv, self.loggers[LOGGER_UNSUB], remoteAddr, false)
		handler.AddDetailsToHandler(details)
	case PUSH_NOTIFICATION_URL:
		handler = newPushResponseHandler(self.loggers[LOGGER_PUSH])
		rid := randomUniqId()
		self.pushNotification(rid, kv, perdp, self.loggers[LOGGER_PUSH], remoteAddr, handler)
	}
	if handler != nil {
		_, err := w.Write(handler.ToJSON())
		if err != nil {
			self.loggers[LOGGER_WEB].Errorf("Failed to write http response: %v", err)
		}
	}
}

func (self *RestAPI) Run(addr string, stopChan chan<- bool) {
	self.loggers[LOGGER_WEB].Configf("[Start] %s", addr)
	self.loggers[LOGGER_WEB].Debugf("[Version] %s", self.version)
	http.Handle(STOP_PROGRAM_URL, self)
	http.Handle(VERSION_INFO_URL, self)
	http.Handle(ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL, self)
	http.Handle(ADD_DELIVERY_POINT_TO_SERVICE_URL, self)
	http.Handle(REMOVE_DELIVERY_POINT_FROM_SERVICE_URL, self)
	http.Handle(REMOVE_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL, self)
	http.Handle(PUSH_NOTIFICATION_URL, self)
	http.Handle(QUERY_NUMBER_OF_DELIVERY_POINTS_URL, self)
	self.stopChan = stopChan
	err := http.ListenAndServe(addr, nil)
	if err != nil {
		self.loggers[LOGGER_WEB].Fatalf("HTTPServerError \"%v\"", err)
	}
	return
}
