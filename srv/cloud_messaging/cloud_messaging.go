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

 * This contains implementation details common to GCM and FCM.
 * Parts of this will be refactored as new features are added for FCM/GCM.
 */
package cloud_messaging

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

type PushServiceBase struct {
	// There is only one Transport and one Client for connecting to gcm or fcm, shared by the set of PSPs with pushservicetype=fcm (whether or not this is using a sandbox)
	// (Expose functionality of this client through public methods)
	client HTTPClient
	// const: "GCM" or "FCM", for logging
	initialism string
	// const: "uniqush.payload.fcm" or "uniqush.payload.gcm"
	rawPayloadKey string
	// const: GCM/FCM endpoint. "https://..../push"
	serviceURL string
	// const: "gcm" or "fcm", for API requests to uniqush and API responses, as well as logging.
	pushServiceName string
}

// Close all open connections.
func (p *PushServiceBase) Finalize() {
	if client, isClient := p.client.(*http.Client); isClient {
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}
}

func (g *PushServiceBase) OverrideClient(client HTTPClient) {
	g.client = client
}

// Builds a common base.
// Note: Make sure that this can be copied by value (it's a collection of poitners right now).
// If it can no longer be copied by value, // change to an initializer function.
func MakePushServiceBase(initialism string, rawPayloadKey string, serviceURL string, pushServiceName string) PushServiceBase {
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
		client:          client,
		initialism:      initialism,
		rawPayloadKey:   rawPayloadKey,
		serviceURL:      serviceURL,
		pushServiceName: pushServiceName,
	}
}

