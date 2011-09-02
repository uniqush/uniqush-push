package uniqush

import (
    "log"
)

type UniqushFrontEnd interface {
    SetChannel(ch chan *Request)
    SetLogger(logger *log.Logger)

    // writer will be used to report real-time event
    SetEventWriter(writer *EventWriter)
    Run()
}

