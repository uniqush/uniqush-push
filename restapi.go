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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uniqush/log"
	"github.com/uniqush/uniqush-push/push"
)

// RestAPI implements uniqush's REST API (/push, /subscribe, /addpsp, etc).
type RestAPI struct {
	psm       *push.PushServiceManager
	loggers   []log.Logger
	backend   *PushBackEnd
	version   string
	waitGroup *sync.WaitGroup
	stopChan  chan<- bool
}

func randomUniqID() string {
	var d [16]byte
	io.ReadFull(rand.Reader, d[:])
	return fmt.Sprintf("%x-%v", time.Now().Unix(), base64.URLEncoding.EncodeToString(d[:]))
}

// NewRestAPI constructs the data structures for the singleton REST API of uniqush-push
func NewRestAPI(psm *push.PushServiceManager, loggers []log.Logger, version string, backend *PushBackEnd) *RestAPI {
	ret := new(RestAPI)
	ret.psm = psm
	ret.loggers = loggers
	ret.version = version
	ret.backend = backend
	ret.waitGroup = new(sync.WaitGroup)
	return ret
}

// Constants for the paths of the REST API
const (
	AddPushServiceProviderToServiceURL      = "/addpsp"
	RemovePushServiceProviderFromServiceURL = "/rmpsp"
	AddDeliveryPointToServiceURL            = "/subscribe"
	RemoveDeliveryPointFromServiceURL       = "/unsubscribe"
	PushNotificationURL                     = "/push"
	PreviewPushNotificationURL              = "/previewpush"
	StopProgramURL                          = "/stop"
	VersionInfoURL                          = "/version"
	QueryNumberOfDeliveryPointsURL          = "/nrdp"
	QuerySubscriptionsURL                   = "/subscriptions"
	QueryPushServiceProviders               = "/psps"
	RebuildServiceSetURL                    = "/rebuildserviceset"
)

// TODO: Switch to the stricter regex in a subsequent release.
// uniqush.org didn't really document the accepted characters, so it's possible some clients used invalid characters.
// Don't allow backticks.
// TODO: Log an error if they're used.
// var validServicePattern *regexp.Regexp = regexp.MustCompile(`^[a-zA-Z.0-9_@-]+$`)
// var validSubscriberPattern *regexp.Regexp = regexp.MustCompile(`^[a-zA-Z.0-9_@-]+$`)

var validServicePattern = regexp.MustCompile(`^[a-zA-Z.0-9_@\[\]^\\\\-]+$`)
var validSubscriberPattern = regexp.MustCompile(`^[a-zA-Z.0-9_@-\[\]^\\\\-]+$`)

func validateSubscribers(subs []string) error {
	for _, sub := range subs {
		if !validSubscriberPattern.MatchString(sub) {
			return fmt.Errorf("invalid subscriber name: %q. Accepted characters: a-z, A-Z, 0-9, -, _, @ or .", sub) // nolint: golint
		}
	}
	return nil
}

