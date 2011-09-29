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

package uniqush

import (
	"http"
	"fmt"
	"time"
	"os"
	"strings"
	"strconv"
	"url"
)

// There is ONLY ONE WebFrontEnd instance running in one program
// It uses global variables.
// (I know it's bad, but web.go does not support MethodHandler any more)
type WebFrontEnd struct {
	ch     chan<- *Request
	logger *Logger
	addr   string
	writer *EventWriter
	stopch chan<- bool
}

var (
	webfrontend *WebFrontEnd
)

type NullWriter struct{}

func (f *NullWriter) Write(p []byte) (int, os.Error) {
	return len(p), nil
}

func NewWebFrontEnd(ch chan *Request, logger *Logger, addr string) UniqushFrontEnd {
	f := new(WebFrontEnd)
	f.ch = ch
	f.logger = logger
	f.writer = NewEventWriter(new(NullWriter))
	f.stopch = nil

	webfrontend = f

	if len(addr) == 0 {
		// By default, we only accept localhost connection
		addr = "localhost:9999"
	}
	f.addr = addr
	return f
}

func (f *WebFrontEnd) SetEventWriter(writer *EventWriter) {
	f.writer = writer
}

func (f *WebFrontEnd) SetChannel(ch chan<- *Request) {
	f.ch = ch
}

func (f *WebFrontEnd) SetStopChannel(ch chan<- bool) {
	f.stopch = ch
}

func (f *WebFrontEnd) stop() {
	if f.stopch != nil {
		f.stopch <- true
	} else {
		os.Exit(0)
	}
}

func (f *WebFrontEnd) SetLogger(logger *Logger) {
	f.logger = logger
}

func (f *WebFrontEnd) addPushServiceProvider(form url.Values, id, addr string) {
	a := new(Request)
	a.PunchTimestamp()

	a.Action = ACTION_ADD_PUSH_SERVICE_PROVIDER
	a.ID = id
	a.RequestSenderAddr = addr
	a.Service = form.Get("service")

	if len(a.Service) == 0 {
		f.logger.Errorf("[AddPushServiceRequestFail] Requestid=%s From=%s NoServiceName", id, addr)
		f.writer.BadRequest(a, os.NewError("NoServiceName"))
		return
	}

	pspname := form.Get("pushservicetype")

	switch ServiceNameToID(pspname) {
	case SRVTYPE_C2DM:
		senderid := form.Get("senderid")
		authtoken := form.Get("authtoken")

		if len(senderid) == 0 {
			f.logger.Errorf("[AddPushServiceRequestFail] Requestid=%s From=%s NoSenderId", id, addr)
			f.writer.BadRequest(a, os.NewError("NoSenderId"))
			return
		}
		if len(authtoken) == 0 {
			f.logger.Errorf("[AddPushServiceRequestFail] Requestid=%s From=%s NoAuthToken", id, addr)
			f.writer.BadRequest(a, os.NewError("NoAuthToken"))
			return
		}
		a.PushServiceProvider = NewC2DMServiceProvider("", senderid, authtoken)

	case SRVTYPE_APNS:
        certfile := form.Get("cert")
        keyfile := form.Get("key")
        sandbox := form.Get("sandbox")
        is_sandbox := false

        if len(certfile) == 0 {
            f.logger.Errorf("[AddPushServiceRequestFail] Requestid=%s From=%s NoCertificate", id, addr)
			f.writer.BadRequest(a, os.NewError("NoCertificate"))
            return
        }
        if len(keyfile) == 0 {
            f.logger.Errorf("[AddPushServiceRequestFail] Requestid=%s From=%s NoPrivateKey", id, addr)
			f.writer.BadRequest(a, os.NewError("NoPrivateKey"))
            return
        }
        if len(sandbox) != 0 && sandbox == "true" {
            is_sandbox = true
        }
        a.PushServiceProvider = NewAPNSServiceProvider("", certfile, keyfile, is_sandbox)
	/* TODO More services */
	case SRVTYPE_MPNS:
		fallthrough
	case SRVTYPE_BBPS:
		fallthrough
	default:
		f.logger.Errorf("[AddPushServiceRequestFail] Requestid=%s From=%s UnsupportPushService=%s", id, addr, pspname)
		f.writer.BadRequest(a, os.NewError("UnsupportPushService:"+pspname))
		return
	}

	f.ch <- a
	f.writer.RequestReceived(a)
	f.logger.Infof("[AddPushServiceRequest] Requestid=%s From=%s Service=%s", id, addr, pspname)
}

