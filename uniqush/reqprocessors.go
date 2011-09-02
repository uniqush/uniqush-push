package uniqush

import (
    "log"
)

type RequestProcessor interface {
    SetLogger(logger *log.Logger)
    SetEventWriter(writer *EventWriter)
    Process(req *Request)
}

type ActionPrinter struct {
    logger *log.Logger
}

func NewActionPrinter(logger *log.Logger) RequestProcessor {
    a := new(ActionPrinter)
    a.logger = logger
    return a
}

func (p *ActionPrinter) SetLogger(logger *log.Logger) {
    p.logger = logger
}

func (p *ActionPrinter) Process(r *Request) {
    p.logger.Printf("Action: %d, id: %s\n", r.Action, r.ID)
}

func (p *ActionPrinter) SetEventWriter(writer *EventWriter) {
    return
}

type loggerEventWriter struct {
    logger *log.Logger
    writer *EventWriter
}

func (l *loggerEventWriter) SetLogger(logger *log.Logger) {
    l.logger = logger
}

func (l *loggerEventWriter) SetEventWriter(writer *EventWriter) {
    l.writer = writer
}

type databaseSetter struct {
    dbfront DatabaseFrontDeskIf
}

func (d *databaseSetter) SetDatabase(dbfront DatabaseFrontDeskIf) {
    d.dbfront = dbfront
}

type AddPushServiceProviderProcessor struct {
    loggerEventWriter
    databaseSetter
}

func NewAddPushServiceProviderProcessor(logger *log.Logger, writer *EventWriter, dbfront DatabaseFrontDeskIf) RequestProcessor{
    ret := new(AddPushServiceProviderProcessor)
    ret.SetLogger(logger)
    ret.SetEventWriter(writer)
    ret.SetDatabase(dbfront)

    return ret
}

func (p *AddPushServiceProviderProcessor) Process(req *Request) {
    err := p.dbfront.AddPushServiceProviderToService(req.Service, req.PushServiceProvider)
    if err != nil {
        p.writer.AddPushServiceFail(req, err)
        p.logger.Printf("[AddPushServiceRequestFail] DatabaseError %v", err)
    }
    p.writer.AddPushServiceSuccess(req)
    p.logger.Printf("[AddPushServiceRequest] Success PushServiceProviderID=%s", req.PushServiceProvider.Name)
}

