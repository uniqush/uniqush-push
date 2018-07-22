/*
 * Copyright 2011-2013 Nan Deng
 * Copyright 2013-2017 Uniqush Contributors.
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

// Package cloud_messaging contains implementation details common to GCM and FCM.
package cloud_messaging

// TODO: Refactor this as new features are added for FCM/GCM.

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/util"
)

// HTTPClient is a mockable interface for the parts of http.Client used by the GCM and FCM modules.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

var _ HTTPClient = &http.Client{}

// PushServiceBase contains the data structures common to Uniqush's GCM and FCM push service implementations.
// This struct is included in the GCM and FCM push service structs.
type PushServiceBase struct {
	// There is only one Transport and one Client for connecting to gcm or fcm, shared by the set of PSPs with pushservicetype=fcm (whether or not this is using a sandbox)
	// (Expose functionality of this client through public methods)
	client HTTPClient
	// const: "GCM" or "FCM", for logging
	initialism string
	// const: "uniqush.payload.fcm" or "uniqush.payload.gcm"
	rawPayloadKey string
	// const: "uniqush.notification.fcm" or "uniqush.notification.gcm"
	rawNotificationKey string
	// const: GCM/FCM endpoint. "https://..../push"
	serviceURL string
	// const: "gcm" or "fcm", for API requests to uniqush and API responses, as well as logging.
	pushServiceName string
}

// Finalize will close all open HTTPS connections to GCM/FCM.
func (psb *PushServiceBase) Finalize() {
	if client, isClient := psb.client.(*http.Client); isClient {
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}
}

// OverrideClient will override the client interface. It is used only for unit testing.
func (psb *PushServiceBase) OverrideClient(client HTTPClient) {
	psb.client = client
}

// MakePushServiceBase instantiates the fields of the FCM/GCM base class PushServiceBase.
// Note: Make sure that this can be copied by value (it's a collection of pointers right now).
// If it can no longer be copied by value, then change this into an initializer function.
func MakePushServiceBase(initialism string, rawPayloadKey string, rawNotificationKey string, serviceURL string, pushServiceName string) PushServiceBase {
	conf := &tls.Config{InsecureSkipVerify: false}
	tr := &http.Transport{
		TLSClientConfig:     conf,
		TLSHandshakeTimeout: time.Second * 5,
		// TODO: Make this configurable later on? The default of 2 is too low.
		// goals: (1) new connections should not be opened and closed frequently, (2) we should not run out of sockets.
		// This doesn't seem to need much tuning. The number of connections open at a given time seems to be less than 500, even when sending hundreds of pushes per second.
		MaxIdleConnsPerHost: 500,
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   time.Second * 10, // Add a timeout for all requests, in case of network issues.
	}
	return PushServiceBase{
		client:             client,
		initialism:         initialism,
		rawPayloadKey:      rawPayloadKey,
		rawNotificationKey: rawNotificationKey,
		serviceURL:         serviceURL,
		pushServiceName:    pushServiceName,
	}
}

// BuildDeliveryPointFromMap builds an FCM/GCM delivery point from key-value pairs provided by an API call to uniqush-push.
func (psb *PushServiceBase) BuildDeliveryPointFromMap(kv map[string]string, dp *push.DeliveryPoint) error {
	err := dp.AddCommonData(kv)
	if err != nil {
		return err
	}
	if account, ok := kv["account"]; ok && len(account) > 0 {
		dp.FixedData["account"] = account
	}
	if regid, ok := kv["regid"]; ok && len(regid) > 0 {
		dp.FixedData["regid"] = regid
	} else {
		return errors.New("NoRegId")
	}

	return nil
}

// CMCommonData contains common fields of HTTP API requests to GCM or FCM
type CMCommonData struct {
	RegIDs         []string `json:"registration_ids"`
	CollapseKey    string   `json:"collapse_key,omitempty"`
	DelayWhileIdle bool     `json:"delay_while_idle,omitempty"`
	TimeToLive     uint     `json:"time_to_live,omitempty"`
}

// CMData contains fields of HTTP API push requests to GCM or FCM.
type CMData struct {
	CMCommonData
	Data         map[string]interface{} `json:"data,omitempty"`         // For compatibility with other GCM platforms(e.g. iOS), should always be a map[string]string
	Notification map[string]interface{} `json:"notification,omitempty"` // For compatibility with other GCM platforms(e.g. iOS), should always be a map[string]string
}

// CMEmptyData contains
type CMEmptyData struct {
	CMCommonData
	Data map[string]interface{} `json:"data"`
}

// MarshalSafe will generate a pushable GCM/FCM notification. This does not check the notification length.
func (d *CMData) MarshalSafe() ([]byte, error) {
	if len(d.Data) == 0 && len(d.Notification) == 0 {
		// extremely rare case
		empty := CMEmptyData{
			CMCommonData: d.CMCommonData,
			Data:         map[string]interface{}{},
		}
		return util.MarshalJSONUnescaped(empty)
	}

	return util.MarshalJSONUnescaped(d)
}

func (d *CMData) String() string {
	bytes, err := d.MarshalSafe()
	if err != nil {
		return ""
	}
	return string(bytes)
}

// validateRawCMData verifies that the user-provided JSON payload is a valid JSON object.
func validateRawCMData(payload string) (map[string]interface{}, push.Error) {
	var data map[string]interface{}
	err := json.Unmarshal([]byte(payload), &data)
	if data == nil {
		return nil, push.NewBadNotificationWithDetails(fmt.Sprintf("Could not parse GCM/FCM data: %v", err))
	}
	return data, nil
}

// CMResult contains the JSON unserialized data of a response from GCM or FCM. Currently, this is the same for both GCM and FCM.
type CMResult struct {
	// MulticastID is unused
	MulticastID uint64 `json:"multicast_id"`
	// Success contains the number of pushes that succeeded
	Success uint `json:"success"`
	// Failure contains the number of pushes that failed.
	Failure uint `json:"failure"`
	// CanonicalIDs is unused
	CanonicalIDs uint `json:"canonical_ids"`
	// Results is the list of responses for each successful or unsuccessful push attempt.
	Results []map[string]string `json:"results"`
}

// Name will return the name of the implementing push service (either "gcm" or "fcm")
func (psb *PushServiceBase) Name() string {
	return psb.pushServiceName
}

// ToCMPayload will serialize notif as a push service payload
func (psb *PushServiceBase) ToCMPayload(notif *push.Notification, regIds []string) ([]byte, push.Error) {
	postData := notif.Data
	payload := new(CMData)
	payload.RegIDs = regIds

	// TTL: default is one hour
	payload.TimeToLive = 60 * 60
	payload.DelayWhileIdle = false

	if mgroup, ok := postData["msggroup"]; ok {
		payload.CollapseKey = mgroup
	} else {
		payload.CollapseKey = ""
	}

	if ttlStr, ok := postData["ttl"]; ok {
		ttl, err := strconv.ParseUint(ttlStr, 10, 32)
		if err == nil {
			payload.TimeToLive = uint(ttl)
		}
	}

	// Support uniqush.notification.gcm/fcm as another optional payload, to conform to GCM spec: https://developers.google.com/cloud-messaging/http-server-ref#send-downstream
	// This will make GCM handle displaying the notification instead of the client.
	// Note that "data" payloads (Uniqush's default for GCM/FCM, for historic reasons) can be sent alongside "notification" payloads.
	// - In that case, "data" will be sent to the app, and "notification" will be rendered directly for the user to see.
	if rawNotification, ok := postData[psb.rawNotificationKey]; ok {
		notification, err := validateRawCMData(rawNotification)
		if err != nil {
			return nil, err
		}
		payload.Notification = notification
	}
	if rawData, ok := postData[psb.rawPayloadKey]; ok {
		data, err := validateRawCMData(rawData)
		if err != nil {
			return nil, err
		}
		payload.Data = data
	} else {
		payload.Data = make(map[string]interface{}, len(postData))

		for k, v := range postData {
			if strings.HasPrefix(k, "uniqush.") { // The "uniqush." keys are reserved for uniqush use.
				continue
			}
			switch k {
			case "msggroup", "ttl", "collapse_key":
				continue
			default:
				payload.Data[k] = v
			}
		}
	}

	jpayload, e0 := payload.MarshalSafe()
	if e0 != nil {
		return nil, push.NewErrorf("Error converting payload to JSON: %v", e0)
	}
	return jpayload, nil
}

func extractRegIds(dpList []*push.DeliveryPoint) []string {
	regIds := make([]string, 0, len(dpList))

	for _, dp := range dpList {
		regIds = append(regIds, dp.VolatileData["regid"])
	}
	return regIds
}

func sendErrToEachDP(psp *push.PushServiceProvider, dpList []*push.DeliveryPoint, resQueue chan<- *push.Result, notif *push.Notification, err push.Error) {
	for _, dp := range dpList {
		res := new(push.Result)
		res.Provider = psp
		res.Content = notif

		res.Err = err
		res.Destination = dp
		resQueue <- res
	}
}

func (psb *PushServiceBase) multicast(psp *push.PushServiceProvider, dpList []*push.DeliveryPoint, resQueue chan<- *push.Result, notif *push.Notification) {
	if len(dpList) == 0 {
		return
	}
	regIds := extractRegIds(dpList)

	jpayload, e0 := psb.ToCMPayload(notif, regIds)

	if e0 != nil {
		sendErrToEachDP(psp, dpList, resQueue, notif, e0)
		return
	}

	req, e1 := http.NewRequest("POST", psb.serviceURL, bytes.NewReader(jpayload))
	if req != nil {
		defer req.Body.Close()
	}
	if e1 != nil {
		httpErr := push.NewErrorf("Error constructing HTTP request: %v", e1)
		sendErrToEachDP(psp, dpList, resQueue, notif, httpErr)
		return
	}

	apikey := psp.VolatileData["apikey"]

	req.Header.Set("Authorization", "key="+apikey)
	req.Header.Set("Content-Type", "application/json")

	// Perform a request, using a connection from the connection pool of a shared http.Client instance.
	r, e2 := psb.client.Do(req)
	if r != nil {
		defer r.Body.Close()
	}
	// TODO: Move this into two steps: sending and processing result
	if e2 != nil {
		for _, dp := range dpList {
			res := new(push.Result)
			res.Provider = psp
			res.Content = notif

			res.Destination = dp
			if err, ok := e2.(net.Error); ok {
				// Temporary error. Try to recover
				if err.Temporary() {
					after := 3 * time.Second
					res.Err = push.NewRetryErrorWithReason(psp, dp, notif, after, err)
				}
			} else if err, ok := e2.(*net.DNSError); ok {
				// DNS error, try to recover it by retry
				after := 3 * time.Second
				res.Err = push.NewRetryErrorWithReason(psp, dp, notif, after, err)

			} else {
				res.Err = push.NewErrorf("Unrecoverable HTTP error sending to %s: %v", psb.pushServiceName, e2)
			}
			resQueue <- res
		}
		return
	}
	// TODO: How does this work if there are multiple delivery points in one request to GCM/FCM?
	newAuthToken := r.Header.Get("Update-Client-Auth")
	if newAuthToken != "" && apikey != newAuthToken {
		psp.VolatileData["apikey"] = newAuthToken
		res := new(push.Result)
		res.Provider = psp
		res.Content = notif
		res.Err = push.NewPushServiceProviderUpdate(psp)
		resQueue <- res
	}

	switch r.StatusCode {
	case 500, 503:
		/* TODO extract the retry after field */
		after := 0 * time.Second
		for _, dp := range dpList {
			res := new(push.Result)
			res.Provider = psp
			res.Content = notif
			res.Destination = dp
			err := push.NewRetryError(psp, dp, notif, after)
			res.Err = err
			resQueue <- res
		}
		return
	case 401:
		err := push.NewBadPushServiceProvider(psp)
		res := new(push.Result)
		res.Provider = psp
		res.Content = notif
		res.Err = err
		resQueue <- res
		return
	case 400:
		err := push.NewBadNotification()
		res := new(push.Result)
		res.Provider = psp
		res.Content = notif
		res.Err = err
		resQueue <- res
		return
	}

	contents, err := ioutil.ReadAll(r.Body)
	if err != nil {
		res := new(push.Result)
		res.Provider = psp
		res.Content = notif
		res.Err = push.NewErrorf("Failed to read %s response: %v", psb.initialism, err)
		resQueue <- res
		return
	}

	var result CMResult
	err = json.Unmarshal(contents, &result)

	if err != nil {
		res := new(push.Result)
		res.Provider = psp
		res.Content = notif
		res.Err = push.NewErrorf("Failed to decode %s response: %v", psb.initialism, err)
		resQueue <- res
		return
	}

	psb.handleCMMulticastResults(psp, dpList, resQueue, notif, result.Results)
}