func (f *WebFrontEnd) removePushServiceProvider(form url.Values, id, addr string) {
	a := new(Request)
	a.PunchTimestamp()

	a.Action = ACTION_REMOVE_PUSH_SERVICE_PROVIDER
	a.ID = id
	a.RequestSenderAddr = addr
	a.Service = form.Get("service")

	if len(a.Service) == 0 {
		f.logger.Errorf("[RemovePushServiceRequestFail] Requestid=%s From=%s NoServiceName", id, addr)
		f.writer.BadRequest(a, os.NewError("NoServiceName"))
		return
	}

	pspid := form.Get("pushserviceid")
	if pspid != "" {
		a.PushServiceProvider = new(PushServiceProvider)
		a.PushServiceProvider.Name = pspid
		f.ch <- a
		f.writer.RequestReceived(a)
		f.logger.Infof("[RemovePushServiceRequest] Requestid=%s From=%s ServiceId=%s", id, addr, pspid)
		return
	}

	pspname := form.Get("pushservicetype")

	switch ServiceNameToID(pspname) {
	case SRVTYPE_C2DM:
		senderid := form.Get("senderid")
		//authtoken := form.Get("authtoken")

		if len(senderid) == 0 {
			f.logger.Errorf("[RemovePushServiceRequestFail] Requestid=%s From=%s NoSenderId", id, addr)
			f.writer.BadRequest(a, os.NewError("NoSenderId"))
			return
		}
        /*
		if len(authtoken) == 0 {
			f.logger.Errorf("[RemovePushServiceRequestFail] Requestid=%s From=%s NoAuthToken", id, addr)
			f.writer.BadRequest(a, os.NewError("NoAuthToken"))
			return
		}
        */
		a.PushServiceProvider = NewC2DMServiceProvider("", senderid, "")

	case SRVTYPE_APNS:
        certfile := form.Get("cert")
        keyfile := form.Get("key")
        sandbox := form.Get("sandbox")
        is_sandbox := false

        if len(certfile) == 0 {
            f.logger.Errorf("[AddPushServiceRequestFail] Requestid=%s From=%s NoCertificate", id, addr)
			f.writer.BadRequest(a, os.NewError("NoCertificate"))
            return
        }
        if len(keyfile) == 0 {
            f.logger.Errorf("[AddPushServiceRequestFail] Requestid=%s From=%s NoPrivateKey", id, addr)
			f.writer.BadRequest(a, os.NewError("NoPrivateKey"))
            return
        }
        if len(sandbox) != 0 && sandbox == "true" {
            is_sandbox = true
        }
        a.PushServiceProvider = NewAPNSServiceProvider("", certfile, keyfile, is_sandbox)
	/* TODO More services */
	case SRVTYPE_MPNS:
		fallthrough
	case SRVTYPE_BBPS:
		fallthrough
	default:
		f.logger.Errorf("[RemovePushServiceRequestFail] Requestid=%s From=%s UnsupportPushService=%s", id, addr, pspname)
		f.writer.BadRequest(a, os.NewError("UnsupportPushService:"+pspname))
		return
	}

	f.ch <- a
	f.writer.RequestReceived(a)
	f.logger.Infof("[RemovePushServiceRequest] Requestid=%s From=%s Service=%s", id, addr, pspname)
}

