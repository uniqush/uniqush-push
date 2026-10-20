//go:build !windows

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
	"errors"
	"fmt"
	"net"
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

// listenUnix listens on a unix socket at path with the given mode.
//
// A socket file left by a uniqush that did not shut down cleanly is replaced,
// but only once nothing answers on it: removing a live one would silently
// orphan the other server, and anything that is not a socket is not ours to
// delete.
func listenUnix(path string, mode os.FileMode) (net.Listener, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode().Type() != os.ModeSocket {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		conn, dialErr := net.Dial("unix", path)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("another process is already listening on %s", path)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("could not tell whether %s is in use: %w", path, dialErr)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	// Create the socket accessible to its owner only, so that it is never
	// reachable with wider permissions than mode, even briefly. The umask is
	// process-wide, so this must run at startup, before anything that could
	// create a file concurrently, such as serving requests, has started.
	oldMask := syscall.Umask(0o177)
	ln, err := net.Listen("unix", path)
	syscall.Umask(oldMask)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, mode); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}