// Builds an FCM/GCM delivery point from key-value pairs provided by an API call to uniqush-push.
func (p *PushServiceBase) BuildDeliveryPointFromMap(kv map[string]string,
	dp *push.DeliveryPoint) error {
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

// Data used by GCM or FCM. Currently the same.
type CMData struct {
	RegIDs         []string               `json:"registration_ids"`
	CollapseKey    string                 `json:"collapse_key,omitempty"`
	Data           map[string]interface{} `json:"data"` // For compatibility with other GCM platforms(e.g. iOS), should always be a map[string]string
	DelayWhileIdle bool                   `json:"delay_while_idle,omitempty"`
	TimeToLive     uint                   `json:"time_to_live,omitempty"`
}

func (d *CMData) String() string {
	ret, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	return string(ret)
}

// validateRawCMData verifies that the user-provided JSON payload is a valid JSON object.
func validateRawCMData(payload string) (map[string]interface{}, push.PushError) {
	var data map[string]interface{}
	err := json.Unmarshal([]byte(payload), &data)
	if data == nil {
		return nil, push.NewBadNotificationWithDetails(fmt.Sprintf("Could not parse GCM/FCM data: %v", err))
	}
	return data, nil
}

// GCM/FCM result. Currently the same for both GCM and FCM.
type CMResult struct {
	MulticastID  uint64              `json:"multicast_id"`
	Success      uint                `json:"success"`
	Failure      uint                `json:"failure"`
	CanonicalIDs uint                `json:"canonical_ids"`
	Results      []map[string]string `json:"results"`
}

func (self *PushServiceBase) Name() string {
	return self.pushServiceName
}

func (self *PushServiceBase) ToCMPayload(notif *push.Notification, regIds []string) ([]byte, push.PushError) {
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

	if rawData, ok := postData[self.rawPayloadKey]; ok {
		// Could add uniqush.notification.gcm/fcm as another optional payload, to conform to GCM spec: https://developers.google.com/cloud-messaging/http-server-ref#send-downstream
		data, err := validateRawCMData(rawData)
		if err != nil {
			return nil, err
		}
		payload.Data = data
	} else {
		nr_elem := len(postData)
		payload.Data = make(map[string]interface{}, nr_elem)

		for k, v := range postData {
			if strings.HasPrefix(k, "uniqush.") { // The "uniqush." keys are reserved for uniqush use.
				continue
			}
			switch k {
			case "msggroup", "ttl":
				continue
			default:
				payload.Data[k] = v
			}
		}
	}

	jpayload, e0 := util.MarshalJSONUnescaped(payload)
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

func sendErrToEachDP(psp *push.PushServiceProvider, dpList []*push.DeliveryPoint, resQueue chan<- *push.PushResult, notif *push.Notification, err push.PushError) {
	for _, dp := range dpList {
		res := new(push.PushResult)
		res.Provider = psp
		res.Content = notif

		res.Err = err
		res.Destination = dp
		resQueue <- res
	}
}

func (self *PushServiceBase) multicast(psp *push.PushServiceProvider, dpList []*push.DeliveryPoint, resQueue chan<- *push.PushResult, notif *push.Notification) {
	if len(dpList) == 0 {
		return
	}
	regIds := extractRegIds(dpList)

	jpayload, e0 := self.ToCMPayload(notif, regIds)

	if e0 != nil {
		sendErrToEachDP(psp, dpList, resQueue, notif, e0)
		return
	}

	req, e1 := http.NewRequest("POST", self.serviceURL, bytes.NewReader(jpayload))
	if req != nil {
		defer req.Body.Close()
	}
	if e1 != nil {
		http_err := push.NewErrorf("Error constructing HTTP request: %v", e1)
		sendErrToEachDP(psp, dpList, resQueue, notif, http_err)
		return
	}

	apikey := psp.VolatileData["apikey"]

	req.Header.Set("Authorization", "key="+apikey)
	req.Header.Set("Content-Type", "application/json")

	// Perform a request, using a connection from the connection pool of a shared http.Client instance.
	r, e2 := self.client.Do(req)
	if r != nil {
		defer r.Body.Close()
	}
	// TODO: Move this into two steps: sending and processing result
	if e2 != nil {
		for _, dp := range dpList {
			res := new(push.PushResult)
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
				res.Err = push.NewErrorf("Unrecoverable HTTP error sending to GCM/FCM: %v", e2)
			}
			resQueue <- res
		}
		return
	}
	// TODO: How does this work if there are multiple delivery points in one request to GCM/FCM?
	new_auth_token := r.Header.Get("Update-Client-Auth")
	if new_auth_token != "" && apikey != new_auth_token {
		psp.VolatileData["apikey"] = new_auth_token
		res := new(push.PushResult)
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
			res := new(push.PushResult)
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
		res := new(push.PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = err
		resQueue <- res
		return
	case 400:
		err := push.NewBadNotification()
		res := new(push.PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = err
		resQueue <- res
		return
	}

	contents, err := ioutil.ReadAll(r.Body)
	if err != nil {
		res := new(push.PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = push.NewErrorf("Failed to read %s response: %v", self.initialism, err)
		resQueue <- res
		return
	}

	var result CMResult
	err = json.Unmarshal(contents, &result)

	if err != nil {
		res := new(push.PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = push.NewErrorf("Failed to decode %s response: %v", self.initialism, err)
		resQueue <- res
		return
	}

	self.handleCMMulticastResults(psp, dpList, resQueue, notif, result.Results)
}

func (self *PushServiceBase) handleCMMulticastResults(psp *push.PushServiceProvider, dpList []*push.DeliveryPoint, resQueue chan<- *push.PushResult, notif *push.Notification, results []map[string]string) {
	for i, r := range results {
		if i >= len(dpList) {
			break
		}
		dp := dpList[i]
		if errmsg, ok := r["error"]; ok {
			switch errmsg {
			case "Unavailable":
				after, _ := time.ParseDuration("2s")
				res := new(push.PushResult)
				res.Provider = psp
				res.Content = notif
				res.Destination = dp
				res.Err = push.NewRetryError(psp, dp, notif, after)
				resQueue <- res
			case "NotRegistered":
				res := new(push.PushResult)
				res.Provider = psp
				res.Err = push.NewUnsubscribeUpdate(psp, dp)
				res.Content = notif
				res.Destination = dp
				resQueue <- res
			case "InvalidRegistration":
				res := new(push.PushResult)
				res.Err = push.NewInvalidRegistrationUpdate(psp, dp)
				res.Content = notif
				res.Destination = dp
				resQueue <- res
			default:
				res := new(push.PushResult)
				res.Err = push.NewErrorf("GCM/FCMError: %v", errmsg)
				res.Provider = psp
				res.Content = notif
				res.Destination = dp
				resQueue <- res
			}
		}
		if newregid, ok := r["registration_id"]; ok {
			dp.VolatileData["regid"] = newregid
			res := new(push.PushResult)
			res.Err = push.NewDeliveryPointUpdate(dp)
			res.Provider = psp
			res.Content = notif
			res.Destination = dp
			resQueue <- res
		}
		if msgid, ok := r["message_id"]; ok {
			res := new(push.PushResult)
			res.Provider = psp
			res.Content = notif
			res.Destination = dp
			res.MsgId = fmt.Sprintf("%v:%v", psp.Name(), msgid)
			resQueue <- res
		}
	}
}

func (self *PushServiceBase) Push(psp *push.PushServiceProvider, dpQueue <-chan *push.DeliveryPoint, resQueue chan<- *push.PushResult, notif *push.Notification) {

	maxNrDst := 1000
	dpList := make([]*push.DeliveryPoint, 0, maxNrDst)
	for dp := range dpQueue {
		if psp.PushServiceName() != dp.PushServiceName() || psp.PushServiceName() != self.pushServiceName {
			res := new(push.PushResult)
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
			res := new(push.PushResult)
			res.Provider = psp
			res.Destination = dp
			res.Content = notif
			res.Err = push.NewBadDeliveryPoint(dp)
			resQueue <- res
			continue
		}

		if len(dpList) >= maxNrDst {
			self.multicast(psp, dpList, resQueue, notif)
			dpList = dpList[:0]
		}
	}
	if len(dpList) > 0 {
		self.multicast(psp, dpList, resQueue, notif)
	}

	close(resQueue)
}

func (self *PushServiceBase) Preview(notif *push.Notification) ([]byte, push.PushError) {
	return self.ToCMPayload(notif, []string{"placeholderRegId"})
}

func (self *PushServiceBase) SetErrorReportChan(errChan chan<- push.PushError) {
	return
}
