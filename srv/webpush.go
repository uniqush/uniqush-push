/*
 * Copyright 2026 Uniqush Contributors.
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

package srv

import (
	"fmt"

	"github.com/uniqush/uniqush-push/push"
	"github.com/uniqush/uniqush-push/srv/webpush"
)

// Names this backend is registered under.
//
// The implementation is plain Web Push (RFC 8030/8291/8292), which is what
// "webpush" says. "unifiedpush" is registered as an alias because that is the
// name people will look for: UnifiedPush is the reason most users will want
// this, and it uses exactly this protocol between application server and push
// server.
//
// They are two independent registrations rather than one with a second label,
// because a pushservicetype is the key both the push service manager and the
// database use. That means a subscription made against "webpush" is a different
// delivery point from one made against "unifiedpush", even for the same
// endpoint. Pick one per service and stay with it.
const (
	WebPushServiceName     = "webpush"
	UnifiedPushServiceName = "unifiedpush"
)

// InstallWebPush registers the Web Push backend under both of its names.
func InstallWebPush() {
	psm := push.GetPushServiceManager()
	for _, name := range []string{WebPushServiceName, UnifiedPushServiceName} {
		if err := psm.RegisterPushServiceType(webpush.NewPushService(name)); err != nil {
			panic(fmt.Sprintf("Failed to install %s module: %v", name, err))
		}
	}
}

// GenerateVAPIDKeys returns a fresh VAPID keypair, raw-url base64 encoded, for
// the -generate-vapid-keys flag.
func GenerateVAPIDKeys() (privateKey, publicKey string, err error) {
	return webpush.GenerateVAPIDKeys()
}
