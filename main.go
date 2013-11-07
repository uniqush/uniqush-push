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
	. "github.com/uniqush/uniqush-push/srv"
	"os"
	"runtime"
)

var uniqushPushConfFlags = flag.String("config", "/etc/uniqush/uniqush-push.conf", "Config file path")
var uniqushPushShowVersionFlag = flag.Bool("version", false, "Version info")

var uniqushPushVersion = "uniqush-push 1.5.1"

func installPushSrvices() {
	InstallGCM()
	InstallC2DM()
	InstallAPNS()
	InstallADM()
}

func main() {
	flag.Parse()
	if *uniqushPushShowVersionFlag {
		fmt.Printf("%v\n", uniqushPushVersion)
		return
	}
	runtime.GOMAXPROCS(runtime.NumCPU() + 1)
	installPushSrvices()

	err := Run(*uniqushPushConfFlags, uniqushPushVersion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot start: %v\n", err)
	}
}
