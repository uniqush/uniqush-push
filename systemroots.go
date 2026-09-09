/*
 * Copyright 2013-2026 Uniqush Contributors.
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
 */

package main

import (
	"crypto/x509"
	"errors"
	"fmt"
)

// systemCertPool is x509.SystemCertPool, as a variable so the failure can be
// tested. There is no portable way to make the real one fail.
var systemCertPool = x509.SystemCertPool

// checkSystemRoots refuses to start when the OS certificate store cannot be
// loaded.
//
// uniqush talks to Apple, Google, Amazon and whatever push server a Web Push
// subscriber named, over TLS, verifying each against the system roots. Without
// them it can deliver nothing at all, and the way that failure presents is the
// problem: crypto/x509 loads the store once and caches the outcome for the life
// of the process, so a single early failure is permanent. Every push then fails
// with an x509.SystemRootsError buried in a handshake error, on a server that
// started cleanly and reports itself healthy.
//
// One line at startup instead. This is the same reasoning as refusing a
// misconfigured redis: a push server that cannot reach a push service is not
// degraded, it is doing nothing, and it should say so at the point where
// somebody is watching.
//
// Loading the pool here also warms that cache while the answer can still be
// acted on, so nothing later gets to discover it in the middle of a push.
//
// What this cannot catch is a store that loads and is empty, which is what an
// image without a ca-certificates package usually gives. CertPool has no
// portable way to count what is in it -- Subjects is deprecated and returns
// nothing for the system pool on macOS and Windows -- and a handshake against
// an empty pool fails as an unknown authority rather than as missing roots.
func checkSystemRoots() error {
	pool, err := systemCertPool()
	if err != nil {
		return fmt.Errorf("cannot load the system root certificates, so no push service can be reached: %w.\n"+
			"Install your distribution's CA certificate bundle (ca-certificates on Debian and Red Hat "+
			"derivatives, or the equivalent in the container image), or point SSL_CERT_FILE or SSL_CERT_DIR "+
			"at one", err)
	}
	if pool == nil {
		return errors.New("the system root certificates loaded as an empty store, so no push service can be reached.\n" +
			"Install your distribution's CA certificate bundle (ca-certificates on Debian and Red Hat " +
			"derivatives, or the equivalent in the container image), or point SSL_CERT_FILE or SSL_CERT_DIR " +
			"at one")
	}
	return nil
}
