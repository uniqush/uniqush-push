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

func (f *WebFrontEnd) addPushServiceProvider(form http.Values, id string) {
    a := new(Request)

    a.Action = ACTION_ADD_PUSH_SERVICE_PROVIDER
    a.ID = id
    a.Service = form.Get("servicename")

    if len(a.Service) == 0 {
        f.logger.Printf("[AddPushServiceRequestFail] Requestid=%s NoServiceSpecified", id)
        return
    }

    pspname := form.Get("pushservicetype")

    switch(ServiceNameToID(pspname)) {
    case SRVTYPE_C2DM:
        senderid := form.Get("senderid")
        authtoken := form.Get("authtoken")

        if len(senderid) == 0 {
            f.logger.Printf("[AddPushServiceRequestFail] Requestid=%s NoSenderId", id)
            return
        }
        if len(authtoken) == 0 {
            f.logger.Printf("[AddPushServiceRequestFail] Requestid=%s NoAuthToken", id)
            return
        }
        a.PushServiceProvider = NewC2DMServiceProvider("", senderid, authtoken)

    /* TODO More services support */
    case SRVTYPE_APNS:
        fallthrough
    case SRVTYPE_MPNS:
        fallthrough
    case SRVTYPE_BBPS:
        fallthrough
    default:
        f.logger.Printf("[AddPushServiceRequestFail] Requestid=%s NotImplementedPushServie=%s", id, pspname)
        return
    }

    f.ch <- a
    f.logger.Printf("[AddPushServiceRequest] Requestid=%s Service=%s", id, pspname)
    f.writer.NewRequestReceived(a)
}

const (
    ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL = "/addpsp"
)

func addPushServiceProvider(w http.ResponseWriter, r *http.Request) {
    id := fmt.Sprintf("%d", time.Nanoseconds())

    r.FormValue("servicename")
    form := r.Form
    fmt.Fprintf(w, "id=%s\r\n", id)
    go webfrontend.addPushServiceProvider(form, id)
}

func (f *WebFrontEnd) Run() {
    f.logger.Printf("[Start] %s", f.addr)
    http.HandleFunc(ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL, addPushServiceProvider)
    http.ListenAndServe(f.addr, nil)
}

