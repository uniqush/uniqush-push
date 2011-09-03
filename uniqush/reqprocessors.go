/*
 *  Uniqush by Nan Deng
 *  Copyright (C) 2010 Nan Deng
 *
 *  This software is free software; you can redistribute it and/or
 *  modify it under the terms of the GNU Lesser General Public
 *  License as published by the Free Software Foundation; either
 *  version 3.0 of the License, or (at your option) any later version.
 *
 *  This software is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 *  Lesser General Public License for more details.
 *
 *  You should have received a copy of the GNU Lesser General Public
 *  License along with this software; if not, write to the Free Software
 *  Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA  02111-1307  USA
 *
 *  Nan Deng <monnand@gmail.com>
 *
 */

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
    p.logger.Printf("Action: %d-%s, id: %s\n", r.Action, r.ActionName(), r.ID)
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

type RemovePushServiceProviderProcessor struct {
    loggerEventWriter
    databaseSetter
}

func NewRemovePushServiceProviderProcessor(logger *log.Logger, writer *EventWriter, dbfront DatabaseFrontDeskIf) RequestProcessor{
    ret := new(RemovePushServiceProviderProcessor)
    ret.SetLogger(logger)
    ret.SetEventWriter(writer)
    ret.SetDatabase(dbfront)

    return ret
}

func (p *RemovePushServiceProviderProcessor) Process(req *Request) {
    err := p.dbfront.RemovePushServiceProviderFromService(req.Service, req.PushServiceProvider)
    if err != nil {
        p.writer.RemovePushServiceFail(req, err)
        p.logger.Printf("[RemovePushServiceRequestFail] DatabaseError %v", err)
    }
    p.writer.RemovePushServiceSuccess(req)
    p.logger.Printf("[RemovePushServiceRequest] Success PushServiceProviderID=%s", req.PushServiceProvider.Name)
}

type SubscribeProcessor struct {
    loggerEventWriter
    databaseSetter
}

func NewSubscribeProcessor(logger *log.Logger, writer *EventWriter, dbfront DatabaseFrontDeskIf) RequestProcessor{
    ret := new(SubscribeProcessor)
    ret.SetLogger(logger)
    ret.SetEventWriter(writer)
    ret.SetDatabase(dbfront)

    return ret
}

func (p *SubscribeProcessor) Process(req *Request) {
    if len(req.Subscribers) == 0 {
        return
    }
    psp, err := p.dbfront.AddDeliveryPointToService(req.Service,
                                                    req.Subscribers[0],
                                                    req.DeliveryPoint,
                                                    req.PreferedService)
    if err != nil {
        p.writer.SubscribeFail(req, err)
        p.logger.Printf("[SubscribeRequestFail] DatabaseError %v", err)
    }
    p.writer.SubscribeSuccess(req)
    p.logger.Printf("[SubscribeRequest] Success DeliveryPoint=%s PushServiceProvider=%s",
                    req.DeliveryPoint.Name, psp.Name)
}

type UnsubscribeProcessor struct {
    loggerEventWriter
    databaseSetter
}

func NewUnsubscribeProcessor(logger *log.Logger, writer *EventWriter, dbfront DatabaseFrontDeskIf) RequestProcessor{
    ret := new(UnsubscribeProcessor)
    ret.SetLogger(logger)
    ret.SetEventWriter(writer)
    ret.SetDatabase(dbfront)

    return ret
}

func (p *UnsubscribeProcessor) Process(req *Request) {
    if len(req.Subscribers) == 0 || req.DeliveryPoint == nil{
        p.logger.Printf("[SubscribeRequestFail] Nil Pointer")
        return
    }
    err := p.dbfront.RemoveDeliveryPointFromService(req.Service,
                                                    req.Subscribers[0],
                                                    req.DeliveryPoint)
    if err != nil {
        p.writer.SubscribeFail(req, err)
        p.logger.Printf("[SubscribeRequestFail] DatabaseError %v", err)
    }
    p.writer.SubscribeSuccess(req)
    p.logger.Printf("[UnsubscribeRequest] Success DeliveryPoint=%s",
                    req.DeliveryPoint.Name)
}

