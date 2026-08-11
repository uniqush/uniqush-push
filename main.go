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
	"flag"
	"fmt"
	"os"

	"github.com/uniqush/uniqush-push/srv"
)

var uniqushPushConfFlags = flag.String("config", "/etc/uniqush/uniqush-push.conf", "Config file path")
var uniqushPushShowVersionFlag = flag.Bool("version", false, "Version info")
var uniqushPushGenerateVAPIDKeysFlag = flag.Bool("generate-vapid-keys", false,
	"Print a new VAPID key pair for a webpush/unifiedpush push service provider, then exit")

var uniqushPushVersion = "uniqush-push 2.7.0"

func installPushServices() {
	// InstallFCM registers both "fcm" and "gcm"; the latter is an alias kept so
	// existing gcm subscriptions survive an upgrade. See srv/fcm.go.
	srv.InstallFCM()
	srv.InstallAPNS()
	srv.InstallADM()
	srv.InstallWebPush()
}

// generateVAPIDKeys prints a new VAPID key pair in the form /addpsp expects.
//
// Doing this in the binary rather than over the REST API is deliberate: the
// REST API has no authentication, and a private key should not be minted by, or
// travel over, an unauthenticated endpoint.
func generateVAPIDKeys() error {
	privateKey, publicKey, err := srv.GenerateVAPIDKeys()
	if err != nil {
		return err
	}
	fmt.Printf("vapidpublickey=%s\n", publicKey)
	fmt.Printf("vapidprivatekey=%s\n", privateKey)
	fmt.Fprintln(os.Stderr, "\nKeep the private key secret. Pass both to /addpsp along with")
	fmt.Fprintln(os.Stderr, "service, pushservicetype=webpush (or unifiedpush) and subscriber.")
	return nil
}

func main() {
	flag.Parse()
	if *uniqushPushShowVersionFlag {
		fmt.Printf("%v\n", uniqushPushVersion)
		return
	}
	if *uniqushPushGenerateVAPIDKeysFlag {
		if err := generateVAPIDKeys(); err != nil {
			fmt.Fprintf(os.Stderr, "Could not generate VAPID keys: %v\n", err)
			os.Exit(1)
		}
		return
	}
	installPushServices()

	err := Run(*uniqushPushConfFlags, uniqushPushVersion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot start: %v\n", err)
	}
}
