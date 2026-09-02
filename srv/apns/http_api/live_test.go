//go:build apns_live

/*
 * Copyright 2013-2026 Uniqush Contributors.
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

// Live tests against Apple's real APNs sandbox.
//
// Apple sells no free route to a push-capable account -- the Push Notifications
// capability needs a paid Apple Developer Program membership, for both .p12 and
// .p8 credentials -- so uniqush cannot verify delivery without one. What it can
// verify, for nothing, is that everything up to authentication works, because
// api.sandbox.push.apple.com answers unauthenticated requests:
//
//	$ curl --http2 -X POST https://api.sandbox.push.apple.com/3/device/aaaa -d '{}'
//	HTTP/2 403
//	apns-id: F4121B12-E0BF-3266-F079-D9253E98BF49
//	{"reason":"MissingProviderToken"}
//
// That one exchange covers TLS to Apple, ALPN negotiating h2, the hostname, the
// /3/device path shape, HTTP/2 framing, and uniqush's error parser running on a
// genuine Apple error body rather than a fixture someone typed from the docs.
// The simulator in srv/apns/apnstest cannot prove any of that, because it is a
// server we wrote to match our own reading of the documentation.
//
// Behind a build tag because it needs the network and Apple's servers:
//
//	go test -tags apns_live -v ./srv/apns/http_api/ -run TestLive
package http_api //nolint:revive

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"github.com/uniqush/uniqush-push/srv/apns/common"
)

// authFailureReasons are the reasons Apple returns for a request that carried
// no usable credentials. Any of them means the request was understood.
var authFailureReasons = map[string]bool{
	"MissingProviderToken": true,
	"InvalidProviderToken": true,
	"ExpiredProviderToken": true,
	"BadCertificate":       true,
	"Forbidden":            true,
}

// liveClient dials Apple the way uniqush does, minus the client certificate.
//
// Deliberately built from the same pieces as GetClient -- an http.Transport
// with a TLS config, handed to http2.ConfigureTransport -- rather than using
// http.DefaultClient. The point is partly to check that this combination
// negotiates h2 with Apple at all, which is the thing that has to work before
// any push can.
func liveClient(t *testing.T) *http.Client {
	t.Helper()

	transport := &http.Transport{
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if err := http2.ConfigureTransport(transport); err != nil {
		t.Fatalf("Could not configure the transport for HTTP/2: %v", err)
	}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second}
}

// TestLiveSandboxSpeaksHTTP2 is the reachability check.
//
// A failure here is not a uniqush bug in itself, but it invalidates everything
// below it: if the connection cannot be made, no conclusion can be drawn from
// what does or does not come back.
func TestLiveSandboxSpeaksHTTP2(t *testing.T) {
	client := liveClient(t)
	defer client.CloseIdleConnections()

	request, err := http.NewRequest(http.MethodPost,
		common.HostDevelopment+"/3/device/"+strings.Repeat("a", 64),
		strings.NewReader(`{"aps":{"alert":"uniqush live probe"}}`))
	if err != nil {
		t.Fatalf("Could not build the request: %v", err)
	}
	request.Header["apns-topic"] = []string{"com.example.uniqush.probe"}
	request.Header["apns-push-type"] = []string{common.DefaultPushType}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("Could not reach %s: %v\n"+
			"These tests are opt-in via -tags apns_live, so a connection failure is a failure, not a skip: "+
			"a reachability check that passes without reaching anything is worse than no check.",
			common.HostDevelopment, err)
	}
	defer response.Body.Close()

	if response.ProtoMajor != 2 {
		t.Errorf("Expected HTTP/2, got HTTP/%d.%d. ALPN did not negotiate h2, which every push depends on",
			response.ProtoMajor, response.ProtoMinor)
	}
	// Apple assigns an apns-id when the client does not send one, and echoes it
	// on every response including errors.
	if response.Header.Get("apns-id") == "" {
		t.Error("Expected APNs to return an apns-id header")
	}
	t.Logf("APNs answered HTTP/%d.%d %d, apns-id %s",
		response.ProtoMajor, response.ProtoMinor, response.StatusCode, response.Header.Get("apns-id"))
}

// TestLiveUnauthenticatedRequestIsRejectedAsExpected checks uniqush's
// understanding of an APNs error response against a real one.
//
// The response body format is the part most easily got wrong from
// documentation alone, and it is what every failure path in
// handlePushResponseBody depends on.
func TestLiveUnauthenticatedRequestIsRejectedAsExpected(t *testing.T) {
	client := liveClient(t)
	defer client.CloseIdleConnections()

	request, err := http.NewRequest(http.MethodPost,
		common.HostDevelopment+"/3/device/"+strings.Repeat("a", 64),
		strings.NewReader(`{"aps":{"alert":"uniqush live probe"}}`))
	if err != nil {
		t.Fatalf("Could not build the request: %v", err)
	}
	request.Header["apns-topic"] = []string{"com.example.uniqush.probe"}
	request.Header["apns-push-type"] = []string{common.DefaultPushType}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("Could not reach %s: %v\n"+
			"These tests are opt-in via -tags apns_live, so a connection failure is a failure, not a skip: "+
			"a reachability check that passes without reaching anything is worse than no check.",
			common.HostDevelopment, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("Could not read the response body: %v", err)
	}

	if response.StatusCode != http.StatusForbidden {
		t.Errorf("Expected 403 for an unauthenticated push, got %d (%s)", response.StatusCode, body)
	}

	// The struct uniqush actually parses with, not a local copy.
	apnsError := new(APNSErrorResponse)
	if err := json.Unmarshal(body, apnsError); err != nil {
		t.Fatalf("uniqush cannot parse a real APNs error response: %v (%s)", err, body)
	}
	if !authFailureReasons[apnsError.Reason] {
		t.Errorf("Expected an authentication-related reason, got %q. "+
			"If Apple has changed what it returns here, the mapping in "+
			"handlePushResponseBody is worth rechecking too", apnsError.Reason)
	}
	t.Logf("APNs rejected the unauthenticated push with %d %s", response.StatusCode, apnsError.Reason)
}

// TestLiveAnUnauthenticatedRejectionIsNotADeadToken checks the one
// unsubscribe decision this probe can actually reach.
//
// Apple answers an unauthenticated push with MissingProviderToken, and uniqush
// must not read that as a dead device: unsubscribing on it would delete every
// subscription in a service because a credential was wrong. This asserts
// exactly that, against Apple's own reply rather than a fixture.
//
// What it deliberately does *not* claim is the Forbidden case. Reaching a
// genuine 403 Forbidden means authenticating and then being refused for the
// topic, which needs a paid Developer Program account this probe does not have
// -- so asserting it here would be asserting nothing. An earlier version
// indexed permanentTokenFailureReasons with whatever reason came back, which
// meant it passed on MissingProviderToken and would have gone on passing if
// Forbidden were later added to that map: a guard that cannot fail.
//
// The Forbidden guarantee lives in TestConformanceForbiddenDoesNotUnsubscribe,
// where the simulator can be told to return it.
func TestLiveAnUnauthenticatedRejectionIsNotADeadToken(t *testing.T) {
	client := liveClient(t)
	defer client.CloseIdleConnections()

	request, err := http.NewRequest(http.MethodPost,
		common.HostDevelopment+"/3/device/"+strings.Repeat("a", 64),
		strings.NewReader(`{"aps":{"alert":"uniqush live probe"}}`))
	if err != nil {
		t.Fatalf("Could not build the request: %v", err)
	}
	request.Header["apns-topic"] = []string{"com.example.uniqush.probe"}
	request.Header["apns-push-type"] = []string{common.DefaultPushType}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("Could not reach %s: %v\n"+
			"These tests are opt-in via -tags apns_live, so a connection failure is a failure, not a skip: "+
			"a reachability check that passes without reaching anything is worse than no check.",
			common.HostDevelopment, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("Could not read the response body: %v", err)
	}

	apnsError := new(APNSErrorResponse)
	if err := json.Unmarshal(body, apnsError); err != nil {
		t.Fatalf("Could not parse the APNs error response: %v (%s)", err, body)
	}
	// Named, so this cannot quietly become a test of some other reason. If Apple
	// ever answers an unauthenticated push differently, that is worth knowing
	// rather than absorbing.
	if apnsError.Reason != "MissingProviderToken" {
		t.Fatalf("Expected APNs to answer an unauthenticated push with MissingProviderToken, "+
			"got %q (HTTP %d). The assertion below is about that reason specifically.",
			apnsError.Reason, response.StatusCode)
	}
	if permanentTokenFailureReasons[apnsError.Reason] {
		t.Errorf("uniqush would unsubscribe every device on reason %q, which APNs returns for "+
			"an unauthenticated request. That is a provider problem, not a dead token.", apnsError.Reason)
	}
}

// TestLiveProductionAndSandboxAreBothReachable checks the constants.
//
// A typo in either host is the sort of thing unit tests cannot catch and that
// only shows up as every push failing to connect.
func TestLiveProductionAndSandboxAreBothReachable(t *testing.T) {
	client := liveClient(t)
	defer client.CloseIdleConnections()

	for _, host := range []string{common.HostProduction, common.HostDevelopment} {
		t.Run(host, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, host+"/3/device/"+strings.Repeat("a", 64),
				strings.NewReader(`{"aps":{}}`))
			if err != nil {
				t.Fatalf("Could not build the request: %v", err)
			}
			request.Header["apns-topic"] = []string{"com.example.uniqush.probe"}
			request.Header["apns-push-type"] = []string{common.DefaultPushType}

			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("Could not reach %s: %v", host, err)
			}
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, response.Body)

			// Any HTTP response at all is the assertion: it means the name
			// resolves, the TLS handshake completed and APNs is on the far end.
			t.Logf("%s answered %d over HTTP/%d", host, response.StatusCode, response.ProtoMajor)
		})
	}
}
