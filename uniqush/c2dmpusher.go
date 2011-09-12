/*
 *  Uniqush by Nan Deng
 *  Copyright (C) 2010 Nan Deng
 *
 *  This software is free software; you can redistribute it and/or
 *  modify it under the terms of the GNU Lesser General Public
 *  License as published by the Free Software Foundation; either
 *  version 3.0 of the License, or (at your option) any later version.
 *
 *  This software is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 *  Lesser General Public License for more details.
 *
 *  You should have received a copy of the GNU Lesser General Public
 *  License along with this software; if not, write to the Free Software
 *  Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA  02111-1307  USA
 *
 *  Nan Deng <monnand@gmail.com>
 *
 */

package uniqush

import (
	"os"
	"http"
	"strings"
	"io/ioutil"
	"url"
	//    "url"
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
	service_url string = "http://android.apis.google.com/c2dm/send"
)

type C2DMPusher struct {
	ServiceType
}

func NewC2DMPusher() *C2DMPusher {
	ret := &C2DMPusher{ServiceType{SRVTYPE_C2DM}}
	return ret
}

func (n *Notification) toC2DMFormat() map[string]string {
	/* TODO We need to add other fields */
	ret := make(map[string]string, len(n.Data)+6)
    for k, v := range n.Data {
        ret[k] = v
    }
	return ret
}

func (p *C2DMPusher) Push(sp *PushServiceProvider,
s *DeliveryPoint,
n *Notification) (string, os.Error) {
	if !p.IsCompatible(&s.OSType) {
		return "", &PushErrorIncompatibleOS{p.ServiceType, s.OSType}
	}
	msg := n.toC2DMFormat()
	data := url.Values{}

	data.Set("registration_id", s.RegistrationID())
	/* TODO better collapse key */
	data.Set("collapse_key", msg["msg"])

	for k, v := range msg {
		data.Set("data."+k, v)
	}

	req, err := http.NewRequest("POST", service_url, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "GoogleLogin auth="+sp.AuthToken())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r, e20 := http.DefaultClient.Do(req)
	if e20 != nil {
		return "", e20
	}
	refreshpsp := false
	new_auth_token := r.Header.Get("Update-Client-Auth")
	if new_auth_token != "" && sp.AuthToken() != new_auth_token {
		sp.UpdateAuthToken(new_auth_token)
		refreshpsp = true
	}

	switch r.StatusCode {
	case 503:
		/* TODO extract the retry after field */
		after := -1
		var reterr os.Error
		reterr = NewRetryError(after)
		if refreshpsp {
			re := NewRefreshDataError(sp, nil, reterr)
			reterr = re
		}
		return "", reterr
	case 401:
		return "", NewInvalidPushServiceProviderError(sp)
	}

	contents, e30 := ioutil.ReadAll(r.Body)
	if e30 != nil {
		if refreshpsp {
			re := NewRefreshDataError(sp, nil, e30)
			e30 = re
		}
		return "", e30
	}

	msgid := string(contents)
	msgid = strings.Replace(msgid, "\r", "", -1)
	msgid = strings.Replace(msgid, "\n", "", -1)
	if msgid[:3] == "id=" {
		if refreshpsp {
			re := NewRefreshDataError(sp, nil, nil)
			return msgid[3:], re
		}
		return msgid[3:], nil
	}
	switch msgid[6:] {
	case "QuotaExceeded":
		var reterr os.Error
		reterr = NewQuotaExceededError(sp)
		if refreshpsp {
			re := NewRefreshDataError(sp, nil, reterr)
			reterr = re
		}
		return "", reterr
	case "InvalidRegistration":
		var reterr os.Error
		reterr = NewInvalidDeliveryPointError(sp, s)
		if refreshpsp {
			re := NewRefreshDataError(sp, nil, reterr)
			reterr = re
		}
		return "", reterr
	case "NotRegistered":
		var reterr os.Error
		reterr = NewUnregisteredError(sp, s)
		if refreshpsp {
			re := NewRefreshDataError(sp, nil, reterr)
			reterr = re
		}
		return "", reterr
	case "MessageTooBig":
		var reterr os.Error
		reterr = NewNotificationTooBigError(sp, s, n)
		if refreshpsp {
			re := NewRefreshDataError(sp, nil, reterr)
			reterr = re
		}
		return "", reterr
	}
	if refreshpsp {
		re := NewRefreshDataError(sp, nil, os.NewError("Unknown Error: "+msgid[6:]))
		return "", re
	}
	return "", os.NewError("Unknown Error: " + msgid[6:])
}
