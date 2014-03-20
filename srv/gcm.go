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
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"time"
	. "github.com/uniqush/uniqush-push/push"
)

const (
	gcmServiceURL string = "https://android.googleapis.com/gcm/send"
)

type gcmPushService struct {
}

func newGCMPushService() *gcmPushService {
	ret := new(gcmPushService)
	return ret
}

func InstallGCM() {
	psm := GetPushServiceManager()
	psm.RegisterPushServiceType(newGCMPushService())
}

func (p *gcmPushService) Finalize() {}

func (p *gcmPushService) BuildPushServiceProviderFromMap(kv map[string]string,
	psp *PushServiceProvider) error {
	if service, ok := kv["service"]; ok && len(service) > 0 {
		psp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}

	if projectid, ok := kv["projectid"]; ok && len(projectid) > 0 {
		psp.FixedData["projectid"] = projectid
	} else {
		return errors.New("NoProjectID")
	}

	if authtoken, ok := kv["apikey"]; ok && len(authtoken) > 0 {
		psp.VolatileData["apikey"] = authtoken
	} else {
		return errors.New("NoAPIKey")
	}

	return nil
}

func (p *gcmPushService) BuildDeliveryPointFromMap(kv map[string]string,
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
	}
	if regid, ok := kv["regid"]; ok && len(regid) > 0 {
		dp.FixedData["regid"] = regid
	} else {
		return errors.New("NoRegId")
	}

	return nil
}

func (p *gcmPushService) Name() string {
	return "gcm"
}

type gcmData struct {
	RegIDs         []string          `json:"registration_ids"`
	CollapseKey    string            `json:"collapse_key,omitempty"`
	Data           map[string]string `json:"data"`
	DelayWhileIdle bool              `json:"delay_while_idle,omitempty"`
	TimeToLive     uint              `json:"time_to_live,omitempty"`
}

func (d *gcmData) String() string {
	ret, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	return string(ret)
}

type gcmResult struct {
	MulticastID  uint64              `json:"multicast_id"`
	Success      uint                `json:"success"`
	Failure      uint                `json:"failure"`
	CanonicalIDs uint                `json:"canonical_ids"`
	Results      []map[string]string `json:"results"`
}

