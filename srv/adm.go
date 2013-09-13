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
	. "github.com/uniqush/uniqush-push/push"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	admTokenURL   string = "https://api.amazon.com/auth/O2/token"
	admServiceURL string = "https://api.amazon.com/messaging/registrations/"
)

type pspLockResponse struct {
	err error
	psp *PushServiceProvider
}

type pspLockRequest struct {
	psp    *PushServiceProvider
	respCh chan<- *pspLockResponse
}

type admPushService struct {
	pspLock chan *pspLockRequest
}

func newADMPushService() *admPushService {
	ret := new(admPushService)
	ret.pspLock = make(chan *pspLockRequest)
	go admPspLocker(ret.pspLock)
	return ret
}

func InstallADM() {
	psm := GetPushServiceManager()
	psm.RegisterPushServiceType(newADMPushService())
}

func (self *admPushService) Finalize() {}
func (self *admPushService) Name() string {
	return "adm"
}
func (self *admPushService) SetErrorReportChan(errChan chan<- error) {
	return
}

func (self *admPushService) BuildPushServiceProviderFromMap(kv map[string]string, psp *PushServiceProvider) error {
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
		return errors.New("NoClientSecrete")
	}

	return nil
}

func (self *admPushService) BuildDeliveryPointFromMap(kv map[string]string, dp *DeliveryPoint) error {
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
	pspLockMap := make(map[string]*PushServiceProvider, 10)
	for req := range lockChan {
		var ok bool
		var clientid string
		psp := req.psp

		resp := new(pspLockResponse)
		if clientid, ok = psp.FixedData["clientid"]; !ok {
			resp.err = NewBadPushServiceProviderWithDetails(psp, "NoClientID")
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
			if _, ok := resp.err.(*PushServiceProviderUpdate); ok {
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

func requestToken(psp *PushServiceProvider) error {
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
		return NewBadPushServiceProviderWithDetails(psp, "NoClientID")
	}
	if cserect, ok = psp.FixedData["clientsecret"]; !ok {
		return NewBadPushServiceProviderWithDetails(psp, "NoClientSecrete")
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("scope", "messaging:push")
	form.Set("client_id", clientid)
	form.Set("client_secret", cserect)
	req, err := http.NewRequest("POST", admTokenURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return fmt.Errorf("NewRequest error: %v", err)
	}
	defer req.Body.Close()
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Do error: %v", err)
	}

	defer resp.Body.Close()

	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return NewBadPushServiceProviderWithDetails(psp, err.Error())
	}
	if resp.StatusCode != 200 {
		var fail tokenFailObj
		err = json.Unmarshal(content, &fail)
		if err != nil {
			return NewBadPushServiceProviderWithDetails(psp, err.Error())
		}
		reason := strings.ToUpper(fail.Reason)
		switch reason {
		case "INVALID_SCOPE":
			reason = "ADM is not enabled. Enable it on the Amazon Mobile App Distribution Portal"
		}
		return NewBadPushServiceProviderWithDetails(psp, fmt.Sprintf("%v:%v (%v)", resp.StatusCode, reason, fail.Description))
	}

	var succ tokenSuccObj
	err = json.Unmarshal(content, &succ)

	if err != nil {
		return NewBadPushServiceProviderWithDetails(psp, err.Error())
	}

	expire := time.Now().Add(time.Duration(succ.Expire-60) * time.Second)

	psp.VolatileData["expire"] = fmt.Sprintf("%v", expire.Unix())
	psp.VolatileData["token"] = succ.Token
	psp.VolatileData["type"] = succ.Type
	return NewPushServiceProviderUpdate(psp)
}

type admMessage struct {
	Data     map[string]string `json:"data"`
	MsgGroup string            `json:"consolidationKey,omitempty"`
	TTL      int64             `json:"expiresAfter,omitempty"`
	MD5      string            `json:"md5,omitempty"`
}

func notifToMessage(notif *Notification) (msg *admMessage, err error) {
	if notif == nil || len(notif.Data) == 0 {
		err = NewBadNotificationWithDetails("empty notification")
		return
	}

	msg = new(admMessage)
	msg.Data = make(map[string]string, len(notif.Data))
	for k, v := range notif.Data {
		switch k {
		case "msggroup":
			msg.MsgGroup = v
		case "ttl":
			ttl, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				continue
			}
			msg.TTL = ttl
		default:
			msg.Data[k] = v
		}
	}
	if len(msg.Data) == 0 {
		err = NewBadNotificationWithDetails("empty notification")
		return
	}
	return
}

func admURL(dp *DeliveryPoint) (url string, err error) {
	if dp == nil {
		err = fmt.Errorf("nil dp")
		return
	}
	if regid, ok := dp.FixedData["regid"]; ok {
		url = fmt.Sprintf("%v%v/messages", admServiceURL, regid)
	} else {
		err = NewBadDeliveryPointWithDetails(dp, "empty delivery point")
	}
	return
}

func admNewRequest(psp *PushServiceProvider, dp *DeliveryPoint, data []byte) (req *http.Request, err error) {
	var token string
	var ok bool
	if token, ok = psp.VolatileData["token"]; !ok {
		err = NewBadPushServiceProviderWithDetails(psp, "NoToken")
		return
	}
	url, err := admURL(dp)
	if err != nil {
		return
	}

	req, err = http.NewRequest("POST", url, bytes.NewBuffer(data))
	if err != nil {
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

func admSinglePush(psp *PushServiceProvider, dp *DeliveryPoint, data []byte, notif *Notification) (string, error) {
	client := &http.Client{}
	req, err := admNewRequest(psp, dp, data)
	if err != nil {
		return "", err
	}
	defer req.Body.Close()
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	id := resp.Header.Get("x-amzn-RequestId")
	if resp.StatusCode != 200 {
		if resp.StatusCode == 503 || resp.StatusCode == 500 || resp.StatusCode == 429 {
			// By default, we retry after one minute.
			retryAfter := resp.Header.Get("Retry-After")
			retrySecond := 60
			if retryAfter != "" {
				retrySecond, err = strconv.Atoi(retryAfter)
				if err != nil {
					retrySecond = 60
				}
			}
			retryDuration := time.Duration(retrySecond) * time.Second
			err = NewRetryError(psp, dp, notif, retryDuration)
			return id, err
		}

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}

		var fail admPushFailResponse
		err = json.Unmarshal(body, &fail)
		if err != nil {
			err = fmt.Errorf("%v: %v", resp.StatusCode, string(body))
			return "", err
		}

		reason := strings.ToLower(fail.Reason)

		switch reason {
		case "messagetoolarge":
			err = NewBadNotificationWithDetails("MessageTooLarge")
		case "invalidregistrationid":
			err = NewBadDeliveryPointWithDetails(dp, "InvalidRegistrationId")
		case "accesstokenexpired":
			// retry would fix it.
			err = NewRetryError(psp, dp, notif, 10*time.Second)
		default:
			err = fmt.Errorf("%v: %v", resp.StatusCode, fail.Reason)
		}

		return "", err
	}
	return id, nil
}

func (self *admPushService) lockPsp(psp *PushServiceProvider) (*PushServiceProvider, error) {
	respCh := make(chan *pspLockResponse)
	req := &pspLockRequest{
		psp:    psp,
		respCh: respCh,
	}

	self.pspLock <- req

	resp := <-respCh

	return resp.psp, resp.err
}

func (self *admPushService) Push(psp *PushServiceProvider, dpQueue <-chan *DeliveryPoint, resQueue chan<- *PushResult, notif *Notification) {
	defer close(resQueue)
	defer func() {
		for _ = range dpQueue {
		}
	}()

	res := new(PushResult)
	res.Content = notif
	res.Provider = psp

	var err error
	psp, err = self.lockPsp(psp)
	if err != nil {
		res.Err = err
		resQueue <- res
		if _, ok := err.(*PushServiceProviderUpdate); !ok {
			return
		}
	}
	msg, err := notifToMessage(notif)
	if err != nil {
		res.Err = err
		resQueue <- res
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		res.Err = err
		resQueue <- res
		return
	}

	wg := sync.WaitGroup{}

	for dp := range dpQueue {
		wg.Add(1)
		res := new(PushResult)
		res.Content = notif
		res.Provider = psp
		res.Destination = dp
		go func() {
			res.MsgId, res.Err = admSinglePush(psp, dp, data, notif)
			resQueue <- res
			wg.Done()
		}()
	}
	wg.Wait()
}
