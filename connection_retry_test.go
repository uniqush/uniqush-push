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
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/uniqush/uniqush-push/push"
)

func TestTransientConnectionFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
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

func postError(t *testing.T, client *http.Client, endpoint string) error {
	t.Helper()
	response, err := client.Post(endpoint, "text/plain", strings.NewReader("payload"))
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("POST succeeded, want a connection error")
	}
	return err
}

func TestConnectionClosedEarlyRetries(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	requestRead := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			requestRead <- acceptErr
			return
		}
		defer conn.Close()
		request, readErr := http.ReadRequest(bufio.NewReader(conn))
		if readErr == nil {
			_, readErr = io.Copy(io.Discard, request.Body)
			request.Body.Close()
		}
		requestRead <- readErr
	}()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	err = postError(t, client, "http://"+listener.Addr().String())
	if requestErr := <-requestRead; requestErr != nil {
		t.Fatalf("server could not read the POST before closing: %v", requestErr)
	}
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("POST error = %T %v, want an early connection close", err, err)
	}
	if !transientConnectionFailure(err) {
		t.Fatalf("early connection close %T %v was not classified as transient", err, err)
	}
}

func TestConnectionRefusedRetries(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	err = postError(t, client, "http://"+address)
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("POST error = %T %v, want connection refused", err, err)
	}
	if !transientConnectionFailure(err) {
		t.Fatalf("connection refusal %T %v was not classified as transient", err, err)
	}
}

func TestTLSHandshakeTimeoutRetries(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, listenerErr := listener.Accept()
		if listenerErr != nil {
			acceptErr <- listenerErr
			return
		}
		accepted <- conn
	}()
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 100 * time.Millisecond}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	err = postError(t, client, "https://"+listener.Addr().String())
	select {
	case conn := <-accepted:
		conn.Close()
	case listenerErr := <-acceptErr:
		t.Fatalf("listener did not accept the TLS connection: %v", listenerErr)
	case <-time.After(time.Second):
		t.Fatal("listener did not accept a TLS connection")
	}
	var timeoutErr interface{ Timeout() bool }
	if !errors.As(err, &timeoutErr) || !timeoutErr.Timeout() {
		t.Fatalf("POST error = %T %v, want a timeout from net/http", err, err)
	}
	if !transientConnectionFailure(err) {
		t.Fatalf("TLS handshake timeout %T %v was not classified as transient", err, err)
	}
}

func TestTLSClientCertificateRejectionIsPermanent(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()

	client := server.Client()
	client.Timeout = 2 * time.Second
	client.Transport.(*http.Transport).Proxy = nil
	err := postError(t, client, server.URL)
	if transientConnectionFailure(err) {
		t.Fatalf("client certificate rejection %T %v was classified as transient", err, err)
	}
}

func TestPrivateAddressDialRejectionIsNotRetried(t *testing.T) {
	dialer := &net.Dialer{Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip != nil && ip.IsPrivate() {
			return errors.New("refusing to connect to " + host + ", which is not globally routable")
		}
		return nil
	}}
	transport := &http.Transport{Proxy: nil, DialContext: dialer.DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	err := postError(t, client, "http://10.0.0.1/")
	if !strings.Contains(err.Error(), "refusing to connect to 10.0.0.1") {
		t.Fatalf("POST error = %T %v, want the private-address dial rejection", err, err)
	}
	var networkErr *net.OpError
	if !errors.As(err, &networkErr) {
		t.Fatalf("POST error = %T %v, want net/http's wrapped dial error", err, err)
	}

	provider := push.NewEmptyPushServiceProvider()
	destination := push.NewEmptyDeliveryPoint()
	notification := push.NewEmptyNotification()
	result := &push.Result{
		Provider:    provider,
		Destination: destination,
		Content:     notification,
		Err:         push.NewConnectionError(err),
	}
	originalErr := result.Err
	retryTransientConnectionError(result)
	if result.Err != originalErr {
		t.Fatalf("private-address rejection was changed to %T", result.Err)
	}
}

func TestRetryTransientConnectionError(t *testing.T) {
	provider := push.NewEmptyPushServiceProvider()
	destination := push.NewEmptyDeliveryPoint()
	notification := push.NewEmptyNotification()
	connectionErr := push.NewConnectionError(syscall.ECONNREFUSED)
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