func (f *WebFrontEnd) addDeliveryPointToService(form url.Values, id, addr string) {
	a := new(Request)
	a.PunchTimestamp()
	a.Action = ACTION_SUBSCRIBE

	a.ID = id
	a.RequestSenderAddr = addr
	a.Service = form.Get("service")

	if len(a.Service) == 0 {
		f.logger.Errorf("[SubscribeFail] Requestid=%s From=%s NoServiceName", id, addr)
		f.writer.BadRequest(a, os.NewError("NoServiceName"))
		return
	}
	subscriber := form.Get("subscriber")

	if subscriber == "" {
		f.logger.Errorf("[SubscribeFail] Requestid=%s From=%s NoSubscriber", id, addr)
		f.writer.BadRequest(a, os.NewError("NoSubscriber"))
		return
	}

	prefered_service := form.Get("preferedservice")
	a.PreferedService = ServiceNameToID(prefered_service)

	if a.PreferedService == SRVTYPE_UNKNOWN || a.PreferedService < 0 {
		a.PreferedService = -1
	}

	a.Subscribers = make([]string, 1)
	a.Subscribers[0] = subscriber

	dpos := form.Get("os")
	switch OSNameToID(dpos) {
	case OSTYPE_ANDROID:
		account := form.Get("account")
		regid := form.Get("regid")
		if account == "" {
			f.logger.Errorf("[SubscribeFail] NoGoogleAccount Requestid=%s From=%s", id, addr)
			f.writer.BadRequest(a, os.NewError("NoGoogleAccount"))
			return
		}
		if regid == "" {
			f.logger.Errorf("[SubscribeFail] NoRegistrationId Requestid=%s From=%s", id, addr)
			f.writer.BadRequest(a, os.NewError("NoRegistrationId"))
			return
		}
		dp := NewAndroidDeliveryPoint("", account, regid)
		a.DeliveryPoint = dp
		f.ch <- a
		f.writer.RequestReceived(a)
		f.logger.Infof("[SubscribeRequest] Requestid=%s From=%s Account=%s", id, addr, account)
		return
	case OSTYPE_IOS:
        devtoken := form.Get("devtoken")
        if devtoken == "" {
			f.logger.Errorf("[SubscribeFail] NoDeviceToken Requestid=%s From=%s", id, addr)
			f.writer.BadRequest(a, os.NewError("NoDeviceToken"))
        }
        dp := NewIOSDeliveryPoint("", devtoken)
		a.DeliveryPoint = dp
		f.ch <- a
		f.writer.RequestReceived(a)
		f.logger.Infof("[SubscribeRequest] Requestid=%s From=%s Devtoken=%s", id, addr, devtoken)
	/* TODO More OSes */
	case OSTYPE_WP:
		fallthrough
	case OSTYPE_BLKBERRY:
		fallthrough
	default:
		f.logger.Errorf("[SubscribeFail] Requestid=%s From=%s UnsupportOS=%s", id, addr, dpos)
		f.writer.BadRequest(a, os.NewError("UnsupportOS:"+dpos))
		return
	}
	return
}

