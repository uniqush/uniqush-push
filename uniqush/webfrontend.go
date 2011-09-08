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

	/* TODO More services */
	case SRVTYPE_APNS:
		fallthrough
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
		authtoken := form.Get("authtoken")

		if len(senderid) == 0 {
			f.logger.Errorf("[RemovePushServiceRequestFail] Requestid=%s From=%s NoSenderId", id, addr)
			f.writer.BadRequest(a, os.NewError("NoSenderId"))
			return
		}
		if len(authtoken) == 0 {
			f.logger.Errorf("[RemovePushServiceRequestFail] Requestid=%s From=%s NoAuthToken", id, addr)
			f.writer.BadRequest(a, os.NewError("NoAuthToken"))
			return
		}
		a.PushServiceProvider = NewC2DMServiceProvider("", senderid, authtoken)

	/* TODO More services */
	case SRVTYPE_APNS:
		fallthrough
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
	/* TODO More OSes */
	case OSTYPE_IOS:
		fallthrough
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
	/* TODO More OSes */
	case OSTYPE_IOS:
		fallthrough
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
	a.Service = form.Get("service")

	if len(a.Service) == 0 {
		f.logger.Errorf("[PushNotificationFail] Requestid=%s From=%s NoServiceName", id, addr)
		f.writer.BadRequest(a, os.NewError("NoServiceName"))
		return
	}
	subscribers := form.Get("subscriber")

	if subscribers == "" {
		f.logger.Errorf("[PushNotificationFail] Requestid=%s From=%s NoSubscriber", id, addr)
		f.writer.BadRequest(a, os.NewError("NoSubscriber"))
		return
	}

	a.Subscribers = strings.Split(subscribers, ",")

	a.Notification = new(Notification)

	a.Notification.Message = form.Get("msg")
	if a.Notification.Message == "" {
		f.logger.Errorf("[PushNotificationFail] Requestid=%s From=%s NoMessageBody", id, addr)
		f.writer.BadRequest(a, os.NewError("NoMessageBody"))
		return
	}
	a.Notification.Badge = -1

	badge := form.Get("badge")
	if badge != "" {
		var e os.Error
		a.Notification.Badge, e = strconv.Atoi(badge)
		if e != nil {
			a.Notification.Badge = -1
		}
	}

	a.Notification.Image = form.Get("img")
	a.Notification.Sound = form.Get("sound")
	f.ch <- a

	// XXX Should we include the message body in the log?
	f.logger.Infof("[PushNotificationRequest] Requestid=%s From=%s Service=%s Subscribers=%s Body=\"%s\"", id, addr, a.Service, subscribers, a.Notification.Message)
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
	f.logger.Infof("[Start] %s", f.addr)
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
