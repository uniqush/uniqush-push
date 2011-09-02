package uniqush

import (
    "log"
    "http"
    "fmt"
    "time"
)

// There is ONLY ONE WebFrontEnd instance running in one program
// It uses global variables.
// (I know it's bad, but web.go does not support MethodHandler any more)
type WebFrontEnd struct {
    ch chan *Request
    logger *log.Logger
    addr string
}

var (
    webfrontend *WebFrontEnd
)
func NewWebFrontEnd(ch chan *Request, logger *log.Logger, addr string) UniqushFrontEnd {
    f := new(WebFrontEnd)
    f.ch = ch
    f.logger = logger

    webfrontend = f

    if len(addr) == 0 {
        // By default, we only accept localhost connection
        addr = "localhost:9999"
    }
    f.addr = addr
    return f
}

func (f *WebFrontEnd) SetChannel(ch chan *Request) {
    f.ch = ch
}

func (f *WebFrontEnd) SetLogger(logger *log.Logger) {
    f.logger = logger
}

func (f *WebFrontEnd) add_push_service(r *http.Request, id string) {
    a := new(Request)
    a.Action = ACTION_ADD_PUSH_SERVICE_PROVIDER
    a.ID = id

    f.ch <- a
}

const (
    ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL = "/addpsp"
)

func add_push_service_h(w http.ResponseWriter, r *http.Request) {
    id := fmt.Sprintf("%d", time.Nanoseconds())
    fmt.Fprintf(w, "id=%s", id)
    go webfrontend.add_push_service(r, id)
}

func diegoroutine() {
    <-webfrontend.ch
}

func die(w http.ResponseWriter, r *http.Request) {
    fmt.Fprintf(w, "<p>Die</p>")
    go diegoroutine()
}

func (f *WebFrontEnd) Run() {
    http.HandleFunc(ADD_PUSH_SERVICE_PROVIDER_TO_SERVICE_URL, add_push_service_h)
    http.HandleFunc("/die", die)
    http.ListenAndServe(f.addr, nil)
}

