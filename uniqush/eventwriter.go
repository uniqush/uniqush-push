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
    "io"
    "fmt"
    "os"
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
    new_request_received string = "{\"event\":\"RequestReceived\", \"request\":\"%s\"}\r\n"
)

func jsonRequest(req *Request) string{
    ret := fmt.Sprintf("{\"id\":%s, \"action\":%s}", req.ID, req.ActionName())
    return ret
}

func (w *EventWriter) RequestReceived(req *Request) {
    fmt.Fprintf(w.writer, new_request_received, jsonRequest(req))
}

func (w *EventWriter) BadRequest(req *Request, err os.Error) {
}

func (w *EventWriter) SubscribeSuccess(req *Request) {
}

func (w *EventWriter) SubscribeFail(req *Request, err os.Error) {
}

func (w *EventWriter) UnsubscribeSucess(req *Request) {
}

func (w *EventWriter) UnsubscribeFail(req *Request, err os.Error) {
}

func (w *EventWriter) AddPushServiceSuccess(req *Request) {
}

func (w *EventWriter) AddPushServiceFail(req *Request, err os.Error) {
}

func (w *EventWriter) RemovePushServiceSuccess(req *Request) {
}

func (w *EventWriter) RemovePushServiceFail(req *Request, err os.Error) {
}

func (w *EventWriter) PushSuccess(req *Request, subscriber string, dp *DeliveryPoint, id string) {
}

func (w *EventWriter) PushFail(req *Request, subscriber string, dp *DeliveryPoint, err os.Error) {
}