func validateService(service string) error {
	if !validServicePattern.MatchString(service) {
		return fmt.Errorf("invalid service name: %q. Accepted characters: a-z, A-Z, 0-9, -, _, @ or .", service) // nolint: golint
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

// Get the optional delivery_point_ids from a map.
func getDeliveryPointIdsFromMap(kv map[string]string) (deliveryPointNames []string, err error) {
	var v string
	var ok bool
	if v, ok = kv["delivery_point_id"]; !ok {
		return nil, nil
	}
	if len(v) == 0 {
		return nil, fmt.Errorf("EmptyDeliveryPoints")
	}
	s := strings.Split(v, ",")
	deliveryPointNames = make([]string, 0, len(s))
	for _, dpName := range s {
		if len(dpName) > 0 {
			// TODO validate.
			deliveryPointNames = append(deliveryPointNames, dpName)
		}
	}
	if len(deliveryPointNames) == 0 {
		return nil, fmt.Errorf("EmptyDeliveryPoints")
	}
	return
}

func getServiceFromMap(kv map[string]string) (service string, err error) {
	var ok bool
	if service, ok = kv["service"]; !ok {
		err = fmt.Errorf("NoService")
		return
	}
	err = validateService(service)
	if err != nil {
		service = ""
		return
	}
	return
}

func (api *RestAPI) changePushServiceProvider(kv map[string]string, logger log.Logger, remoteAddr string, add bool) APIResponseDetails {
	psp, err := api.psm.BuildPushServiceProviderFromMap(kv)
	if err != nil {
		logger.Errorf("From=%v Cannot build push service provider: %v", remoteAddr, err)
		return APIResponseDetails{From: &remoteAddr, Code: UNIQUSH_ERROR_BUILD_PUSH_SERVICE_PROVIDER, ErrorMsg: strPtrOfErr(err)}
	}
	service, err := getServiceFromMap(kv)
	if err != nil {
		logger.Errorf("From=%v Cannot get service name: %v; %v", remoteAddr, service, err)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SERVICE, ErrorMsg: strPtrOfErr(err)}
	}
	if add {
		err = api.backend.AddPushServiceProvider(service, psp)
	} else {
		err = api.backend.RemovePushServiceProvider(service, psp)
	}
	if err != nil {
		logger.Errorf("From=%v Failed: %v", remoteAddr, err)
		return APIResponseDetails{From: &remoteAddr, Code: UNIQUSH_ERROR_GENERIC, ErrorMsg: strPtrOfErr(err)}
	}
	pspName := psp.Name()
	logger.Infof("From=%v Service=%v PushServiceProvider=%v Success!", remoteAddr, service, pspName)
	return APIResponseDetails{From: &remoteAddr, Service: &service, PushServiceProvider: &pspName, Code: UNIQUSH_SUCCESS}
}

func (api *RestAPI) changeSubscription(kv map[string]string, logger log.Logger, remoteAddr string, issub bool) APIResponseDetails {
	dp, err := api.psm.BuildDeliveryPointFromMap(kv)
	if err != nil {
		logger.Errorf("Cannot build delivery point: %v", err)
		return APIResponseDetails{From: &remoteAddr, Code: UNIQUSH_ERROR_BUILD_DELIVERY_POINT, ErrorMsg: strPtrOfErr(err)}
	}
	service, err := getServiceFromMap(kv)
	if err != nil {
		logger.Errorf("From=%v Cannot get service name: %v; %v", remoteAddr, service, err)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SERVICE, ErrorMsg: strPtrOfErr(err)}
	}
	subs, err := getSubscribersFromMap(kv, true)
	if err != nil {
		logger.Errorf("From=%v Service=%v Cannot get subscriber: %v", remoteAddr, service, err)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SUBSCRIBER, ErrorMsg: strPtrOfErr(err)}
	}

	var psp *push.PushServiceProvider
	if issub {
		psp, err = api.backend.Subscribe(service, subs[0], dp)
	} else {
		err = api.backend.Unsubscribe(service, subs[0], dp)
	}
	if err != nil {
		logger.Errorf("From=%v Failed: %v", remoteAddr, err)
		return APIResponseDetails{From: &remoteAddr, Code: UNIQUSH_ERROR_GENERIC, ErrorMsg: strPtrOfErr(err)}
	}
	dpName := dp.Name()
	if psp == nil {
		logger.Infof("From=%v Service=%v Subscriber=%v DeliveryPoint=%v Success!", remoteAddr, service, subs[0], dpName)
		return APIResponseDetails{From: &remoteAddr, Service: &service, Subscriber: &subs[0], DeliveryPoint: &dpName, Code: UNIQUSH_SUCCESS}
	}
	pspName := psp.Name()
	logger.Infof("From=%v Service=%v Subscriber=%v PushServiceProvider=%v DeliveryPoint=%v Success!", remoteAddr, service, subs[0], pspName, dpName)
	return APIResponseDetails{From: &remoteAddr, Service: &service, Subscriber: &subs[0], DeliveryPoint: &dpName, PushServiceProvider: &pspName, Code: UNIQUSH_SUCCESS}
}

