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
	"errors"
	"io"
	"net"
	"syscall"

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

// transientConnectionFailure retries only failures with a known transient
// signal. net/http wraps request errors in url.Error and net.OpError, but those
// wrapper types alone do not say whether another attempt can succeed.
func transientConnectionFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return false
		}
		return dnsErr.IsTimeout || dnsErr.IsTemporary
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var timeoutErr interface{ Timeout() bool }
	return errors.As(err, &timeoutErr) && timeoutErr.Timeout()
}
