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
	"net/url"
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

func TestTransientConnectionFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "connection reset",
			err: &url.Error{Op: "Post", URL: "https://push.example", Err: &net.OpError{
				Op: "read", Net: "tcp", Err: errors.New("connection reset by peer"),
			}},
			want: true,
		},
		{
			name: "TLS handshake timeout",
			err: &url.Error{Op: "Post", URL: "https://push.example", Err: &net.OpError{
				Op: "remote error", Net: "tcp", Err: context.DeadlineExceeded,
			}},
			want: true,
		},
		{
			name: "temporary DNS failure",
			err: &url.Error{Op: "Post", URL: "https://push.example", Err: &net.OpError{
				Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "server misbehaving", IsTemporary: true},
			}},
			want: true,
		},
		{
			name: "DNS not found",
			err: &url.Error{Op: "Post", URL: "https://push.example", Err: &net.OpError{
				Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", IsNotFound: true},
			}},
			want: false,
		},
		{
			name: "certificate verification failure",
			err: &url.Error{Op: "Post", URL: "https://push.example", Err: &net.OpError{
				Op: "remote error", Net: "tcp", Err: &tls.CertificateVerificationError{
					Err: errors.New("x509: certificate signed by unknown authority"),
				},
			}},
			want: false,
		},
		{
			name: "canceled request",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "non-network error",
			err:  errors.New("unsupported protocol scheme"),
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := transientConnectionFailure(test.err); got != test.want {
				t.Errorf("transientConnectionFailure(%T) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestRetryTransientConnectionError(t *testing.T) {
	for _, service := range []string{"apns", "fcm", "webpush"} {
		t.Run(service, func(t *testing.T) {
			provider := push.NewEmptyPushServiceProvider()
			provider.FixedData["service"] = service
			destination := push.NewEmptyDeliveryPoint()
			destination.FixedData["service"] = service
			destination.FixedData["subscriber"] = "bob"
			notification := push.NewEmptyNotification()
			connectionErr := push.NewConnectionError(&net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: errors.New("connection refused"),
			})
			result := &push.Result{
				Provider:    provider,
				Destination: destination,
				Content:     notification,
				Err:         connectionErr,
			}

			retryTransientConnectionError(result)

			retryErr, ok := result.Err.(*push.RetryError)
			if !ok {
				t.Fatalf("result error = %T, want *push.RetryError", result.Err)
			}
			if retryErr.Provider != provider || retryErr.Destination != destination || retryErr.Content != notification {
				t.Fatal("retry error did not retain the provider, destination, and notification")
			}
			if retryErr.Reason != connectionErr {
				t.Fatalf("retry reason = %v, want original connection error %v", retryErr.Reason, connectionErr)
			}
		})
	}
}

func TestPermanentConnectionErrorIsNotRetried(t *testing.T) {
	provider := push.NewEmptyPushServiceProvider()
	destination := push.NewEmptyDeliveryPoint()
	notification := push.NewEmptyNotification()
	result := &push.Result{
		Provider:    provider,
		Destination: destination,
		Content:     notification,
		Err: push.NewConnectionError(&net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: &net.DNSError{Err: "no such host", IsNotFound: true},
		}),
	}
	originalErr := result.Err

	retryTransientConnectionError(result)

	if result.Err != originalErr {
		t.Fatalf("permanent connection error was changed to %T", result.Err)
	}
}
