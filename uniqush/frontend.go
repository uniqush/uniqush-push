package uniqush

import (
    "log"
)

type UniqushFrontEnd interface {
    SetChannel(ch chan *Request)
    SetLogger(logger *log.Logger)
    Run()
}

