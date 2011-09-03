package uniqush

import (
    "log"
)


type PushProcessor struct {
    loggerEventWriter
    databaseSetter
    pushers []Pusher
    max_nr_gorountines int
}

func NewPushProcessor(logger *log.Logger, writer *EventWriter, dbfront DatabaseFrontDeskIf) RequestProcessor{
    ret := new(PushProcessor)
    ret.SetLogger(logger)
    ret.SetEventWriter(writer)
    ret.SetDatabase(dbfront)
    ret.pushers = make([]Pusher, SRVTYPE_NR_PUSH_SERVICE_TYPE)
    ret.max_nr_gorountines = 1024

    for i := 0; i < SRVTYPE_NR_PUSH_SERVICE_TYPE; i++ {
        ret.pushers[i] = &NullPusher{}
    }

    return ret
}

type successPushLog struct {
    dp *DeliveryPoint
    subid int
    id string
}

func (p *PushProcessor) push(req *Request, service string, subscriber string, subid int) []*successPushLog {
    pspdppairs, err := p.dbfront.GetPushServiceProviderDeliveryPointPairs(service, subscriber)
    if err != nil {
        p.logger.Printf("[PushFail] Service=%s Subscriber=%s DatabaseError %v", service, subscriber, err)
        p.writer.PushFail(req, subscriber, nil, err)
    }
    ret := make([]*successPushLog, len(pspdppairs))

    for i, pdpair := range pspdppairs {
        psp := pdpair.PushServiceProvider
        dp := pdpair.DeliveryPoint

        if psp.ValidServiceType() {
            id, err := p.pushers[psp.ServiceID()].Push(psp, dp, req.Notification)
            if err != nil {
                // We want to be fast. So defer all IO operations
                defer p.logger.Printf("[PushFail] Service=%s Subscriber=%s DeliveryPoint=%s \"%v\"", service, subscriber, dp.Name, err)
                defer p.writer.PushFail(req, subscriber, dp, err)
                ret[i] = nil
                continue
            }
            ret[i] = &successPushLog{dp, subid, id}
        }
    }

    return ret
}

func (p *PushProcessor) pushBulk(req *Request, service string, subscribers []string, finish chan bool) {
    dps := make([]*successPushLog, 0, len(subscribers) * 2)
    for i, sub := range subscribers {
        r := p.push(req, service, sub, i)
        dps = append(dps, r...)
    }

    // Delay IO operations after real push
    for _, pushlog := range dps {
        if pushlog != nil {
            p.logger.Printf("[PushRequest] Success Service=%s Subscriber=%s DeliveryPoint=%s id=%s",
                        service, subscribers[pushlog.subid], pushlog.dp.Name, pushlog.id)
            p.writer.PushSuccess(req, subscribers[pushlog.subid], pushlog.dp, pushlog.id)

        }
    }
    if finish != nil {
        finish <- true
    }
}

func (p *PushProcessor) Process(req *Request) {
    nr_subs_per_goroutine := len(req.Subscribers) / p.max_nr_gorountines
    nr_subs_last_goroutine := len(req.Subscribers) % p.max_nr_gorountines
    nr_goroutines := 0
    finish := make(chan bool)
    pos := 0

    for pos = 0; pos < len(req.Subscribers) - nr_subs_last_goroutine; pos += nr_subs_per_goroutine {
        go p.pushBulk(req, req.Service, req.Subscribers[pos:pos + nr_subs_per_goroutine], finish)
        nr_goroutines += 1
    }
    if pos < len(req.Subscribers) {
        p.pushBulk(req, req.Service, req.Subscribers[pos:], nil)
    }

    for i := 0; i < nr_goroutines; i++ {
        <-finish
    }
}

