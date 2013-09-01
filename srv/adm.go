/*
 * Copyright 2013 Nan Deng
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

package srv

import (
	"errors"
	. "github.com/uniqush/uniqush-push/push"
)

type admPushService struct {
}

func newADMPushService() *admPushService {
	ret := new(admPushService)
	return ret
}

func InstallADM() {
	psm := GetPushServiceManager()
	psm.RegisterPushServiceType(newADMPushService())
}

func (self *admPushService) Finalize() {}
func (self *admPushService) Name() string {
	return "adm"
}
func (self *admPushService) SetErrorReportChan(errChan chan<- error) {
	return
}

func (self *admPushService) BuildPushServiceProviderFromMap(kv map[string]string, psp *PushServiceProvider) error {
	if service, ok := kv["service"]; ok && len(service) > 0 {
		psp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}

	if clientid, ok := kv["clientid"]; ok && len(clientid) > 0 {
		psp.FixedData["clientid"] = clientid
	} else {
		return errors.New("NoClientID")
	}

	if clientsecret, ok := kv["clientsecret"]; ok && len(clientsecret) > 0 {
		psp.FixedData["clientsecret"] = clientsecret
	} else {
		return errors.New("NoClientSecrete")
	}

	return nil
}

func (self *admPushService) BuildDeliveryPointFromMap(kv map[string]string, dp *DeliveryPoint) error {
	if service, ok := kv["service"]; ok && len(service) > 0 {
		dp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}
	if sub, ok := kv["subscriber"]; ok && len(sub) > 0 {
		dp.FixedData["subscriber"] = sub
	} else {
		return errors.New("NoSubscriber")
	}
	if regid, ok := kv["regid"]; ok && len(regid) > 0 {
		dp.FixedData["regid"] = regid
	} else {
		return errors.New("NoRegId")
	}

	return nil
}

func (self *admPushService) Push(psp *PushServiceProvider, dpQueue <-chan *DeliveryPoint, resQueue chan<- *PushResult, notif *Notification) {
}
