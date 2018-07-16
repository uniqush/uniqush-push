/*
 * Copyright 2011-2013 Nan Deng
 * Copyright 2013-2017 Uniqush Contributors.
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
 * This contains cloud messaging code specific to FCM.
 * Implementation details common to GCM and FCM are kept in srv/cloud_messaging
 */

package srv

import (
	"errors"
	"fmt"

	"github.com/uniqush/uniqush-push/push"
	cm "github.com/uniqush/uniqush-push/srv/cloud_messaging"
)

const (
	// FCM endpoint
	fcmServiceURL string = "https://fcm.googleapis.com/fcm/send"
	// payload key to extract from push requests to uniqush. The corresponding value is a JSON blob for a FCM data push
	// (silent, unless the app has logic to extract information to display notifications on the device)
	fcmRawPayloadKey = "uniqush.payload.fcm"
	// notification key to extract from push requests to uniqush. The corresponding value is a JSON blob for a FCM notification (alerts user)
	fcmRawNotificationKey = "uniqush.notification.fcm"
	// initialism for log messages
	fcmInitialism = "FCM"
	// push service type(name), for requests to uniqush
	fcmPushServiceName = "fcm"
)

type fcmPushService struct {
	// There is only one Transport and one Client for connecting to fcm, shared by the set of PSPs with pushservicetype=fcm (whether or not this is using a sandbox)
	cm.PushServiceBase
}

var _ push.PushServiceType = &fcmPushService{}

func newFCMPushService() *fcmPushService {
	return &fcmPushService{
		PushServiceBase: cm.MakePushServiceBase(fcmInitialism, fcmRawPayloadKey, fcmRawNotificationKey, fcmServiceURL, fcmPushServiceName),
	}
}

// InstallFCM registers the only instance of the FCM push service. It is called only once.
func InstallFCM() {
	psm := push.GetPushServiceManager()
	err := psm.RegisterPushServiceType(newFCMPushService())
	if err != nil {
		panic(fmt.Sprintf("Failed to install FCM module: %v", err))
	}
}

func (p *fcmPushService) BuildPushServiceProviderFromMap(kv map[string]string,
	psp *push.PushServiceProvider) error {
	if service, ok := kv["service"]; ok && len(service) > 0 {
		psp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}

	if authtoken, ok := kv["apikey"]; ok && len(authtoken) > 0 {
		psp.VolatileData["apikey"] = authtoken
	} else {
		return errors.New("NoAPIKey")
	}

	return nil
}
