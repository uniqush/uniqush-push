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
    "time"
)

const (
    ACTION_PUSH = iota
    ACTION_SUBSCRIBE
    ACTION_UNSUBSCRIBE
    ACTION_ADD_PUSH_SERVICE_PROVIDER
    ACTION_REMOVE_PUSH_SERVICE_PROVIDER
    NR_ACTIONS
)

type Request struct {
    ID string
    Action int
    Service string
    RequestSenderAddr string
    Subscribers []string
    PreferedService int
    Timestamp int64
    nrRetries int
    backoffTime int64

    PushServiceProvider *PushServiceProvider
    DeliveryPoint *DeliveryPoint
    Notification *Notification
    PreviousTry *Request
}

var (
    actionNames []string
)

func init() {
    actionNames = make([]string, NR_ACTIONS)
    actionNames[ACTION_PUSH] = "Push"
    actionNames[ACTION_SUBSCRIBE] = "Subcribe"
    actionNames[ACTION_UNSUBSCRIBE] = "Unsubcribe"
    actionNames[ACTION_ADD_PUSH_SERVICE_PROVIDER] = "AddPushServiceProvider"
    actionNames[ACTION_REMOVE_PUSH_SERVICE_PROVIDER] = "RemovePushServiceProvider"
}

func (req *Request) PunchTimestamp() {
    req.Timestamp = int64(time.Now().Nanosecond())
}

func (req *Request) ValidAction() bool {
    if req.Action < 0 || req.Action >= len(actionNames) {
        return false
    }
    return true
}

func (req *Request) ActionName() string {
    if req.ValidAction() {
        return actionNames[req.Action]
    }
    return "InvalidAction"
}

