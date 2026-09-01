//go:build fcm_live

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

// Live tests against Google's real FCM servers.
//
// The rest of this package's tests drive a mocked HTTPClient, which proves that
// uniqush builds the request it means to build and interprets the response it
// expects to get. It cannot prove that Google agrees with any of it, and after
// the HTTP v1 migration that is exactly the open question.
//
// These tests fill that gap. They are behind a build tag rather than a t.Skip
// because they need real credentials, take real network round trips and consume
// real quota, none of which belongs in `go test ./...`.
//
//	export UNIQUSH_FCM_PROJECT_ID=my-firebase-project
//	export UNIQUSH_FCM_CREDENTIALS=/path/to/service-account.json
//	go test -tags fcm_live -v ./srv/fcm/ -run TestLive
//
// Setting UNIQUSH_FCM_REGID as well adds the one test that needs a device:
// an actual delivery to an actual registration token.
//
//	export UNIQUSH_FCM_REGID=<token from a browser or Android app>
//
// See examples/fcm-demo/README.md for how to get each of those.
package fcm

import (
	"os"
	"strings"
	"testing"

	"github.com/uniqush/uniqush-push/push"
)

const (
	envProjectID   = "UNIQUSH_FCM_PROJECT_ID"
	envCredentials = "UNIQUSH_FCM_CREDENTIALS"
	envRegID       = "UNIQUSH_FCM_REGID"
)

// liveService returns a real push service and provider, or skips.
//
// Deliberately not overriding the client factory: the point of these tests is
// that newAuthenticatedClient really does read the service account, really does
// mint an OAuth2 token and that Google really does accept it.
func liveService(t *testing.T) (*pushService, *push.PushServiceProvider) {
	t.Helper()

	projectID := os.Getenv(envProjectID)
	credentials := os.Getenv(envCredentials)
	if projectID == "" || credentials == "" {
		t.Skipf("Set %s and %s to run the live FCM tests", envProjectID, envCredentials)
	}
	// No readability check here. os.Stat, the obvious one, succeeds on a file
	// this process cannot open, so it would not have caught the case its own
	// error message described. Opening it would work, but it is redundant:
	// BuildPushServiceProviderFromMap below reads and parses the file, and its
	// error distinguishes "could not read" from "not a service account", which
	// a bare open cannot.
	service := NewPushService("fcm").(*pushService)
	registerForTest(service.Name())

	psp, err := push.GetPushServiceManager().BuildPushServiceProviderFromMap(map[string]string{
		"service":         "livetest",
		"pushservicetype": service.Name(),
		"projectid":       projectID,
		"credentialsfile": credentials,
	})
	if err != nil {
		// Registration parses the service account offline, so this fails before
		// any network call if the file is the wrong JSON entirely -- an API key
		// or a google-services.json rather than a service account.
		t.Fatalf("Could not build a provider from %s/%s: %v", envProjectID, envCredentials, err)
	}
	return service, psp
}

func liveDeliveryPoint(t *testing.T, service *pushService, regID string) *push.DeliveryPoint {
	t.Helper()
	dp, err := push.GetPushServiceManager().BuildDeliveryPointFromMap(map[string]string{
		"service":         "livetest",
		"subscriber":      "livetester",
		"pushservicetype": service.Name(),
		"regid":           regID,
	})
	if err != nil {
		t.Fatalf("Could not build a delivery point: %v", err)
	}
	return dp
}

// pushLive sends one notification to one token and returns the single result.
func pushLive(t *testing.T, service *pushService, psp *push.PushServiceProvider, dp *push.DeliveryPoint, notif *push.Notification) *push.Result {
	t.Helper()

	dpQueue := make(chan *push.DeliveryPoint, 1)
	dpQueue <- dp
	close(dpQueue)
	resQueue := make(chan *push.Result, 4)

	service.Push(psp, dpQueue, resQueue, notif)

	var results []*push.Result
	for res := range resQueue {
		results = append(results, res)
	}
	if len(results) != 1 {
		t.Fatalf("Expected exactly one result, got %d", len(results))
	}
	return results[0]
}

// TestLiveCredentialsAreAcceptedByGoogle is the test that answers the question
// the FCM migration actually raised: does the new auth work at all?
//
// It sends to a registration token that cannot exist, so nothing is delivered
// and no device is needed. What matters is *which* rejection comes back. An
// INVALID_ARGUMENT means the request reached FCM's message handling, which it
// only does after the bearer token has been minted, sent and accepted. That is
// the whole auth path -- service account parsing, JWT assertion, token exchange
// with Google's OAuth2 endpoint, and the Authorization header -- confirmed
// end to end by a response that has nothing to do with auth.
//
// The failures this is really watching for are the two that would mean the
// migration is broken:
//
//   - a BadPushServiceProvider naming HTTP 401 or 403: the credentials were
//     rejected, or the Cloud Messaging API is not enabled on the project
//   - the "non-JSON body" error: something answered that is not the v1 API,
//     which is what the decommissioned legacy endpoint does
func TestLiveCredentialsAreAcceptedByGoogle(t *testing.T) {
	service, psp := liveService(t)
	defer service.Finalize()

	dp := liveDeliveryPoint(t, service, "uniqush-live-test-not-a-real-registration-token")
	notif := &push.Notification{Data: map[string]string{"msg": "uniqush live credential check"}}

	result := pushLive(t, service, psp, dp, notif)
	if result.Err == nil {
		t.Fatalf("A fabricated registration token was accepted, which should be impossible. MsgID=%q", result.MsgID)
	}

	switch err := result.Err.(type) {
	case *push.BadNotification:
		// INVALID_ARGUMENT: FCM parsed our message and rejected the token.
		// This is the expected outcome, and it proves auth succeeded.
		t.Logf("FCM rejected the fabricated token, as expected: %v", err)

	case *push.UnsubscribeUpdate:
		// UNREGISTERED. Unlikely for a token of this shape, but it also only
		// happens after authentication, so it is equally good news.
		t.Logf("FCM reported the fabricated token as unregistered: %v", err)

	case *push.BadPushServiceProvider:
		t.Fatalf("FCM rejected our credentials: %v\n"+
			"Check that %s points at a service account JSON for project %s, that the service "+
			"account has the Firebase Cloud Messaging API Admin role, and that the Firebase "+
			"Cloud Messaging API (V1) is enabled in the Google Cloud console.",
			err, envCredentials, os.Getenv(envProjectID))

	case *push.ConnectionError:
		t.Fatalf("Could not reach FCM: %v", err)

	default:
		message := result.Err.Error()
		if strings.Contains(message, "non-JSON body") {
			t.Fatalf("Something other than the v1 API answered: %v\n"+
				"An HTML 404 here means the request went to the legacy endpoint, "+
				"which was decommissioned on 2024-06-20.", result.Err)
		}
		t.Fatalf("Unexpected error type %T from FCM: %v", result.Err, result.Err)
	}
}

