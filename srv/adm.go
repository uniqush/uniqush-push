/*
 * Copyright 2013 Nan Deng
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

package srv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uniqush/uniqush-push/push"
)

const (
	admTokenURL   string = "https://api.amazon.com/auth/O2/token"
	admServiceURL string = "https://api.amazon.com/messaging/registrations/"
)

type pspLockResponse struct {
	err push.PushError
	psp *push.PushServiceProvider
}

type pspLockRequest struct {
	psp    *push.PushServiceProvider
	respCh chan<- *pspLockResponse
}

type admPushService struct {
	pspLock chan *pspLockRequest
}

var _ push.PushServiceType = &admPushService{}

func newADMPushService() *admPushService {
	ret := new(admPushService)
	ret.pspLock = make(chan *pspLockRequest)
	go admPspLocker(ret.pspLock)
	return ret
}

func InstallADM() {
	psm := push.GetPushServiceManager()
	psm.RegisterPushServiceType(newADMPushService())
}

func (self *admPushService) Finalize() {}
func (self *admPushService) Name() string {
	return "adm"
}
func (self *admPushService) SetErrorReportChan(errChan chan<- push.PushError) {
	return
}

func (self *admPushService) BuildPushServiceProviderFromMap(kv map[string]string, psp *push.PushServiceProvider) error {
	if service, ok := kv["service"]; ok && len(service) > 0 {
		psp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}

	if clientid, ok := kv["clientid"]; ok && len(clientid) > 0 {
		psp.FixedData["clientid"] = clientid
	} else {
		return errors.New("NoClientID")
	}

	if clientsecret, ok := kv["clientsecret"]; ok && len(clientsecret) > 0 {
		psp.FixedData["clientsecret"] = clientsecret
	} else {
		return errors.New("NoClientSecret")
	}

	return nil
}

func (self *admPushService) BuildDeliveryPointFromMap(kv map[string]string, dp *push.DeliveryPoint) error {
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
	if regid, ok := kv["regid"]; ok && len(regid) > 0 {
		dp.FixedData["regid"] = regid
	} else {
		return errors.New("NoRegId")
	}

	return nil
}

func admPspLocker(lockChan <-chan *pspLockRequest) {
	pspLockMap := make(map[string]*push.PushServiceProvider, 10)
	for req := range lockChan {
		var ok bool
		var clientid string
		psp := req.psp

		resp := new(pspLockResponse)
		if clientid, ok = psp.FixedData["clientid"]; !ok {
			resp.err = push.NewBadPushServiceProviderWithDetails(psp, "NoClientID")
			req.respCh <- resp
			continue
		}

		if psp, ok = pspLockMap[clientid]; !ok {
			psp = req.psp
			pspLockMap[clientid] = psp
		}
		resp.err = requestToken(psp)
		resp.psp = psp
		if resp.err != nil {
			if _, ok := resp.err.(*push.PushServiceProviderUpdate); ok {
				pspLockMap[clientid] = psp
			} else {
				delete(pspLockMap, clientid)
			}
		}
		req.respCh <- resp
	}
}

type tokenSuccObj struct {
	Token  string `json:"access_token"`
	Expire int    `json:"expires_in"`
	Scope  string `json:"scope"`
	Type   string `json:"token_type"`
}

type tokenFailObj struct {
	Reason      string `json:"error"`
	Description string `json:"error_description"`
}

func requestToken(psp *push.PushServiceProvider) push.PushError {
	var ok bool
	var clientid string
	var cserect string

	if _, ok = psp.VolatileData["token"]; ok {
		if exp, ok := psp.VolatileData["expire"]; ok {
			unixsec, err := strconv.ParseInt(exp, 10, 64)
			if err == nil {
				deadline := time.Unix(unixsec, int64(0))
				if deadline.After(time.Now()) {
					fmt.Printf("We don't need to request another token\n")
					return nil
				}
			}
		}
	}

	if clientid, ok = psp.FixedData["clientid"]; !ok {
		return push.NewBadPushServiceProviderWithDetails(psp, "NoClientID")
	}
	if cserect, ok = psp.FixedData["clientsecret"]; !ok {
		return push.NewBadPushServiceProviderWithDetails(psp, "NoClientSecret")
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("scope", "messaging:push")
	form.Set("client_id", clientid)
	form.Set("client_secret", cserect)
	req, err := http.NewRequest("POST", admTokenURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return push.NewErrorf("NewRequest error: %v", err)
	}
	defer req.Body.Close()
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return push.NewErrorf("Do error: %v", err)
	}

	defer resp.Body.Close()

	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return push.NewBadPushServiceProviderWithDetails(psp, err.Error())
	}
	if resp.StatusCode != 200 {
		var fail tokenFailObj
		err = json.Unmarshal(content, &fail)
		if err != nil {
			return push.NewBadPushServiceProviderWithDetails(psp, err.Error())
		}
		reason := strings.ToUpper(fail.Reason)
		switch reason {
		case "INVALID_SCOPE":
			reason = "ADM is not enabled. Enable it on the Amazon Mobile App Distribution Portal"
		}
		return push.NewBadPushServiceProviderWithDetails(psp, fmt.Sprintf("%v:%v (%v)", resp.StatusCode, reason, fail.Description))
	}

	var succ tokenSuccObj
	err = json.Unmarshal(content, &succ)

	if err != nil {
		return push.NewBadPushServiceProviderWithDetails(psp, err.Error())
	}

	expire := time.Now().Add(time.Duration(succ.Expire-60) * time.Second)

	psp.VolatileData["expire"] = fmt.Sprintf("%v", expire.Unix())
	psp.VolatileData["token"] = succ.Token
	psp.VolatileData["type"] = succ.Type
	return push.NewPushServiceProviderUpdate(psp)
}

type admMessage struct {
	Data     map[string]string `json:"data"`
	MsgGroup string            `json:"consolidationKey,omitempty"`
	TTL      int64             `json:"expiresAfter,omitempty"`
	MD5      string            `json:"md5,omitempty"`
}

func notifToMessage(notif *push.Notification) (msg *admMessage, err push.PushError) {
	if notif == nil || len(notif.Data) == 0 {
		err = push.NewBadNotificationWithDetails("empty notification")
		return
	}

	msg = new(admMessage)
	msg.Data = make(map[string]string, len(notif.Data))
	if msggroup, ok := notif.Data["msggroup"]; ok {
		msg.MsgGroup = msggroup
	}
	if rawTTL, ok := notif.Data["ttl"]; ok {
		ttl, err := strconv.ParseInt(rawTTL, 10, 64)
		if err == nil {
			msg.TTL = ttl
		}
	}
	if rawPayload, ok := notif.Data["uniqush.payload.adm"]; ok {
		jsonErr := json.Unmarshal([]byte(rawPayload), &(msg.Data))
		if jsonErr != nil {
			err = push.NewBadNotificationWithDetails(fmt.Sprintf("invalid uniqush.payload.adm: %v", jsonErr))
			return
		}
	} else {
		for k, v := range notif.Data {
			if k == "msggroup" || k == "ttl" {
				continue
			}
			if strings.HasPrefix(k, "uniqush.") { // keys beginning with "uniqush." are reserved by Uniqush.
				continue
			}
			msg.Data[k] = v
		}
	}
	if len(msg.Data) == 0 {
		err = push.NewBadNotificationWithDetails("empty notification")
		return
	}
	return
}

func admURL(dp *push.DeliveryPoint) (url string, err push.PushError) {
	if dp == nil {
		err = push.NewError("nil dp")
		return
	}
	if regid, ok := dp.FixedData["regid"]; ok {
		url = fmt.Sprintf("%v%v/messages", admServiceURL, regid)
	} else {
		err = push.NewBadDeliveryPointWithDetails(dp, "empty delivery point")
	}
	return
}

func admNewRequest(psp *push.PushServiceProvider, dp *push.DeliveryPoint, data []byte) (req *http.Request, err push.PushError) {
	var token string
	var ok bool
	if token, ok = psp.VolatileData["token"]; !ok {
		err = push.NewBadPushServiceProviderWithDetails(psp, "NoToken")
		return
	}
	url, err := admURL(dp)
	if err != nil {
		return
	}

	req, reqErr := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if reqErr != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-amzn-type-version", "com.amazon.device.messaging.ADMMessage@1.0")
	req.Header.Set("x-amzn-accept-type", "com.amazon.device.messaging.ADMSendResult@1.0")
	req.Header.Set("Authorization", "Bearer "+token)

	return
}

type admPushFailResponse struct {
	Reason string `json:"reason"`
}

func admSinglePush(psp *push.PushServiceProvider, dp *push.DeliveryPoint, data []byte, notif *push.Notification) (string, push.PushError) {
	client := &http.Client{}
	req, err := admNewRequest(psp, dp, data)
	if err != nil {
		return "", err
	}
	defer req.Body.Close()
	resp, httpErr := client.Do(req)
	if httpErr != nil {
		return "", push.NewErrorf("Failed to send adm push: %v", httpErr.Error())
	}
	defer resp.Body.Close()

	id := resp.Header.Get("x-amzn-RequestId")
	if resp.StatusCode != 200 {
		if resp.StatusCode == 503 || resp.StatusCode == 500 || resp.StatusCode == 429 {
			// By default, we retry after one minute.
			retryAfter := resp.Header.Get("Retry-After")
			retrySecond := 60
			if retryAfter != "" {
				var retryErr error
				retrySecond, retryErr = strconv.Atoi(retryAfter)
				if retryErr != nil {
					retrySecond = 60
				}
			}
			retryDuration := time.Duration(retrySecond) * time.Second
			err = push.NewRetryError(psp, dp, notif, retryDuration)
			return id, err
		}

		body, ioErr := ioutil.ReadAll(resp.Body)
		if ioErr != nil {
			return "", push.NewErrorf("Failed to read adm response: %v", err)
		}

		var fail admPushFailResponse
		jsonErr := json.Unmarshal(body, &fail)
		if jsonErr != nil {
			return "", push.NewErrorf("%v: %v", resp.StatusCode, string(body))
		}

		reason := strings.ToLower(fail.Reason)

		switch reason {
		case "messagetoolarge":
			err = push.NewBadNotificationWithDetails("MessageTooLarge")
		case "invalidregistrationid":
			err = push.NewBadDeliveryPointWithDetails(dp, "InvalidRegistrationId")
		case "accesstokenexpired":
			// retry would fix it.
			err = push.NewRetryError(psp, dp, notif, 10*time.Second)
		default:
			err = push.NewErrorf("%v: %v", resp.StatusCode, fail.Reason)
		}

		return "", err
	}
	return id, nil
}

func (self *admPushService) lockPsp(psp *push.PushServiceProvider) (*push.PushServiceProvider, push.PushError) {
	respCh := make(chan *pspLockResponse)
	req := &pspLockRequest{
		psp:    psp,
		respCh: respCh,
	}

	self.pspLock <- req

	resp := <-respCh

	return resp.psp, resp.err
}

func (self *admPushService) notifToJSON(notif *push.Notification) ([]byte, push.PushError) {
	msg, err := notifToMessage(notif)
	if err != nil {
		return nil, err
	}

	data, jsonErr := json.Marshal(msg)
	if jsonErr != nil {
		return nil, push.NewErrorf("Failed to marshal message: %v", jsonErr)
	}
	return data, nil
}

func (self *admPushService) Preview(notif *push.Notification) ([]byte, push.PushError) {
	return self.notifToJSON(notif)
}

func (self *admPushService) Push(psp *push.PushServiceProvider, dpQueue <-chan *push.DeliveryPoint, resQueue chan<- *push.PushResult, notif *push.Notification) {
	defer close(resQueue)
	defer func() {
		for range dpQueue {
		}
	}()

	res := new(push.PushResult)
	res.Content = notif
	res.Provider = psp

	var err push.PushError
	psp, err = self.lockPsp(psp)
	if err != nil {
		res.Err = err
		resQueue <- res
		if _, ok := err.(*push.PushServiceProviderUpdate); !ok {
			return
		}
	}
	data, err := self.notifToJSON(notif)

	if err != nil {
		res.Err = err
		resQueue <- res
		return
	}

	wg := sync.WaitGroup{}

	for dp := range dpQueue {
		wg.Add(1)
		res := new(push.PushResult)
		res.Content = notif
		res.Provider = psp
		res.Destination = dp
		go func(dp *push.DeliveryPoint) {
			res.MsgId, res.Err = admSinglePush(psp, dp, data, notif)
			resQueue <- res
			wg.Done()
		}(dp)
	}
	wg.Wait()
}
