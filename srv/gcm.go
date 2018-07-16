/*
 * Copyright 2011-2013 Nan Deng
 * Copyright 2013-2017 Uniqush contributors.
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
 * This contains cloud messaging code specific to GCM.
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
	// GCM endpoint (FCM can be used to push GCM subscriptions. gcm-http.googleapis.com/gcm/ will be decommissioned in april 2019)
	gcmServiceURL string = "https://fcm.googleapis.com/fcm/send"
	// payload key to extract from push requests to uniqush. The corresponding value is a JSON blob for a GCM data
	// (silent, unless the app has logic to extract information to display notifications on the device)
	gcmRawPayloadKey = "uniqush.payload.gcm"
	// notification key to extract from push requests to uniqush. The corresponding value is a JSON blob for a GCM notification (alerts user)
	gcmRawNotificationKey = "uniqush.notification.gcm"
	// initialism for log messages
	gcmInitialism = "GCM"
	// push service type(name), for requests to uniqush
	gcmPushServiceName = "gcm"
)

type gcmPushService struct {
	// There is only one Transport and one Client for connecting to gcm, shared by the set of PSPs with pushservicetype=gcm (whether or not this is using a sandbox)
	cm.PushServiceBase
}

var _ push.PushServiceType = &gcmPushService{}

func newGCMPushService() *gcmPushService {
	return &gcmPushService{
		PushServiceBase: cm.MakePushServiceBase(gcmInitialism, gcmRawPayloadKey, gcmRawNotificationKey, gcmServiceURL, gcmPushServiceName),
	}
}

// InstallGCM registers the only instance of the GCM push service. It is called only once.
func InstallGCM() {
	psm := push.GetPushServiceManager()
	err := psm.RegisterPushServiceType(newGCMPushService())
	if err != nil {
		panic(fmt.Sprintf("Failed to install GCM module: %v", err))
	}
}

func (p *gcmPushService) BuildPushServiceProviderFromMap(kv map[string]string,
	psp *push.PushServiceProvider) error {
	if service, ok := kv["service"]; ok && len(service) > 0 {
		psp.FixedData["service"] = service
	} else {
		return errors.New("NoService")
	}

	if projectid, ok := kv["projectid"]; ok && len(projectid) > 0 {
		psp.FixedData["projectid"] = projectid
	} else {
		return errors.New("NoProjectID")
	}

	if authtoken, ok := kv["apikey"]; ok && len(authtoken) > 0 {
		psp.VolatileData["apikey"] = authtoken
	} else {
		return errors.New("NoAPIKey")
	}

	return nil
}
