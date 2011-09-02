package uniqush

import (
    "io"
    "fmt"
)

type EventWriter struct {
    writer io.Writer
}

func NewEventWriter(writer io.Writer) *EventWriter {
    w := new(EventWriter)
    w.writer = writer
    return w
}

const (
    new_request_received string = "{\"event\":\"NewRequestReceived\", \"request\":\"%s\"}\r\n"
)

func jsonRequest(req *Request) string{
    ret := fmt.Sprintf("{\"id\":%s, \"action\":%s}", req.ID, req.ActionName())
    return ret
}

func (w *EventWriter) NewRequestReceived(req *Request) {
    fmt.Fprintf(w.writer, new_request_received, jsonRequest(req))
}

