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
    new_request_received string = "{\"event\":\"NewRequestReceived\", \"id\":\"%s\"}\r\n"
)

func (w *EventWriter) NewRequestReceived(id string) {
    fmt.Fprintf(w.writer, new_request_received, id)
}

