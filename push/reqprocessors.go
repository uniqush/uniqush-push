/*
 * Copyright 2011 Nan Deng
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package push

import (
	"fmt"
)

type RequestProcessor interface {
	SetLogger(logger *Logger)
	SetEventWriter(writer *EventWriter)
	Process(req *Request)
}

type ActionPrinter struct {
	logger *Logger
}

func NewActionPrinter(logger *Logger) RequestProcessor {
	a := new(ActionPrinter)
	a.logger = logger
	return a
}

func (p *ActionPrinter) SetLogger(logger *Logger) {
	p.logger = logger
}

func (p *ActionPrinter) Process(r *Request) {
	p.logger.Debugf("Action: %d-%s, id: %s\n", r.Action, r.ActionName(), r.ID)
	r.Finish()
	return
}

func (p *ActionPrinter) SetEventWriter(writer *EventWriter) {
	return
}

type loggerEventWriter struct {
	logger *Logger
	writer *EventWriter
}

func (l *loggerEventWriter) SetLogger(logger *Logger) {
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

func NewAddPushServiceProviderProcessor(logger *Logger, writer *EventWriter, dbfront DatabaseFrontDeskIf) RequestProcessor {
	ret := new(AddPushServiceProviderProcessor)
	ret.SetLogger(logger)
	ret.SetEventWriter(writer)
	ret.SetDatabase(dbfront)

	return ret
}

func (p *AddPushServiceProviderProcessor) Process(req *Request) {
	defer req.Finish()
	err := p.dbfront.AddPushServiceProviderToService(req.Service, req.PushServiceProvider)
	if err != nil {
		p.writer.AddPushServiceFail(req, err)
		p.logger.Errorf("[AddPushServiceRequestFail] RequestId=%v DatabaseError %v", req.ID, err)
		req.Respond(fmt.Errorf("[AddPushServiceRequestFail] RequestId=%v DatabaseError %v", req.ID, err))
		return
	}
	p.writer.AddPushServiceSuccess(req)
	p.logger.Infof("[AddPushServiceRequest] RequestId=%v Success PushServiceProviderID=%s",
		req.ID, req.PushServiceProvider.Name())
	e := fmt.Errorf("PushServiceProvider=%v Success!", req.PushServiceProvider.Name())
	req.Respond(e)
	return
}

type RemovePushServiceProviderProcessor struct {
	loggerEventWriter
	databaseSetter
}

func NewRemovePushServiceProviderProcessor(logger *Logger,
	writer *EventWriter,
	dbfront DatabaseFrontDeskIf) RequestProcessor {
	ret := new(RemovePushServiceProviderProcessor)
	ret.SetLogger(logger)
	ret.SetEventWriter(writer)
	ret.SetDatabase(dbfront)

	return ret
}

func (p *RemovePushServiceProviderProcessor) Process(req *Request) {
	defer req.Finish()
	err := p.dbfront.RemovePushServiceProviderFromService(req.Service, req.PushServiceProvider)
	if err != nil {
		p.writer.RemovePushServiceFail(req, err)
		p.logger.Errorf("[RemovePushServiceRequestFail] RequestId=%v DatabaseError %v", req.ID, err)
		req.Respond(fmt.Errorf("[RemovePushServiceRequestFail] RequestId=%v DatabaseError %v", req.ID, err))
		return
	}
	p.writer.RemovePushServiceSuccess(req)
	p.logger.Infof("[RemovePushServiceRequest] Success PushServiceProviderID=%s", req.PushServiceProvider.Name())
	return
}

type SubscribeProcessor struct {
	loggerEventWriter
	databaseSetter
}

func NewSubscribeProcessor(logger *Logger, writer *EventWriter, dbfront DatabaseFrontDeskIf) RequestProcessor {
	ret := new(SubscribeProcessor)
	ret.SetLogger(logger)
	ret.SetEventWriter(writer)
	ret.SetDatabase(dbfront)

	return ret
}

func (p *SubscribeProcessor) Process(req *Request) {
	defer req.Finish()
	if len(req.Subscribers) == 0 {
		return
	}
	psp, err := p.dbfront.AddDeliveryPointToService(req.Service,
		req.Subscribers[0],
		req.DeliveryPoint)
	if err != nil || psp == nil {
		p.writer.SubscribeFail(req, err)
		p.logger.Errorf("[SubscribeRequestFail] RequestId=%v DatabaseError %v", req.ID, err)
		req.Respond(fmt.Errorf("[SubscribeRequestFail] RequestId=%v DatabaseError %v", req.ID, err))
		return
	}
	p.writer.SubscribeSuccess(req)
	dpname := req.DeliveryPoint.Name()
	pspname := psp.Name()
	p.logger.Infof("[SubscribeRequest] RequestId=%v Success DeliveryPoint=%s PushServiceProvider=%s",
		req.ID, dpname, pspname)
	e := fmt.Errorf("DeliveryPoint=%v Success!", dpname)
	req.Respond(e)
	return
}

type UnsubscribeProcessor struct {
	loggerEventWriter
	databaseSetter
}

func NewUnsubscribeProcessor(logger *Logger, writer *EventWriter, dbfront DatabaseFrontDeskIf) RequestProcessor {
	ret := new(UnsubscribeProcessor)
	ret.SetLogger(logger)
	ret.SetEventWriter(writer)
	ret.SetDatabase(dbfront)

	return ret
}

func (p *UnsubscribeProcessor) Process(req *Request) {
	defer req.Finish()
	if len(req.Subscribers) == 0 || req.DeliveryPoint == nil {
		p.logger.Errorf("[UnSubscribeRequestFail] RequestId=%v Nil Pointer", req.ID)
		req.Respond(fmt.Errorf("[UnSubscribeRequestFail] RequestId=%v Nil Pointer", req.ID))
		return

	}
	err := p.dbfront.RemoveDeliveryPointFromService(req.Service,
		req.Subscribers[0],
		req.DeliveryPoint)
	if err != nil {
		p.writer.SubscribeFail(req, err)
		p.logger.Errorf("[UnSubscribeRequestFail] RequestId=%v DatabaseError %v", req.ID, err)
		req.Respond(fmt.Errorf("[UnSubscribeRequestFail] RequestId=%v DatabaseError %v", req.ID, err))
		return
	}
	p.writer.SubscribeSuccess(req)
	p.logger.Infof("[UnsubscribeRequest] RequestId=%v Success DeliveryPoint=%s",
		req.ID, req.DeliveryPoint.Name())
	return
}
