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

func (w *EventWriter) PushSuccess(req *Request,
                                  subscriber string,
                                  psp *PushServiceProvider,
                                  dp *DeliveryPoint,
                                  id string) {
}

func (w *EventWriter) PushFail(req *Request,
                               subscriber string,
                               psp *PushServiceProvider,
                               dp *DeliveryPoint,
                               err os.Error) {
}

