package uniqush

import (
    "log"
)

type UniqushBackEndIf interface {
    SetChannel(ch chan *Request)
    SetLogger(logger *log.Logger)
    SetProcessor(action int, proc RequestProcessor)
    Run()
}

type UniqushBackEnd struct {
    procs []RequestProcessor
    ch chan *Request
    logger *log.Logger
}

func NewUniqushBackEnd(ch chan *Request, logger *log.Logger) UniqushBackEndIf {
    b := new(UniqushBackEnd)
    b.ch = ch
    b.logger = logger
    return b
}

func (b *UniqushBackEnd) SetChannel(ch chan *Request) {
    b.ch = ch
}

func (b *UniqushBackEnd) SetLogger(logger *log.Logger) {
    b.logger = logger
}

func (b *UniqushBackEnd) SetProcessor(action int, proc RequestProcessor) {
    if len(b.procs) < NR_ACTIONS {
        a := make([]RequestProcessor, NR_ACTIONS, NR_ACTIONS)
        copy(a, b.procs)
        b.procs = a
    }
    if action < 0 || action >= NR_ACTIONS {
        return
    }
    b.procs[action] = proc
}

func (b *UniqushBackEnd) Run() {
    if len(b.procs) < NR_ACTIONS {
        return
    }
    for {
        r := <-b.ch

        if r.Action < 0 || r.Action >= NR_ACTIONS {
            continue
        }
        p := b.procs[r.Action]
        go p.Process(r)
    }
}

