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
    psm *PushServiceManager
}


type NullWriter struct{}

func (f *NullWriter) Write(p []byte) (int, os.Error) {
	return len(p), nil
}

func NewWebFrontEnd(ch chan *Request,
                    logger *Logger,
                    addr string,
                    psm *PushServiceManager) UniqushFrontEnd {
	f := new(WebFrontEnd)
	f.ch = ch
	f.logger = logger
	f.writer = NewEventWriter(new(NullWriter))
	f.stopch = nil
    f.psm = psm

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

func (f *WebFrontEnd) addPushServiceProvider(kv map[string]string,
                                             id, addr string) {
	a := new(Request)
	a.PunchTimestamp()

	a.Action = ACTION_ADD_PUSH_SERVICE_PROVIDER
	a.ID = id
	a.RequestSenderAddr = addr
    var ok bool
	if a.Service, ok = kv["service"]; !ok {
		f.logger.Errorf("[AddPushServiceRequestFail] Requestid=%s From=%s NoServiceName", id, addr)
		f.writer.BadRequest(a, os.NewError("NoServiceName"))
		return
	}

    psp, err := f.psm.BuildPushServiceProviderFromMap(kv)
    if err != nil {
        f.logger.Errorf("[AddPushServiceRequestFail] %v", err)
        f.writer.BadRequest(a, err)
        return
    }
    a.PushServiceProvider = psp
	f.ch <- a
	f.writer.RequestReceived(a)
	f.logger.Infof("[AddPushServiceRequest] Requestid=%s From=%s Service=%s", id, addr, psp.Name())
}

func (f *WebFrontEnd) removePushServiceProvider(kv map[string]string,
                                                id, addr string) {
	a := new(Request)
	a.PunchTimestamp()

	a.Action = ACTION_REMOVE_PUSH_SERVICE_PROVIDER
	a.ID = id
	a.RequestSenderAddr = addr
    var ok bool
	if a.Service, ok = kv["service"]; !ok {
		f.logger.Errorf("[RemovePushServiceRequestFail] Requestid=%s From=%s NoServiceName", id, addr)
		f.writer.BadRequest(a, os.NewError("NoServiceName"))
		return
	}

    psp, err := f.psm.BuildPushServiceProviderFromMap(kv)
    if err != nil {
        f.logger.Errorf("[AddPushServiceRequestFail] %v", err)
        f.writer.BadRequest(a, err)
        return
    }
    a.PushServiceProvider = psp

	f.ch <- a
	f.writer.RequestReceived(a)
	f.logger.Infof("[RemovePushServiceRequest] Requestid=%s From=%s Service=%s", id, addr, psp.Name())
}

func (f *WebFrontEnd) addDeliveryPointToService(kv map[string]string,
                                                id, addr string) {
	a := new(Request)
	a.PunchTimestamp()
	a.Action = ACTION_SUBSCRIBE

	a.ID = id
	a.RequestSenderAddr = addr

    var ok bool
	if a.Service, ok = kv["service"]; !ok {
		f.logger.Errorf("[SubscribeFail] Requestid=%s From=%s NoServiceName", id, addr)
		f.writer.BadRequest(a, os.NewError("NoServiceName"))
		return
	}
    var subscriber string
    if subscriber, ok = kv["subscriber"]; !ok {
		f.logger.Errorf("[SubscribeFail] Requestid=%s From=%s NoSubscriber", id, addr)
		f.writer.BadRequest(a, os.NewError("NoSubscriber"))
		return
	}

	a.Subscribers = make([]string, 1)
	a.Subscribers[0] = subscriber

    dp, err := f.psm.BuildDeliveryPointFromMap(kv)
    if err != nil {
        f.logger.Errorf("[SubscribeFail] %v", err)
        f.writer.BadRequest(a, err)
        return
    }
    a.DeliveryPoint = dp
    f.ch <- a
    f.writer.RequestReceived(a)
    f.logger.Infof("[SubscribeRequest] Requestid=%s From=%s Name=%s", id, addr, dp.Name())
	return
}

func (f *WebFrontEnd) removeDeliveryPointFromService(kv map[string]string,
                                                     id, addr string) {
	a := new(Request)
	a.PunchTimestamp()
	a.Action = ACTION_UNSUBSCRIBE
	a.RequestSenderAddr = addr

	a.ID = id
	a.RequestSenderAddr = addr

    var ok bool
	if a.Service, ok = kv["service"]; !ok {
		f.logger.Errorf("[UnsubscribeFail] Requestid=%s From=%s NoServiceName", id, addr)
		f.writer.BadRequest(a, os.NewError("NoServiceName"))
		return
	}
    var subscriber string
    if subscriber, ok = kv["subscriber"]; !ok {
		f.logger.Errorf("[UnsubscribeFail] Requestid=%s From=%s NoSubscriber", id, addr)
		f.writer.BadRequest(a, os.NewError("NoSubscriber"))
		return
	}

	a.Subscribers = make([]string, 1)
	a.Subscribers[0] = subscriber

    dp, err := f.psm.BuildDeliveryPointFromMap(kv)
    if err != nil {
        f.logger.Errorf("[UnsubscribeFail] %v", err)
        f.writer.BadRequest(a, err)
        return
    }
    a.DeliveryPoint = dp
    f.ch <- a
    f.writer.RequestReceived(a)
    f.logger.Infof("[UnsubscribeRequest] Requestid=%s From=%s Name=%s", id, addr, dp.Name())
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

const (
	ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL    = "/addpsp"
	REMOVE_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL = "/rmpsp"
	ADD_DELIVERY_POINT_TO_SERVICE_URL           = "/subscribe"
	REMOVE_DELIVERY_POINT_FROM_SERVICE_URL      = "/unsubscribe"
	PUSH_NOTIFICATION_URL                       = "/push"
	STOP_PROGRAM_URL                            = "/stop"
)

func (f *WebFrontEnd) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := fmt.Sprintf("%d", time.Nanoseconds())
    r.ParseForm()
    kv := make(map[string]string, len(r.Form))
    for k, v := range r.Form {
        if len(v) > 0 {
            kv[strings.ToLower(k)] = v[0]
        }
    }
    switch (r.URL.Path) {
    case STOP_PROGRAM_URL:
        f.stop()
    case ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL:
        go f.addPushServiceProvider(kv, id, r.RemoteAddr)
    case REMOVE_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL:
        go f.removePushServiceProvider(kv, id, r.RemoteAddr)
    case ADD_DELIVERY_POINT_TO_SERVICE_URL:
        go f.addDeliveryPointToService(kv, id, r.RemoteAddr)
    case REMOVE_DELIVERY_POINT_FROM_SERVICE_URL:
        go f.removeDeliveryPointFromService(kv, id, r.RemoteAddr)
    case PUSH_NOTIFICATION_URL:
        go f.pushNotification(r.Form, id, r.RemoteAddr)
    }
	fmt.Fprintf(w, "id=%s\r\n", id)
}

func (f *WebFrontEnd) Run() {
	f.logger.Configf("[Start] %s", f.addr)
    /*
	http.HandleFunc(ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL, addPushServiceProvider)
	http.HandleFunc(REMOVE_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL, removePushServiceProvider)
	http.HandleFunc(ADD_DELIVERY_POINT_TO_SERVICE_URL, addDeliveryPointToService)
	http.HandleFunc(REMOVE_DELIVERY_POINT_FROM_SERVICE_URL, removeDeliveryPointFromService)
	http.HandleFunc(PUSH_NOTIFICATION_URL, pushNotification)
	http.HandleFunc(STOP_PROGRAM_URL, stopProgram)
    */
    http.Handle(STOP_PROGRAM_URL, f)
    http.Handle(ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL, f)
    http.Handle(ADD_DELIVERY_POINT_TO_SERVICE_URL, f)
    http.Handle(REMOVE_DELIVERY_POINT_FROM_SERVICE_URL, f)
    http.Handle(REMOVE_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL, f)
    http.Handle(PUSH_NOTIFICATION_URL, f)
	err := http.ListenAndServe(f.addr, nil)
	if err != nil {
		f.logger.Fatalf("HTTPServerError \"%v\"", err)
	}
	f.stop()
}
