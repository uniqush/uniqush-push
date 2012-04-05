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

package push

import (
	"errors"

	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
)

/* FIXME
 * Yes, it is http not https
 * Because:
 *  1) The certificate does not match the host name 
 *      android.apis.google.com
 *  2) Go does not support (until now) user defined
 *      verifier for TLS
 * The user defined verifier feature was submitted
 * and under reviewed:
 * http://codereview.appspot.com/4964043/
 *
 * However, even we can use a fake verifier, there
 * is still a security issue.
 *
 * Hope goole could fix the certificate problem
 * soon, or we have to use C2DM as an unsecure
 * service.
 */
const (
	serviceURL string = "http://android.apis.google.com/c2dm/send"
)

func init() {
	psm := GetPushServiceManager()
	psm.RegisterPushServiceType(NewC2DMPushService())
}

type C2DMPushService struct {
}

func NewC2DMPushService() *C2DMPushService {
	ret := new(C2DMPushService)
	return ret
}

func (p *C2DMPushService) SetAsyncFailureHandler(pf PushFailureHandler) {
}

func (p *C2DMPushService) Finalize() {}

func (p *C2DMPushService) BuildPushServiceProviderFromMap(kv map[string]string,
	psp *PushServiceProvider) error {
	if service, ok := kv["service"]; ok {
		psp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}
	if senderid, ok := kv["senderid"]; ok {
		psp.FixedData["senderid"] = senderid
	} else {
		return errors.New("NoSenderId")
	}

	if authtoken, ok := kv["authtoken"]; ok {
		psp.VolatileData["authtoken"] = authtoken
	} else {
		return errors.New("NoAuthToken")
	}

	return nil
}

func (p *C2DMPushService) BuildDeliveryPointFromMap(kv map[string]string,
	dp *DeliveryPoint) error {
	if service, ok := kv["service"]; ok {
		dp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}
	if sub, ok := kv["subscriber"]; ok {
		dp.FixedData["subscriber"] = sub
	} else {
		return errors.New("NoSubscriber")
	}
	if account, ok := kv["account"]; ok {
		dp.FixedData["account"] = account
	} else {
		return errors.New("NoGoogleAccount")
	}

	if regid, ok := kv["regid"]; ok {
		dp.FixedData["regid"] = regid
	} else {
		return errors.New("NoRegId")
	}

	return nil
}

func (p *C2DMPushService) Name() string {
	return "c2dm"
}

func (p *C2DMPushService) Push(psp *PushServiceProvider,
	dp *DeliveryPoint,
	n *Notification) (string, error) {
	if psp.PushServiceName() != dp.PushServiceName() ||
		psp.PushServiceName() != p.Name() {
		return "", NewPushIncompatibleError(psp, dp, p)
	}

	msg := n.Data
	data := url.Values{}
	data.Set("registration_id", dp.FixedData["regid"])
	if mid, ok := msg["id"]; ok {
		data.Set("collapse_key", mid)
	} else {
		data.Set("collapse_key", msg["msg"])
	}

	for k, v := range msg {
		switch k {
		case "id":
			continue
		default:
			data.Set("data."+k, v)
		}
	}

	req, err := http.NewRequest("POST", serviceURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}

	authtoken := psp.VolatileData["authtoken"]

	req.Header.Set("Authorization", "GoogleLogin auth="+authtoken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r, e20 := http.DefaultClient.Do(req)
	if e20 != nil {
		return "", e20
	}
	refreshpsp := false
	new_auth_token := r.Header.Get("Update-Client-Auth")
	if new_auth_token != "" && authtoken != new_auth_token {
		psp.VolatileData["authtoken"] = new_auth_token
		refreshpsp = true
	}

	switch r.StatusCode {
	case 503:
		/* TODO extract the retry after field */
		after := -1
		var reterr error
		reterr = NewRetryError(after)
		if refreshpsp {
			re := NewRefreshDataError(psp, nil, reterr)
			reterr = re
		}
		return "", reterr
	case 401:
		return "", NewInvalidPushServiceProviderError(psp, errors.New("Invalid Auth Token"))
	}

	contents, e30 := ioutil.ReadAll(r.Body)
	if e30 != nil {
		if refreshpsp {
			re := NewRefreshDataError(psp, nil, e30)
			e30 = re
		}
		return "", e30
	}

	msgid := string(contents)
	msgid = strings.Replace(msgid, "\r", "", -1)
	msgid = strings.Replace(msgid, "\n", "", -1)
	if msgid[:3] == "id=" {
		retid := fmt.Sprintf("c2dm:%s-%s", psp.Name(), msgid[3:])
		if refreshpsp {
			re := NewRefreshDataError(psp, nil, nil)
			return retid, re
		}
		return retid, nil
	}
	switch msgid[6:] {
	case "QuotaExceeded":
		var reterr error
		reterr = NewQuotaExceededError(psp)
		if refreshpsp {
			re := NewRefreshDataError(psp, nil, reterr)
			reterr = re
		}
		return "", reterr
	case "InvalidRegistration":
		var reterr error
		reterr = NewInvalidDeliveryPointError(psp, dp, errors.New("InvalidRegistration"))
		if refreshpsp {
			re := NewRefreshDataError(psp, nil, reterr)
			reterr = re
		}
		return "", reterr
	case "NotRegistered":
		var reterr error
		reterr = NewUnregisteredError(psp, dp)
		if refreshpsp {
			re := NewRefreshDataError(psp, nil, reterr)
			reterr = re
		}
		return "", reterr
	case "MessageTooBig":
		var reterr error
		reterr = NewNotificationTooBigError(psp, dp, n)
		if refreshpsp {
			re := NewRefreshDataError(psp, nil, reterr)
			reterr = re
		}
		return "", reterr
	}
	if refreshpsp {
		re := NewRefreshDataError(psp, nil, errors.New("Unknown Error: "+msgid[6:]))
		return "", re
	}
	return "", errors.New("Unknown Error: " + msgid[6:])
}
