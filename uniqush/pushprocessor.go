package uniqush

import (
    "log"
)


type PushProcessor struct {
    loggerEventWriter
    databaseSetter
    pushers []Pusher
}

func NewPushProcessor(logger *log.Logger, writer *EventWriter, dbfront DatabaseFrontDeskIf) RequestProcessor{
    ret := new(PushProcessor)
    ret.SetLogger(logger)
    ret.SetEventWriter(writer)
    ret.SetDatabase(dbfront)
    ret.pushers = make([]Pusher, SRVTYPE_NR_PUSH_SERVICE_TYPE)

    for (i := 0; i < SRVTYPE_NR_PUSH_SERVICE_TYPE; i++) {
        ret.pushers = &NullPusher{}
    }

    return ret
}