func (psb *PushServiceBase) handleCMMulticastResults(psp *push.PushServiceProvider, dpList []*push.DeliveryPoint, resQueue chan<- *push.Result, notif *push.Notification, results []map[string]string) {
	for i, r := range results {
		if i >= len(dpList) {
			break
		}
		dp := dpList[i]
		if errmsg, ok := r["error"]; ok {
			switch errmsg {
			case "Unavailable":
				after, _ := time.ParseDuration("2s")
				res := new(push.Result)
				res.Provider = psp
				res.Content = notif
				res.Destination = dp
				res.Err = push.NewRetryError(psp, dp, notif, after)
				resQueue <- res
			case "NotRegistered":
				res := new(push.Result)
				res.Provider = psp
				res.Err = push.NewUnsubscribeUpdate(psp, dp)
				res.Content = notif
				res.Destination = dp
				resQueue <- res
			case "InvalidRegistration":
				res := new(push.Result)
				res.Err = push.NewInvalidRegistrationUpdate(psp, dp)
				res.Content = notif
				res.Destination = dp
				resQueue <- res
			default:
				res := new(push.Result)
				res.Err = push.NewErrorf("FCMError: %v", errmsg)
				res.Provider = psp
				res.Content = notif
				res.Destination = dp
				resQueue <- res
			}
		}
		if newregid, ok := r["registration_id"]; ok {
			dp.VolatileData["regid"] = newregid
			res := new(push.Result)
			res.Err = push.NewDeliveryPointUpdate(dp)
			res.Provider = psp
			res.Content = notif
			res.Destination = dp
			resQueue <- res
		}
		if msgid, ok := r["message_id"]; ok {
			res := new(push.Result)
			res.Provider = psp
			res.Content = notif
			res.Destination = dp
			res.MsgID = fmt.Sprintf("%v:%v", psp.Name(), msgid)
			resQueue <- res
		}
	}
}

