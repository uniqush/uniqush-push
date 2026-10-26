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
	"context"
	"crypto/tls"
	"errors"
	"net"

	"github.com/uniqush/uniqush-push/push"
)

// retryTransientConnectionError moves transport errors into the retry path
// shared by APNs, FCM, and Web Push. A ConnectionError can also wrap permanent
// failures, so classify its cause before asking fixError to schedule a retry.
func retryTransientConnectionError(result *push.Result) {
	if result == nil || result.Provider == nil || result.Destination == nil || result.Content == nil {
		return
	}
	connectionErr, ok := result.Err.(*push.ConnectionError)
	if !ok || !transientConnectionFailure(connectionErr.Err) {
		return
	}
	result.Err = push.NewRetryErrorWithReason(
		result.Provider,
		result.Destination,
		result.Content,
		0,
		connectionErr,
	)
}

// transientConnectionFailure retries network-operation failures, but not
// failures that another attempt cannot fix. Endpoint-policy rejections never
// arrive as ConnectionError; DNS and certificate errors need explicit checks
// because net/http also wraps them in net.OpError.
func transientConnectionFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var verificationErr *tls.CertificateVerificationError
	if errors.As(err, &verificationErr) {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return false
		}
		return dnsErr.IsTimeout || dnsErr.IsTemporary
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkErr *net.OpError
	return errors.As(err, &networkErr)
}
