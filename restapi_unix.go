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

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func (api *RestAPI) signalSetup() {
	ch := make(chan os.Signal, 1)
	// TODO: Figure out what the equivalent should be on Windows.
	//
	// os.Kill (SIGKILL) used to be listed here behind a `// nolint: megacheck`
	// directive. SIGKILL cannot be caught by any process, so signal.Notify
	// silently ignored it and the graceful shutdown below never ran for it.
	// Removing it is behaviour-neutral and lets staticcheck's SA1016 stay on.
	signal.Notify(ch, syscall.SIGTERM)
	<-ch
	api.stop(nil, "SIGTERM")
}
