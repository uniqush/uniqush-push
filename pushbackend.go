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

package main

import (
	. "github.com/uniqush/log"
	. "github.com/uniqush/uniqush-push/db"
	. "github.com/uniqush/uniqush-push/push"
)

type PushBackEnd struct {
	psm       *PushServiceManager
	db        PushDatabase
	loggers    []Logger
	notifpool *NotificationPool
}

func NewPushBackEnd(psm *PushServiceManager, database PushDatabase, loggers []Logger) *PushBackEnd {
	ret := new(PushBackEnd)
	ret.psm = psm
	ret.db = database
	ret.loggers = loggers
	ret.notifpool = NewNotificationPool(0, 0)
	return ret
}

func (self *PushBackEnd) AddPushServiceProvider(service string, psp *PushServiceProvider) error {
	err := self.db.AddPushServiceProviderToService(service, psp)
	if err != nil {
		return err
	}
	return nil
}

func (self *PushBackEnd) RemovePushServiceProvider(service string, psp *PushServiceProvider) error {
	err := self.db.RemovePushServiceProviderFromService(service, psp)
	if err != nil {
		return err
	}
	return nil
}
