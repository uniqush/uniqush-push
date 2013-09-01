/*
 * Copyright 2011-2013 Nan Deng
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
	"crypto/sha1"
	"crypto/tls"
	"errors"
	"fmt"
	. "github.com/uniqush/uniqush-push/push"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	c2dmServiceURL string = "https://android.apis.google.com/c2dm/send"
)

type c2dmPushService struct {
}

func newC2DMPushService() *c2dmPushService {
	ret := new(c2dmPushService)
	return ret
}

func InstallC2DM() {
	psm := GetPushServiceManager()
	psm.RegisterPushServiceType(newC2DMPushService())
}

func (p *c2dmPushService) Finalize() {}

func (p *c2dmPushService) BuildPushServiceProviderFromMap(kv map[string]string,
	psp *PushServiceProvider) error {
	if service, ok := kv["service"]; ok && len(service) > 0 {
		psp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}

	if senderid, ok := kv["senderid"]; ok && len(senderid) > 0 {
		psp.FixedData["senderid"] = senderid
	} else {
		return errors.New("NoSenderID")
	}

	if authtoken, ok := kv["authtoken"]; ok && len(authtoken) > 0 {
		psp.VolatileData["authtoken"] = authtoken
	} else {
		return errors.New("NoAuthToken")
	}
	return nil
}

func (p *c2dmPushService) BuildDeliveryPointFromMap(kv map[string]string,
	dp *DeliveryPoint) error {
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
	} else {
		return errors.New("NoGoogleAccount")
	}

	if regid, ok := kv["regid"]; ok && len(regid) > 0 {
		dp.FixedData["regid"] = regid
	} else {
		return errors.New("NoRegId")
	}

	return nil
}

func (p *c2dmPushService) Name() string {
	return "c2dm"
}

func (p *c2dmPushService) singlePush(psp *PushServiceProvider, dp *DeliveryPoint, n *Notification) (string, error) {
	if psp.PushServiceName() != dp.PushServiceName() || psp.PushServiceName() != p.Name() {
		return "", NewIncompatibleError()
	}

	msg := n.Data
	data := url.Values{}
	regid := dp.FixedData["regid"]
	if len(regid) == 0 {
		reterr := NewBadDeliveryPointWithDetails(dp, "EmptyRegistrationID")
		return "", reterr
	}
	data.Set("registration_id", regid)
	if mid, ok := msg["id"]; ok {
		data.Set("collapse_key", mid)
	} else {
		now := time.Now().UTC()
		ckey := fmt.Sprintf("%v-%v-%v-%v-%v",
			dp.Name(),
			psp.Name(),
			now.Format("Mon Jan 2 15:04:05 -0700 MST 2006"),
			now.Nanosecond(),
			msg["msg"])
		hash := sha1.New()
		hash.Write([]byte(ckey))
		h := make([]byte, 0, 64)
		ckey = fmt.Sprintf("%x", hash.Sum(h))
		data.Set("collapse_key", ckey)
	}

	for k, v := range msg {
		switch k {
		case "id":
			continue
		default:
			data.Set("data."+k, v)
		}
	}

	req, err := http.NewRequest("POST", c2dmServiceURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}

	authtoken := psp.VolatileData["authtoken"]

	req.Header.Set("Authorization", "GoogleLogin auth="+authtoken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	conf := &tls.Config{InsecureSkipVerify: true}
	tr := &http.Transport{TLSClientConfig: conf}
	client := &http.Client{Transport: tr}

	r, e20 := client.Do(req)
	if e20 != nil {
		return "", e20
	}
	defer r.Body.Close()
	new_auth_token := r.Header.Get("Update-Client-Auth")
	if new_auth_token != "" && authtoken != new_auth_token {
		psp.VolatileData["authtoken"] = new_auth_token
		return "", NewPushServiceProviderUpdate(psp)
	}

	switch r.StatusCode {
	case 503:
		fallthrough
	case 500:
		after := 0 * time.Second
		var reterr error
		reterr = NewRetryError(psp, dp, n, after)
		return "", reterr
	case 401:
		return "", NewBadPushServiceProvider(psp)
	case 400:
		return "", NewBadNotification()
	}

	contents, e30 := ioutil.ReadAll(r.Body)
	if e30 != nil {
		return "", e30
	}

	msgid := string(contents)
	msgid = strings.Replace(msgid, "\r", "", -1)
	msgid = strings.Replace(msgid, "\n", "", -1)
	if msgid[:3] == "id=" {
		retid := fmt.Sprintf("c2dm:%s-%s", psp.Name(), msgid[3:])
		return retid, nil
	}
	var reterr error
	switch msgid[6:] {
	case "QuotaExceeded":
		reterr = NewBadPushServiceProviderWithDetails(psp, msgid[6:])
	case "InvalidRegistration":
		reterr = NewBadDeliveryPointWithDetails(dp, msgid[6:])
	case "NotRegistered":
		reterr = NewBadDeliveryPointWithDetails(dp, msgid[6:])
	case "MessageTooBig":
		reterr = NewBadNotificationWithDetails(msgid[6:])
	case "DeviceQuotaExceeded":
		reterr = NewBadDeliveryPointWithDetails(dp, msgid[6:])
	default:
		reterr = errors.New("Unknown Error from C2DM: " + msgid[6:])
	}
	return "", reterr
}

func (self *c2dmPushService) Push(psp *PushServiceProvider, dpQueue <-chan *DeliveryPoint, resQueue chan<- *PushResult, notif *Notification) {
	wg := new(sync.WaitGroup)
	for dp := range dpQueue {
		wg.Add(1)
		go func() {
			res := new(PushResult)
			res.Provider = psp
			res.Destination = dp
			res.Content = notif
			msgid, err := self.singlePush(psp, dp, notif)
			if err != nil {
				res.Err = err
			} else {
				res.MsgId = msgid
			}
			resQueue <- res
			wg.Done()
		}()
	}

	wg.Wait()
	close(resQueue)
}

func (self *c2dmPushService) SetErrorReportChan(errChan chan<- error) {
	return
}
