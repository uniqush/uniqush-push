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

    PushServiceProvider *PushServiceProvider
    DeliveryPoint *DeliveryPoint
    Notification *Notification
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
    req.Timestamp = time.Nanoseconds()
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

