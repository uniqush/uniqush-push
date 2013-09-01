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
	"time"
)

const (
	admTokenURL   string = "https://api.amazon.com/auth/O2/token"
	admServiceURL string = "https://api.amazon.com/messaging/registrations/"
)

type admPushService struct {
}

func newADMPushService() *admPushService {
	ret := new(admPushService)
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
					fmt.Printf("We don't need to request another token")
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
		return NewBadPushServiceProviderWithDetails(psp, fmt.Sprintf("%v:%v", resp.StatusCode, reason))
	}

	var succ tokenSuccObj
	err = json.Unmarshal(content, &succ)

	if err != nil {
		return NewBadPushServiceProviderWithDetails(psp, err.Error())
	}

	fmt.Printf("Obtained the token: %+v\n", succ)

	expire := time.Now().Add(time.Duration(succ.Expire-60) * time.Second)

	psp.VolatileData["expire"] = fmt.Sprintf("%v", expire.Unix())
	psp.VolatileData["token"] = succ.Token
	psp.VolatileData["type"] = succ.Type
	return NewPushServiceProviderUpdate(psp)
}

func (self *admPushService) Push(psp *PushServiceProvider, dpQueue <-chan *DeliveryPoint, resQueue chan<- *PushResult, notif *Notification) {
	defer close(resQueue)
	err := requestToken(psp)
	res := new(PushResult)
	res.Content = notif
	res.Provider = psp

	if err != nil {
		res.Err = err
		resQueue <- res
		if _, ok := err.(*PushServiceProviderUpdate); !ok {
			for _ = range dpQueue {
			}
			return
		}
	}
	for _ = range dpQueue {
	}
}