func (api *RestAPI) buildNotificationFromKV(reqID string, kv map[string]string, logger log.Logger, remoteAddr string, service string, subs []string) (notif *push.Notification, details *APIResponseDetails, err error) {
	notif = push.NewEmptyNotification()

	for k, v := range kv {
		if len(v) == 0 {
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
		logger.Errorf("RequestID=%v From=%v Service=%v NrSubscribers=%v Subscribers=\"%+v\" EmptyNotification", reqID, remoteAddr, service, len(subs), subs)
		details = &APIResponseDetails{RequestID: &reqID, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_EMPTY_NOTIFICATION}
		return nil, details, errors.New("empty notification")
	}
	return notif, nil, nil
}

func (api *RestAPI) pushNotification(reqID string, kv map[string]string, perdp map[string][]string, logger log.Logger, remoteAddr string, handler APIResponseHandler) {
	service, err := getServiceFromMap(kv)
	if err != nil {
		logger.Errorf("RequestID=%v From=%v Cannot get service name: %v; %v", reqID, remoteAddr, service, err)
		handler.AddDetailsToHandler(APIResponseDetails{RequestID: &reqID, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SERVICE})
		return
	}
	subs, err := getSubscribersFromMap(kv, false)
	if err != nil {
		logger.Errorf("RequestID=%v From=%v Service=%v Cannot get subscriber: %v", reqID, remoteAddr, service, err)
		handler.AddDetailsToHandler(APIResponseDetails{RequestID: &reqID, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_SUBSCRIBER})
		return
	}
	if len(subs) == 0 {
		logger.Errorf("RequestID=%v From=%v Service=%v NoSubscriber", reqID, remoteAddr, service)
		handler.AddDetailsToHandler(APIResponseDetails{RequestID: &reqID, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_NO_SUBSCRIBER})
		return
	}
	dpIds, err := getDeliveryPointIdsFromMap(kv)
	if err != nil {
		logger.Errorf("RequestID=%v From=%v Service=%v Cannot get delivery point ids: %v", reqID, remoteAddr, service, err)
		handler.AddDetailsToHandler(APIResponseDetails{RequestID: &reqID, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_CANNOT_GET_DELIVERY_POINT_ID})
		return
	}
	if len(subs) == 0 {
		logger.Errorf("RequestID=%v From=%v Service=%v NoSubscriber", reqID, remoteAddr, service)
		handler.AddDetailsToHandler(APIResponseDetails{RequestID: &reqID, From: &remoteAddr, Service: &service, Code: UNIQUSH_ERROR_NO_SUBSCRIBER})
		return
	}

	notif, details, err := api.buildNotificationFromKV(reqID, kv, logger, remoteAddr, service, subs)
	if err != nil {
		handler.AddDetailsToHandler(*details)
		return
	}

	logger.Infof("RequestID=%v From=%v Service=%v NrSubscribers=%v Subscribers=\"%+v\"", reqID, remoteAddr, service, len(subs), subs)

	api.backend.Push(reqID, remoteAddr, service, subs, dpIds, notif, perdp, logger, handler)
}

// preview takes key-value pairs (pushservicetype, plus data for building the payload), a logger, and logging data.
func (api *RestAPI) preview(reqID string, kv map[string]string, logger log.Logger, remoteAddr string) PreviewAPIResponseDetails {
	pushServiceType, ok := kv["pushservicetype"]
	if !ok || pushServiceType == "" {
		msg := "Must specify a known pushservicetype"
		return PreviewAPIResponseDetails{Code: UNIQUSH_ERROR_NO_PUSH_SERVICE_TYPE, ErrorMsg: &msg}
	}
	delete(kv, "pushservicetype") // Some modules don't filter this out.
	notif, details, err := api.buildNotificationFromKV(reqID, kv, logger, remoteAddr, "placeholderservice", []string{})
	if err != nil {
		return PreviewAPIResponseDetails{
			Code:     details.Code,
			ErrorMsg: details.ErrorMsg,
		}
	}

	data, err := api.backend.Preview(pushServiceType, notif)
	if err != nil {
		errmsg := err.Error()
		return PreviewAPIResponseDetails{Code: UNIQUSH_ERROR_GENERIC, ErrorMsg: &errmsg}
	}
	return PreviewAPIResponseDetails{Code: UNIQUSH_SUCCESS, Payload: apiBytesToObject(data)}
}

func apiBytesToObject(data []byte) interface{} {
	// currently, all types are JSON. In the future, there may be non-JSON payloads in a protocol.
	// Either return a string or an object (to be converted to JSON again by the API)
	var obj interface{}
	err := json.Unmarshal(data, &obj)
	if err != nil || obj == nil {
		return string(data)
	}
	return obj
}

func (api *RestAPI) stop(w io.Writer, remoteAddr string) {
	api.waitGroup.Wait()
	api.backend.Finalize()
	api.loggers[LoggerWeb].Infof("stopped by %v", remoteAddr)
	if w != nil {
		fmt.Fprintf(w, "Stopped\r\n")
	}
	api.stopChan <- true
}

func (api *RestAPI) numberOfDeliveryPoints(kv map[string][]string, logger log.Logger) int {
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
	ret = api.backend.NumberOfDeliveryPoints(service, sub, logger)
	return ret
}

func (api *RestAPI) querySubscriptions(kv map[string][]string, logger log.Logger) []byte {
	// "subscriber" is a required parameter
	subscriberParam, ok := kv["subscriber"]
	if !ok || len(subscriberParam) == 0 {
		logger.Errorf("Query=Subscriptions NoSubscriber %v", kv)
		return []byte("[]")
	}
	var services []string
	// "services" is an optional parameter that can have one or more services passed in the form of a CSV
	servicesParam, ok := kv["services"]
	if ok && len(servicesParam) > 0 {
		services = strings.Split(servicesParam[0], ",")
	}
	includeDPIds := false
	if v, ok := kv["include_delivery_point_ids"]; ok && len(v) > 0 && v[0] == "1" {
		includeDPIds = true
	}
	subscriptions := api.backend.Subscriptions(services, subscriberParam[0], logger, includeDPIds)
	json, err := json.Marshal(subscriptions)
	if err != nil {
		logger.Errorf("Service=%v Subscriber=%v %s", services, subscriberParam[0], err)
		return []byte("[]")
	}

	return json
}

func encodePSPForAPI(psp *push.PushServiceProvider) map[string]string {
	result := make(map[string]string)
	for key, value := range psp.VolatileData {
		result[key] = value
	}
	for key, value := range psp.FixedData {
		result[key] = value
	}
	return result
}

// queryPSPs returns JSON describing the set of all PSPs stored in Uniqush. This API is intended for debugging/verifying that uniqush is set up properly.
func (api *RestAPI) queryPSPs(logger log.Logger) []byte {
	psps, err := api.backend.GetPushServiceProviderConfigs()
	type responseType struct {
		Services     map[string][]map[string]string `json:"services"`
		ErrorMessage *string                        `json:"errorMsg,omitempty"`
		Code         string                         `json:"code"`
	}
	var r responseType
	r.Services = make(map[string][]map[string]string)
	for _, psp := range psps {
		data := encodePSPForAPI(psp)
		service := data["service"]
		r.Services[service] = append(r.Services[service], data)
	}
	if err != nil {
		errorMsg := err.Error()
		logger.Errorf("Error querying PSPs in /psps: %v", err)
		r.Code = UNIQUSH_ERROR_DATABASE
		r.ErrorMessage = &errorMsg
	} else {
		r.Code = UNIQUSH_SUCCESS
	}
	json, err := json.Marshal(r)
	if err != nil {
		return []byte("Failed to serialize response")
	}
	return json
}

// rebuildServiceSet is used to make sure that the /subscriptions and /psps APIs work properly, on uniqush setups created before those APIs existed.
func (api *RestAPI) rebuildServiceSet(logger log.Logger) []byte {
	err := api.backend.RebuildServiceSet()
	var details APIResponseDetails
	if err != nil {
		logger.Errorf("Error in /rebuildserviceset: %v", err)
		errorMsg := err.Error()
		details = APIResponseDetails{
			Code:     UNIQUSH_ERROR_GENERIC,
			ErrorMsg: &errorMsg,
		}
	} else {
		details = APIResponseDetails{Code: UNIQUSH_SUCCESS}
	}
	json, err := json.Marshal(details)
	if err != nil {
		return []byte("Failed to encode response")
	}
	return json
}

func parseKV(form url.Values) (kv map[string]string, perdp map[string][]string) {
	kv = make(map[string]string, len(form))
	perdp = make(map[string][]string, 3)
	perdpPrefix := "uniqush.perdp."
	for k, v := range form {
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
	return kv, perdp
}

func (api *RestAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	remoteAddr := r.RemoteAddr

	switch r.URL.Path {
	case QuerySubscriptionsURL:
		r.ParseForm()
		n := api.querySubscriptions(r.Form, api.loggers[LoggerSubscriptions])
		fmt.Fprintf(w, "%s\r\n", n)
		return
	case QueryPushServiceProviders:
		n := api.queryPSPs(api.loggers[LoggerPSPs])
		fmt.Fprintf(w, "%s\r\n", n)
		return
	case RebuildServiceSetURL:
		n := api.rebuildServiceSet(api.loggers[LoggerServices])
		fmt.Fprintf(w, "%s\r\n", n)
		return
	case QueryNumberOfDeliveryPointsURL:
		r.ParseForm()
		n := api.numberOfDeliveryPoints(r.Form, api.loggers[LoggerWeb])
		fmt.Fprintf(w, "%v\r\n", n)
		return
	case PreviewPushNotificationURL:
		r.ParseForm()
		kv, _ := parseKV(r.Form)
		rid := randomUniqID()
		details := api.preview(rid, kv, api.loggers[LoggerPreview], remoteAddr)
		bytes, err := json.Marshal(details)
		if err != nil {
			fmt.Fprintf(w, "%s\r\n", err.Error())
			return
		}
		fmt.Fprintf(w, "%s\r\n", string(bytes))
		return
	case VersionInfoURL:
		fmt.Fprintf(w, "%v\r\n", api.version)
		api.loggers[LoggerWeb].Infof("Checked version from %v", remoteAddr)
		return
	case StopProgramURL:
		api.stop(w, remoteAddr)
		return
	}
	r.ParseForm()
	kv, perdp := parseKV(r.Form)

	api.waitGroup.Add(1)
	defer api.waitGroup.Done()
	var handler APIResponseHandler
	var details APIResponseDetails
	switch r.URL.Path {
	case AddPushServiceProviderToServiceURL:
		handler = newSimpleResponseHandler(api.loggers[LoggerAddPSP], "AddPushServiceProvider")
		details = api.changePushServiceProvider(kv, api.loggers[LoggerAddPSP], remoteAddr, true)
		handler.AddDetailsToHandler(details)
	case RemovePushServiceProviderFromServiceURL:
		handler = newSimpleResponseHandler(api.loggers[LoggerRemovePSP], "RemovePushServiceProvider")
		details = api.changePushServiceProvider(kv, api.loggers[LoggerRemovePSP], remoteAddr, false)
		handler.AddDetailsToHandler(details)
	case AddDeliveryPointToServiceURL:
		handler = newSimpleResponseHandler(api.loggers[LoggerSub], "Subscribe")
		details = api.changeSubscription(kv, api.loggers[LoggerSub], remoteAddr, true)
		handler.AddDetailsToHandler(details)
	case RemoveDeliveryPointFromServiceURL:
		handler = newSimpleResponseHandler(api.loggers[LoggerUnsub], "Unsubscribe")
		details = api.changeSubscription(kv, api.loggers[LoggerUnsub], remoteAddr, false)
		handler.AddDetailsToHandler(details)
	case PushNotificationURL:
		handler = newPushResponseHandler(api.loggers[LoggerPush])
		rid := randomUniqID()
		api.pushNotification(rid, kv, perdp, api.loggers[LoggerPush], remoteAddr, handler)
	}
	if handler != nil {
		// Be consistent about ending responses in \r\n
		_, err := fmt.Fprintf(w, "%s\r\n", string(handler.ToJSON()))
		if err != nil {
			api.loggers[LoggerWeb].Errorf("Failed to write http response: %v", err)
		}
	}
}

// Run will start the API service, listening for requests on the address addr
func (api *RestAPI) Run(addr string, stopChan chan<- bool) {
	api.loggers[LoggerWeb].Infof("[Start] %s", addr)
	api.loggers[LoggerWeb].Debugf("[Version] %s", api.version)

	http.Handle(StopProgramURL, api)
	http.Handle(VersionInfoURL, api)
	http.Handle(AddPushServiceProviderToServiceURL, api)
	http.Handle(AddDeliveryPointToServiceURL, api)
	http.Handle(RemoveDeliveryPointFromServiceURL, api)
	http.Handle(RemovePushServiceProviderFromServiceURL, api)
	http.Handle(PushNotificationURL, api)
	http.Handle(PreviewPushNotificationURL, api)
	http.Handle(QueryNumberOfDeliveryPointsURL, api)
	http.Handle(QuerySubscriptionsURL, api)
	http.Handle(QueryPushServiceProviders, api)
	http.Handle(RebuildServiceSetURL, api)

	api.stopChan = stopChan
	err := http.ListenAndServe(addr, nil)
	if err != nil {
		api.loggers[LoggerWeb].Fatalf("HTTPServerError \"%v\"", err)
	}
}
