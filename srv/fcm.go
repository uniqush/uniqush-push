/*
 * Copyright 2011-2013 Nan Deng
 * Copyright 2013-2026 Uniqush Contributors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package srv contains implementations of push services with code to send
// pushes to, receive responses from, and manage delivery points for the various
// external push service providers (ADM, APNs, FCM and Web Push).
package srv

import (
	"fmt"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/fcm"
)

// Names the FCM backend is registered under.
//
// "gcm" is an alias rather than a separate implementation. Google Cloud
// Messaging was folded into FCM years ago -- uniqush repointed its gcm backend
// at the FCM endpoint back in 2018 -- so there has been no behavioural
// difference for a long time, and both stopped working together when the legacy
// API was decommissioned in June 2024.
//
// The alias exists for one specific reason. A delivery point's identity is
// "<pushservicetype>:<hash of its fixed data>", and that string is its database
// key. Retiring the name would strand every stored gcm subscription: not
// pushable, and not removable via /unsubscribe either, since that needs the type
// registered in order to compute the name. Keeping it lets an operator re-run
// /addpsp with a service account and carry on, with no device re-subscribing.
const (
	FCMServiceName = "fcm"
	GCMServiceName = "gcm"
)

// InstallFCM registers the FCM push service under both of its names.
func InstallFCM() {
	psm := push.GetPushServiceManager()
	for _, name := range []string{FCMServiceName, GCMServiceName} {
		if err := psm.RegisterPushServiceType(fcm.NewPushService(name)); err != nil {
			panic(fmt.Sprintf("Failed to install %s module: %v", name, err))
		}
	}
}