func (f *WebFrontEnd) removeDeliveryPointFromService(form url.Values, id, addr string) {
	a := new(Request)
	a.PunchTimestamp()
	a.Action = ACTION_UNSUBSCRIBE
	a.RequestSenderAddr = addr

	a.ID = id
	a.Service = form.Get("service")

	if len(a.Service) == 0 {
		f.logger.Errorf("[UnsubscribeFail] Requestid=%s From=%s NoServiceName", id, addr)
		f.writer.BadRequest(a, os.NewError("NoServiceName"))
		return
	}
	subscriber := form.Get("subscriber")

	if subscriber == "" {
		f.logger.Errorf("[UnsubscribeFail] Requestid=%s From=%s NoSubscriber", id, addr)
		f.writer.BadRequest(a, os.NewError("NoSubscriber"))
		return
	}
	a.Subscribers = make([]string, 1)
	a.Subscribers[0] = subscriber

	dpname := form.Get("deliverypointid")
	if len(dpname) > 0 {
		dp := new(DeliveryPoint)
		dp.Name = dpname
		a.DeliveryPoint = dp
		f.ch <- a
		f.writer.RequestReceived(a)
		f.logger.Infof("[UnsubscribeRequest] Requestid=%s From=%s DeliveryPoint=%s", id, addr, dpname)
		return
	}

	dpos := form.Get("os")
	switch OSNameToID(dpos) {
	case OSTYPE_ANDROID:
		account := form.Get("account")
		regid := form.Get("regid")
		if account == "" {
			f.logger.Errorf("[UnsubscribeFail] Reuqestid=%s From=%s NoGoogleAccount", id, addr)
			f.writer.BadRequest(a, os.NewError("NoGoogleAccount"))
			return
		}
		if regid == "" {
			f.logger.Errorf("[UnsubscribeFail] Requestid=%s From=%s NoRegistrationId", id, addr)
			f.writer.BadRequest(a, os.NewError("NoRegistrationId"))
			return
		}
		dp := NewAndroidDeliveryPoint("", account, regid)
		a.DeliveryPoint = dp
		f.ch <- a
		f.writer.RequestReceived(a)
		f.logger.Infof("[UnsubscribeRequest] Requestid=%s From=%s Account=%s", id, addr, account)
		return
	case OSTYPE_IOS:
        devtoken := form.Get("devtoken")
        if devtoken == "" {
			f.logger.Errorf("[UnsubscribeFail] NoDeviceToken Requestid=%s From=%s", id, addr)
			f.writer.BadRequest(a, os.NewError("NoDeviceToken"))
        }
        dp := NewIOSDeliveryPoint("", devtoken)
		a.DeliveryPoint = dp
		f.ch <- a
		f.writer.RequestReceived(a)
		f.logger.Infof("[UnsubscribeRequest] Requestid=%s From=%s Devtoken=%s", id, addr, devtoken)
	/* TODO More OSes */
	case OSTYPE_WP:
		fallthrough
	case OSTYPE_BLKBERRY:
		fallthrough
	default:
		f.logger.Errorf("[UnsubscribeFail] Requestid=%s From=%s UnsupportOS=%s", id, addr, dpos)
		f.writer.BadRequest(a, os.NewError("UnsupportOS:"+dpos))
		return
	}
	return
}

func (f *WebFrontEnd) pushNotification(form url.Values, id, addr string) {
	a := new(Request)
	a.PunchTimestamp()
	a.Action = ACTION_PUSH
	a.RequestSenderAddr = addr

	a.ID = id
	a.Notification = NewEmptyNotification()
    var subscribers string

    for k, v := range form {
        if len(v) <= 0 {
            continue
        }
        switch (k) {
        case "service":
            a.Service = v[0]
        case "subscribers": fallthrough
        case "subscriber":
            subscribers = v[0]
            a.Subscribers = strings.Split(v[0], ",")
        case "message":
	        a.Notification.Data["msg"] = v[0]
        case "badge":
            if v[0] != "" {
                var e os.Error
                _, e = strconv.Atoi(v[0])
                if e == nil {
                    a.Notification.Data["badge"] = v[0]
                }
            }
        case "image":
            a.Notification.Data["img"] = v[0]
        default:
            a.Notification.Data[k] = v[0]
        }
    }

	if len(a.Service) == 0 {
		f.logger.Errorf("[PushNotificationFail] Requestid=%s From=%s NoServiceName", id, addr)
		f.writer.BadRequest(a, os.NewError("NoServiceName"))
		return
	}
	if len(a.Subscribers) == 0 {
		f.logger.Errorf("[PushNotificationFail] Requestid=%s From=%s NoSubscriber", id, addr)
		f.writer.BadRequest(a, os.NewError("NoSubscriber"))
		return
	}
	if a.Notification.IsEmpty() {
		f.logger.Errorf("[PushNotificationFail] Requestid=%s From=%s EmptyData", id, addr)
		f.writer.BadRequest(a, os.NewError("NoMessageBody"))
		return
	}
	f.ch <- a
	// XXX Should we include the message body in the log?
	f.logger.Infof("[PushNotificationRequest] Requestid=%s From=%s Service=%s Subscribers=%s Body=\"%s\"", id, addr, a.Service, subscribers, a.Notification.Data["msg"])
    f.logger.Debugf("[PushNotificationRequest] Data=%v", a.Notification.Data)
	f.writer.RequestReceived(a)
}