// TestLiveUnknownTokenIsNotUnsubscribed guards the conservative unsubscribe
// rule against the real API rather than a mock.
//
// v1 collapsed a lot of distinct legacy failures into INVALID_ARGUMENT --
// a malformed token, an oversized payload, a non-string data value. Treating
// that as "this device is gone" would delete working subscriptions because of
// a bad payload, so only UNREGISTERED and SENDER_ID_MISMATCH unsubscribe.
//
// That reasoning depends on a claim about what Google actually returns for a
// malformed token, which is worth checking rather than assuming. If FCM ever
// starts answering UNREGISTERED here, this test says so, and it becomes safe
// to simplify the mapping.
func TestLiveUnknownTokenIsNotUnsubscribed(t *testing.T) {
	service, psp := liveService(t)
	defer service.Finalize()

	dp := liveDeliveryPoint(t, service, "!!! definitely not a token !!!")
	notif := &push.Notification{Data: map[string]string{"msg": "uniqush live mapping check"}}

	result := pushLive(t, service, psp, dp, notif)
	if _, unsubscribed := result.Err.(*push.UnsubscribeUpdate); unsubscribed {
		t.Errorf("A malformed token produced an unsubscribe. If FCM now returns UNREGISTERED "+
			"for malformed tokens, the mapping in interpretResponse can be simplified; "+
			"until then this would delete live subscriptions. Error: %v", result.Err)
	}
}

// TestLivePayloadRulesMatchGoogles checks that what uniqush rejects locally is
// what FCM would have rejected anyway.
//
// uniqush refuses a non-string value in "data" itself, with a message naming
// the offending field, rather than letting FCM answer with an opaque 400. That
// is only an improvement if the two agree about the rule, so this asserts the
// local rejection happens before any network call.
func TestLivePayloadRulesMatchGoogles(t *testing.T) {
	service, psp := liveService(t)
	defer service.Finalize()

	dp := liveDeliveryPoint(t, service, "uniqush-live-test-not-a-real-registration-token")
	notif := &push.Notification{Data: map[string]string{
		"uniqush.payload.fcm": `{"count": 3}`,
	}}

	result := pushLive(t, service, psp, dp, notif)
	if result.Err == nil {
		t.Fatal("Expected a non-string data value to be rejected")
	}
	if _, isBad := result.Err.(*push.BadNotification); !isBad {
		t.Errorf("Expected a BadNotification naming the field, got %T: %v", result.Err, result.Err)
	}
	if !strings.Contains(result.Err.Error(), "count") {
		t.Errorf("Expected the error to name the offending field, got: %v", result.Err)
	}
}

// TestLiveDeliveryToRealDevice is the only test here that proves delivery.
//
// Everything above confirms that Google accepts uniqush's credentials and
// understands its requests. None of it confirms that a notification arrives,
// which needs a registration token from a real app instance -- see
// examples/fcm-demo for the shortest route to one.
//
// A 200 from FCM means accepted for delivery, not delivered, so the assertion
// here is deliberately modest: check for the message name FCM assigns, and
// print it. Whether the notification actually showed up is a question for the
// device.
func TestLiveDeliveryToRealDevice(t *testing.T) {
	regID := os.Getenv(envRegID)
	if regID == "" {
		t.Skipf("Set %s to a real registration token to test delivery", envRegID)
	}

	service, psp := liveService(t)
	defer service.Finalize()

	dp := liveDeliveryPoint(t, service, regID)
	notif := &push.Notification{Data: map[string]string{
		"msg":    "Hello from uniqush-push",
		"source": "live_test.go",
		"ttl":    "60",
	}}

	result := pushLive(t, service, psp, dp, notif)
	if result.Err != nil {
		t.Fatalf("Push to a real device failed: %v", result.Err)
	}
	// MsgID is "<psp name>:projects/<project>/messages/<id>". An empty one means
	// a 200 whose body was not the expected {"name": ...}, which would be a
	// change in the API worth noticing.
	if result.MsgID == "" {
		t.Error("FCM returned success but no message name")
	}
	t.Logf("FCM accepted the push: %s", result.MsgID)
	t.Log("Now check the device: a data message with msg=\"Hello from uniqush-push\" should have arrived.")
}
