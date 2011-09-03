package uniqush

import (
    "log"
    "http"
    "fmt"
    "time"
    "os"
)

// There is ONLY ONE WebFrontEnd instance running in one program
// It uses global variables.
// (I know it's bad, but web.go does not support MethodHandler any more)
type WebFrontEnd struct {
    ch chan *Request
    logger *log.Logger
    addr string
    writer *EventWriter
}

var (
    webfrontend *WebFrontEnd
)

type NullWriter struct {}

func (f *NullWriter) Write(p []byte) (int, os.Error) {
    return len(p), nil
}

func NewWebFrontEnd(ch chan *Request, logger *log.Logger, addr string) UniqushFrontEnd {
    f := new(WebFrontEnd)
    f.ch = ch
    f.logger = logger
    f.writer = NewEventWriter(new(NullWriter))

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

func (f *WebFrontEnd) SetChannel(ch chan *Request) {
    f.ch = ch
}

func (f *WebFrontEnd) SetLogger(logger *log.Logger) {
    f.logger = logger
}

func (f *WebFrontEnd) addPushServiceProvider(form http.Values, id, addr string) {
    a := new(Request)

    a.Action = ACTION_ADD_PUSH_SERVICE_PROVIDER
    a.ID = id
    a.RequestSenderAddr = addr
    a.Service = form.Get("service")

    if len(a.Service) == 0 {
        f.logger.Printf("[AddPushServiceRequestFail] Requestid=%s From=%s NoServiceName", id, addr)
        f.writer.BadRequest(a, os.NewError("NoServiceName"))
        return
    }

    pspname := form.Get("pushservicetype")

    switch(ServiceNameToID(pspname)) {
    case SRVTYPE_C2DM:
        senderid := form.Get("senderid")
        authtoken := form.Get("authtoken")

        if len(senderid) == 0 {
            f.logger.Printf("[AddPushServiceRequestFail] Requestid=%s From=%s NoSenderId", id, addr)
            f.writer.BadRequest(a, os.NewError("NoSenderId"))
            return
        }
        if len(authtoken) == 0 {
            f.logger.Printf("[AddPushServiceRequestFail] Requestid=%s From=%s NoAuthToken", id, addr)
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
        f.logger.Printf("[AddPushServiceRequestFail] Requestid=%s From=%s UnsupportPushService=%s", id, addr, pspname)
        f.writer.BadRequest(a, os.NewError("UnsupportPushService:" + pspname))
        return
    }

    f.ch <- a
    f.writer.RequestReceived(a)
    f.logger.Printf("[AddPushServiceRequest] Requestid=%s From=%s Service=%s", id, addr, pspname)
}

func (f *WebFrontEnd) addDeliveryPointToService(form http.Values, id, addr string) {
    a := new(Request)
    a.Action = ACTION_SUBSCRIBE

    a.ID = id
    a.RequestSenderAddr = addr
    a.Service = form.Get("service")

    if len(a.Service) == 0 {
        f.logger.Printf("[SubscribeFail] Requestid=%s From=%s NoServiceName", id, addr)
        f.writer.BadRequest(a, os.NewError("NoServiceName"))
        return
    }
    subscriber := form.Get("subscriber")

    if subscriber == "" {
        f.logger.Printf("[SubscribeFail] Requestid=%s From=%s NoSubscriber", id, addr)
        f.writer.BadRequest(a, os.NewError("NoSubscriber"))
        return
    }

    prefered_service := form.Get("preferedservice")
    a.PreferedService = ServiceNameToID(prefered_service)

    if (a.PreferedService == SRVTYPE_UNKNOWN || a.PreferedService < 0) {
        a.PreferedService = -1
    }

    a.Subscribers = make([]string, 1)
    a.Subscribers[0] = subscriber

    dpos := form.Get("os")
    switch (OSNameToID(dpos)) {
    case OSTYPE_ANDROID:
        account := form.Get("account")
        regid := form.Get("regid")
        if account == "" {
            f.logger.Printf("[SubscribeFail] NoGoogleAccount Requestid=%s From=%s", id, addr)
            f.writer.BadRequest(a, os.NewError("NoGoogleAccount"))
            return
        }
        if regid == "" {
            f.logger.Printf("[SubscribeFail] NoRegistrationId Requestid=%s From=%s", id, addr)
            f.writer.BadRequest(a, os.NewError("NoRegistrationId"))
            return
        }
        dp := NewAndroidDeliveryPoint("", account, regid)
        a.DeliveryPoint = dp
        f.ch <- a
        f.writer.RequestReceived(a)
        f.logger.Printf("[SubscribeRequest] Requestid=%s From=%s Account=%s", id, addr, account)
        return
    /* TODO More OSes */
    case OSTYPE_IOS:
        fallthrough
    case OSTYPE_WP:
        fallthrough
    case OSTYPE_BLKBERRY:
        fallthrough
    default:
        f.logger.Printf("[SubscribeFail] Requestid=%s From=%s UnsupportOS=%s", id, addr, dpos)
        f.writer.BadRequest(a, os.NewError("UnsupportOS:" + dpos))
        return
    }
    return
}

func (f *WebFrontEnd) removeDeliveryPointFromService(form http.Values, id, addr string) {
    a := new(Request)
    a.Action = ACTION_UNSUBSCRIBE
    a.RequestSenderAddr = addr

    a.ID = id
    a.Service = form.Get("service")

    if len(a.Service) == 0 {
        f.logger.Printf("[UnsubscribeFail] Requestid=%s From=%s NoServiceName", id, addr)
        f.writer.BadRequest(a, os.NewError("NoServiceName"))
        return
    }
    subscriber := form.Get("subscriber")

    if subscriber == "" {
        f.logger.Printf("[UnsubscribeFail] Requestid=%s From=%s NoSubscriber", id, addr)
        f.writer.BadRequest(a, os.NewError("NoSubscriber"))
        return
    }
    a.Subscribers = make([]string, 1)
    a.Subscribers[0] = subscriber

    dpname := form.Get("deliverypoint")
    if len(dpname) > 0 {
        dp := new(DeliveryPoint)
        dp.Name = dpname
        f.ch <- a
        f.writer.RequestReceived(a)
        f.logger.Printf("[UnsubscribeRequest] Requestid=%s From=%s DeliveryPoint=%s", id, addr, dpname)
        return
    }

    dpos := form.Get("os")
    switch (OSNameToID(dpos)) {
    case OSTYPE_ANDROID:
        account := form.Get("account")
        regid := form.Get("regid")
        if account == "" {
            f.logger.Printf("[UnsubscribeFail] Reuqestid=%s From=%s NoGoogleAccount", id, addr)
            f.writer.BadRequest(a, os.NewError("NoGoogleAccount"))
            return
        }
        if regid == "" {
            f.logger.Printf("[UnsubscribeFail] Requestid=%s From=%s NoRegistrationId", id, addr)
            f.writer.BadRequest(a, os.NewError("NoRegistrationId"))
            return
        }
        dp := NewAndroidDeliveryPoint("", account, regid)
        a.DeliveryPoint = dp
        f.ch <- a
        f.writer.RequestReceived(a)
        f.logger.Printf("[UnsubscribeRequest] Requestid=%s From=%s Account=%s", id, addr, account)
        return
    /* TODO More OSes */
    case OSTYPE_IOS:
        fallthrough
    case OSTYPE_WP:
        fallthrough
    case OSTYPE_BLKBERRY:
        fallthrough
    default:
        f.logger.Printf("[UnsubscribeFail] Requestid=%s From=%s UnsupportOS=%s", id, addr, dpos)
        f.writer.BadRequest(a, os.NewError("UnsupportOS:" + dpos))
        return
    }
    return
}
func addPushServiceProvider(w http.ResponseWriter, r *http.Request) {
    id := fmt.Sprintf("%d", time.Nanoseconds())

    r.FormValue("service")
    form := r.Form
    fmt.Fprintf(w, "id=%s\r\n", id)

    go webfrontend.addPushServiceProvider(form, id, r.RemoteAddr)
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

const (
    ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL = "/addpsp"
    ADD_DELIVERY_POINT_TO_SERVICE = "/subscribe"
    REMOVE_DELIVERY_POINT_FROM_SERVICE = "/unsubscribe"
)

func (f *WebFrontEnd) Run() {
    f.logger.Printf("[Start] %s", f.addr)
    http.HandleFunc(ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL, addPushServiceProvider)
    http.HandleFunc(ADD_DELIVERY_POINT_TO_SERVICE, addDeliveryPointToService)
    http.HandleFunc(REMOVE_DELIVERY_POINT_FROM_SERVICE, removeDeliveryPointFromService)
    http.ListenAndServe(f.addr, nil)
}