func addPushServiceProvider(w http.ResponseWriter, r *http.Request) {
	id := fmt.Sprintf("%d", time.Nanoseconds())

	r.FormValue("service")
	form := r.Form
	fmt.Fprintf(w, "id=%s\r\n", id)

	go webfrontend.addPushServiceProvider(form, id, r.RemoteAddr)
}

func removePushServiceProvider(w http.ResponseWriter, r *http.Request) {
	id := fmt.Sprintf("%d", time.Nanoseconds())

	r.FormValue("service")
	form := r.Form
	fmt.Fprintf(w, "id=%s\r\n", id)

	go webfrontend.removePushServiceProvider(form, id, r.RemoteAddr)
}

func addDeliveryPointToService(w http.ResponseWriter, r *http.Request) {
	id := fmt.Sprintf("%d", time.Nanoseconds())

	r.FormValue("service")
	form := r.Form
	fmt.Fprintf(w, "id=%s\r\n", id)

	go webfrontend.addDeliveryPointToService(form, id, r.RemoteAddr)
}

func removeDeliveryPointFromService(w http.ResponseWriter, r *http.Request) {
	id := fmt.Sprintf("%d", time.Nanoseconds())

	r.FormValue("service")
	form := r.Form
	fmt.Fprintf(w, "id=%s\r\n", id)

	go webfrontend.removeDeliveryPointFromService(form, id, r.RemoteAddr)
}

func stopProgram(w http.ResponseWriter, r *http.Request) {
	// TODO Add some authentication method
	webfrontend.stop()
}

func pushNotification(w http.ResponseWriter, r *http.Request) {
	id := fmt.Sprintf("%d", time.Nanoseconds())

	r.FormValue("service")
	form := r.Form
	fmt.Fprintf(w, "id=%s\r\n", id)
	go webfrontend.pushNotification(form, id, r.RemoteAddr)
}

const (
	ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL    = "/addpsp"
	REMOVE_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL = "/rmpsp"
	ADD_DELIVERY_POINT_TO_SERVICE_URL           = "/subscribe"
	REMOVE_DELIVERY_POINT_FROM_SERVICE_URL      = "/unsubscribe"
	PUSH_NOTIFICATION_URL                       = "/push"
	STOP_PROGRAM_URL                            = "/stop"
)

func (f *WebFrontEnd) Run() {
	f.logger.Configf("[Start] %s", f.addr)
	http.HandleFunc(ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL, addPushServiceProvider)
	http.HandleFunc(REMOVE_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL, removePushServiceProvider)
	http.HandleFunc(ADD_DELIVERY_POINT_TO_SERVICE_URL, addDeliveryPointToService)
	http.HandleFunc(REMOVE_DELIVERY_POINT_FROM_SERVICE_URL, removeDeliveryPointFromService)
	http.HandleFunc(STOP_PROGRAM_URL, stopProgram)
	http.HandleFunc(PUSH_NOTIFICATION_URL, pushNotification)
	err := http.ListenAndServe(f.addr, nil)
	if err != nil {
		f.logger.Fatalf("HTTPServerError \"%v\"", err)
	}
	f.stop()
}