// Push sends a push notification to 1 or more delivery points in dpQueue asynchronously, and sends results on resQueue.
func (psb *PushServiceBase) Push(psp *push.PushServiceProvider, dpQueue <-chan *push.DeliveryPoint, resQueue chan<- *push.Result, notif *push.Notification) {

	maxNrDst := 1000
	dpList := make([]*push.DeliveryPoint, 0, maxNrDst)
	for dp := range dpQueue {
		if psp.PushServiceName() != dp.PushServiceName() || psp.PushServiceName() != psb.pushServiceName {
			res := new(push.Result)
			res.Provider = psp
			res.Destination = dp
			res.Content = notif
			res.Err = push.NewIncompatibleError()
			resQueue <- res
			continue
		}
		if _, ok := dp.VolatileData["regid"]; ok {
			dpList = append(dpList, dp)
		} else if regid, ok := dp.FixedData["regid"]; ok {
			dp.VolatileData["regid"] = regid
			dpList = append(dpList, dp)
		} else {
			res := new(push.Result)
			res.Provider = psp
			res.Destination = dp
			res.Content = notif
			res.Err = push.NewBadDeliveryPoint(dp)
			resQueue <- res
			continue
		}

		if len(dpList) >= maxNrDst {
			psb.multicast(psp, dpList, resQueue, notif)
			dpList = dpList[:0]
		}
	}
	if len(dpList) > 0 {
		psb.multicast(psp, dpList, resQueue, notif)
	}

	close(resQueue)
}

// Preview will return the JSON payload that this will push to GCM/FCM for previewing (with a placeholder reg ids)
func (psb *PushServiceBase) Preview(notif *push.Notification) ([]byte, push.Error) {
	return psb.ToCMPayload(notif, []string{"placeholderRegId"})
}

// SetErrorReportChan will set the report chan used for asynchronous feedback that is not associated with a request. (does nothing for cloud messaging)
func (psb *PushServiceBase) SetErrorReportChan(errChan chan<- push.Error) {
}

// SetPushServiceConfig is called during initialization to provide the unserialized contents of uniqush.conf. (does nothing for cloud messaging)
func (psb *PushServiceBase) SetPushServiceConfig(c *push.PushServiceConfig) {
}