func (self *gcmPushService) multicast(psp *PushServiceProvider, dpList []*DeliveryPoint, resQueue chan<- *PushResult, notif *Notification) {
	if len(dpList) == 0 {
		return
	}

	regIds := make([]string, 0, len(dpList))

	for _, dp := range dpList {
		regIds = append(regIds, dp.VolatileData["regid"])
	}
	msg := notif.Data
	data := new(gcmData)
	data.RegIDs = regIds

	// TTL: default is one hour
	data.TimeToLive = 60 * 60
	data.DelayWhileIdle = false

	if mgroup, ok := msg["msggroup"]; ok {
		data.CollapseKey = mgroup
	} else {
		data.CollapseKey = ""
	}

	nr_elem := len(msg)
	data.Data = make(map[string]string, nr_elem)

	for k, v := range msg {
		switch k {
		case "msggroup":
			continue
		case "ttl":
			ttl, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				continue
			}
			data.TimeToLive = uint(ttl)
		default:
			data.Data[k] = v
		}
	}

	jdata, e0 := json.Marshal(data)
	if e0 != nil {
		for _, dp := range dpList {
			res := new(PushResult)
			res.Provider = psp
			res.Content = notif

			res.Err = e0
			res.Destination = dp
			resQueue <- res
		}
		return
	}

	req, e1 := http.NewRequest("POST", gcmServiceURL, bytes.NewReader(jdata))
	if e1 != nil {
		for _, dp := range dpList {
			res := new(PushResult)
			res.Provider = psp
			res.Content = notif

			res.Err = e1
			res.Destination = dp
			resQueue <- res
		}
		return
	}
	defer req.Body.Close()

	apikey := psp.VolatileData["apikey"]

	req.Header.Set("Authorization", "key="+apikey)
	req.Header.Set("Content-Type", "application/json")

	conf := &tls.Config{InsecureSkipVerify: false}
	tr := &http.Transport{TLSClientConfig: conf}
	client := &http.Client{Transport: tr}

	r, e2 := client.Do(req)
	if e2 != nil {
		for _, dp := range dpList {
			res := new(PushResult)
			res.Provider = psp
			res.Content = notif

			res.Destination = dp
			if err, ok := e2.(net.Error); ok {
				// Temporary error. Try to recover
				if err.Temporary() {
					after := 3 * time.Second
					res.Err = NewRetryErrorWithReason(psp, dp, notif, after, err)
				}
			} else if err, ok := e2.(*net.DNSError); ok {
				// DNS error, try to recover it by retry
				after := 3 * time.Second
				res.Err = NewRetryErrorWithReason(psp, dp, notif, after, err)

			} else {
				res.Err = e2
			}
			resQueue <- res
		}
		return
	}
	defer r.Body.Close()
	new_auth_token := r.Header.Get("Update-Client-Auth")
	if new_auth_token != "" && apikey != new_auth_token {
		psp.VolatileData["apikey"] = new_auth_token
		res := new(PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = NewPushServiceProviderUpdate(psp)
		resQueue <- res
	}

	switch r.StatusCode {
	case 503:
		fallthrough
	case 500:
		/* TODO extract the retry after field */
		after := 0 * time.Second
		for _, dp := range dpList {
			res := new(PushResult)
			res.Provider = psp
			res.Content = notif
			res.Destination = dp
			err := NewRetryError(psp, dp, notif, after)
			res.Err = err
			resQueue <- res
		}
		return
	case 401:
		err := NewBadPushServiceProvider(psp)
		res := new(PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = err
		resQueue <- res
		return
	case 400:
		err := NewBadNotification()
		res := new(PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = err
		resQueue <- res
		return
	}

	contents, err := ioutil.ReadAll(r.Body)
	if err != nil {
		res := new(PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = err
		resQueue <- res
		return
	}

	var result gcmResult
	err = json.Unmarshal(contents, &result)

	if err != nil {
		res := new(PushResult)
		res.Provider = psp
		res.Content = notif
		res.Err = err
		resQueue <- res
		return
	}

	for i, r := range result.Results {
		if i >= len(dpList) {
			break
		}
		dp := dpList[i]
		if errmsg, ok := r["error"]; ok {
			switch errmsg {
			case "Unavailable":
				after, _ := time.ParseDuration("2s")
				res := new(PushResult)
				res.Provider = psp
				res.Content = notif
				res.Destination = dp
				res.Err = NewRetryError(psp, dp, notif, after)
				resQueue <- res
			case "NotRegistered":
				res := new(PushResult)
				res.Provider = psp
				res.Err = NewUnsubscribeUpdate(psp, dp)
				res.Content = notif
				res.Destination = dp
				resQueue <- res
			default:
				res := new(PushResult)
				res.Err = fmt.Errorf("GCMError: %v", errmsg)
				res.Provider = psp
				res.Content = notif
				res.Destination = dp
				resQueue <- res
			}
		}
		if newregid, ok := r["registration_id"]; ok {
			dp.VolatileData["regid"] = newregid
			res := new(PushResult)
			res.Err = NewDeliveryPointUpdate(dp)
			res.Provider = psp
			res.Content = notif
			res.Destination = dp
			resQueue <- res
		}
		if msgid, ok := r["message_id"]; ok {
			res := new(PushResult)
			res.Provider = psp
			res.Content = notif
			res.Destination = dp
			res.MsgId = fmt.Sprintf("%v:%v", psp.Name(), msgid)
			resQueue <- res
		}
	}

}

func (self *gcmPushService) Push(psp *PushServiceProvider, dpQueue <-chan *DeliveryPoint, resQueue chan<- *PushResult, notif *Notification) {

	maxNrDst := 1000
	dpList := make([]*DeliveryPoint, 0, maxNrDst)
	for dp := range dpQueue {
		if psp.PushServiceName() != dp.PushServiceName() || psp.PushServiceName() != self.Name() {
			res := new(PushResult)
			res.Provider = psp
			res.Destination = dp
			res.Content = notif
			res.Err = NewIncompatibleError()
			resQueue <- res
			continue
		}
		if _, ok := dp.VolatileData["regid"]; ok {
			dpList = append(dpList, dp)
		} else if regid, ok := dp.FixedData["regid"]; ok {
			dp.VolatileData["regid"] = regid
			dpList = append(dpList, dp)
		} else {
			res := new(PushResult)
			res.Provider = psp
			res.Destination = dp
			res.Content = notif
			res.Err = NewBadDeliveryPoint(dp)
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

func (self *gcmPushService) SetErrorReportChan(errChan chan<- error) {
	return
}
