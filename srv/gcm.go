/*
 * Copyright 2011-2013 Nan Deng
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

	"github.com/uniqush/uniqush-push/push"
	cm "github.com/uniqush/uniqush-push/srv/cloud_messaging"
)

const (
	// GCM endpoint
	gcmServiceURL string = "https://android.googleapis.com/gcm/send"
	// payload key to extract from push requests to uniqush
	gcmRawPayloadKey = "uniqush.payload.gcm"
	// acronym for log messages
	gcmAcronym = "GCM"
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
		PushServiceBase: cm.MakePushServiceBase(gcmAcronym, gcmRawPayloadKey, gcmServiceURL, gcmPushServiceName),
	}
}

func InstallGCM() {
	psm := push.GetPushServiceManager()
	psm.RegisterPushServiceType(newGCMPushService())
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

func (p *gcmPushService) Name() string {
	return "gcm"
}
